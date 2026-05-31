package bitfinex

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	json "github.com/goccy/go-json"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

const (
	defaultRESTBaseURL = "https://api-pub.bitfinex.com"
	defaultWSURL       = "wss://api-pub.bitfinex.com/ws/2"
)

type Provider struct {
	HTTPClient  *http.Client
	RESTBaseURL string
	WSURL       string
	Now         func() time.Time
}

var _ exchange.MarketDataProvider = (*Provider)(nil)

type Option func(*Provider)

func New(options ...Option) *Provider {
	provider := &Provider{
		HTTPClient:  http.DefaultClient,
		RESTBaseURL: defaultRESTBaseURL,
		WSURL:       defaultWSURL,
		Now:         time.Now,
	}
	for _, option := range options {
		option(provider)
	}
	return provider
}

func (p *Provider) Exchange() exchange.ID {
	return exchange.Bitfinex
}

func (p *Provider) Stream(ctx context.Context, binding exchange.Binding, depth int, out chan<- exchange.OrderBookSnapshot) error {
	conn, _, err := websocket.Dial(ctx, p.wsURL(), nil)
	if err != nil {
		return fmt.Errorf("dial bitfinex websocket: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 20)

	payload, err := p.subscribePayload(binding, depth)
	if err != nil {
		return err
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		return fmt.Errorf("subscribe bitfinex websocket: %w", err)
	}

	parser := newParser()
	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		snapshot, ok, err := parser.Parse(payload, binding, depth, p.now())
		if err != nil {
			return err
		}
		if ok {
			out <- snapshot
		}
	}
}

func (p *Provider) Poll(ctx context.Context, binding exchange.Binding, depth int) (exchange.OrderBookSnapshot, error) {
	startedAt := p.now()
	endpoint := fmt.Sprintf("%s/v2/book/%s/P0?len=%d", strings.TrimRight(p.restBaseURL(), "/"), url.PathEscape(binding.RESTSymbol), bookLength(depth))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return exchange.OrderBookSnapshot{}, fmt.Errorf("bitfinex depth status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}
	now := p.now()
	return parseREST(payload, binding, depth, now, now.Sub(startedAt))
}

func (p *Provider) subscribePayload(binding exchange.Binding, depth int) ([]byte, error) {
	return json.Marshal(map[string]any{
		"event":   "subscribe",
		"channel": "book",
		"symbol":  binding.WebSocketSymbol,
		"prec":    "P0",
		"freq":    "F0",
		"len":     strconv.Itoa(bookLength(depth)),
	})
}

func bookLength(depth int) int {
	switch {
	case depth <= 1:
		return 1
	case depth <= 25:
		return 25
	default:
		return 100
	}
}

type parser struct {
	book sideBook
}

func newParser() *parser {
	return &parser{book: newSideBook()}
}

func (p *parser) Parse(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time) (exchange.OrderBookSnapshot, bool, error) {
	var event map[string]any
	if err := json.Unmarshal(payload, &event); err == nil && event["event"] != nil {
		if event["event"] == "error" {
			return exchange.OrderBookSnapshot{}, false, fmt.Errorf("bitfinex websocket error: %v", event["msg"])
		}
		return exchange.OrderBookSnapshot{}, false, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil || len(raw) < 2 {
		return exchange.OrderBookSnapshot{}, false, nil
	}
	var heartbeat string
	if err := json.Unmarshal(raw[1], &heartbeat); err == nil && heartbeat == "hb" {
		return exchange.OrderBookSnapshot{}, false, nil
	}
	if len(raw[1]) == 0 || raw[1][0] != '[' {
		return exchange.OrderBookSnapshot{}, false, nil
	}
	var nested []json.RawMessage
	if err := json.Unmarshal(raw[1], &nested); err != nil {
		return exchange.OrderBookSnapshot{}, false, err
	}
	if len(nested) > 0 && len(nested[0]) > 0 && nested[0][0] == '[' {
		p.book = newSideBook()
		for _, item := range nested {
			if err := p.applyLevel(item); err != nil {
				return exchange.OrderBookSnapshot{}, false, err
			}
		}
	} else if err := p.applyLevel(raw[1]); err != nil {
		return exchange.OrderBookSnapshot{}, false, err
	}
	out, err := snapshot(binding, p.book.sorted(depth, true), p.book.sorted(depth, false), receivedAt, 0, exchange.TransportWebSocket, "book v2 live")
	return out, err == nil, err
}

func (p *parser) applyLevel(raw json.RawMessage) error {
	var level []float64
	if err := json.Unmarshal(raw, &level); err != nil {
		return err
	}
	if len(level) < 3 {
		return fmt.Errorf("bitfinex level has %d fields", len(level))
	}
	price := strconv.FormatFloat(level[0], 'f', -1, 64)
	quantity := strconv.FormatFloat(absFloat(level[2]), 'f', -1, 64)
	side := "bid"
	if level[2] < 0 {
		side = "ask"
	}
	if int(level[1]) == 0 {
		quantity = "0"
	}
	return p.book.apply(side, price, quantity)
}

func parseREST(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time, latency time.Duration) (exchange.OrderBookSnapshot, error) {
	var rows [][]float64
	if err := json.Unmarshal(payload, &rows); err != nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("decode bitfinex REST book: %w", err)
	}
	book := newSideBook()
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		side := "bid"
		if row[2] < 0 {
			side = "ask"
		}
		if err := book.apply(side, strconv.FormatFloat(row[0], 'f', -1, 64), strconv.FormatFloat(absFloat(row[2]), 'f', -1, 64)); err != nil {
			return exchange.OrderBookSnapshot{}, err
		}
	}
	return snapshot(binding, book.sorted(depth, true), book.sorted(depth, false), receivedAt, latency, exchange.TransportPolling, "REST book fallback")
}

type sideBook struct {
	bids map[string]exchange.PriceLevel
	asks map[string]exchange.PriceLevel
}

func newSideBook() sideBook {
	return sideBook{
		bids: make(map[string]exchange.PriceLevel),
		asks: make(map[string]exchange.PriceLevel),
	}
}

func (b sideBook) apply(side string, price string, size string) error {
	level, err := exchange.NewPriceLevel(price, size)
	if err != nil {
		return err
	}
	target := b.bids
	if side == "ask" || side == "offer" || side == "sell" {
		target = b.asks
	}
	if level.Quantity.IsZero() {
		delete(target, level.PriceText)
		return nil
	}
	target[level.PriceText] = level
	return nil
}

func (b sideBook) sorted(depth int, bids bool) []exchange.PriceLevel {
	values := make([]exchange.PriceLevel, 0, len(b.bids))
	if bids {
		for _, value := range b.bids {
			values = append(values, value)
		}
		sort.Slice(values, func(i, j int) bool { return values[i].Price.Cmp(values[j].Price) > 0 })
	} else {
		values = values[:0]
		for _, value := range b.asks {
			values = append(values, value)
		}
		sort.Slice(values, func(i, j int) bool { return values[i].Price.Cmp(values[j].Price) < 0 })
	}
	if depth > 0 && len(values) > depth {
		values = values[:depth]
	}
	return values
}

func snapshot(binding exchange.Binding, bids []exchange.PriceLevel, asks []exchange.PriceLevel, receivedAt time.Time, latency time.Duration, transport exchange.Transport, message string) (exchange.OrderBookSnapshot, error) {
	if len(bids) == 0 || len(asks) == 0 {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("bitfinex depth has empty side")
	}
	return exchange.OrderBookSnapshot{
		Exchange:   exchange.Bitfinex,
		Market:     binding.Market,
		Bids:       bids,
		Asks:       asks,
		ReceivedAt: receivedAt,
		Latency:    latency,
		Transport:  transport,
		Status:     exchange.StatusLive,
		Message:    message,
	}, nil
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func (p *Provider) httpClient() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return http.DefaultClient
}

func (p *Provider) restBaseURL() string {
	if p.RESTBaseURL != "" {
		return p.RESTBaseURL
	}
	return defaultRESTBaseURL
}

func (p *Provider) wsURL() string {
	if p.WSURL != "" {
		return p.WSURL
	}
	return defaultWSURL
}

func (p *Provider) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}
