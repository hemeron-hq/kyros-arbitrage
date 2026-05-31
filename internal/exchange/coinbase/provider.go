package coinbase

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/coder/websocket"
	json "github.com/goccy/go-json"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

const (
	defaultRESTBaseURL = "https://api.exchange.coinbase.com"
	defaultWSURL       = "wss://advanced-trade-ws.coinbase.com"
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
	return exchange.Coinbase
}

func (p *Provider) Stream(ctx context.Context, binding exchange.Binding, depth int, out chan<- exchange.OrderBookSnapshot) error {
	conn, _, err := websocket.Dial(ctx, p.wsURL(), nil)
	if err != nil {
		return fmt.Errorf("dial coinbase websocket: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 20)

	payload, err := p.subscribePayload(binding)
	if err != nil {
		return err
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		return fmt.Errorf("subscribe coinbase websocket: %w", err)
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
	endpoint := strings.TrimRight(p.restBaseURL(), "/") + "/products/" + url.PathEscape(binding.RESTSymbol) + "/book?level=2"
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
		return exchange.OrderBookSnapshot{}, fmt.Errorf("coinbase depth status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}
	now := p.now()
	return parseREST(payload, binding, depth, now, now.Sub(startedAt))
}

func (p *Provider) subscribePayload(binding exchange.Binding) ([]byte, error) {
	return json.Marshal(map[string]any{
		"type":        "subscribe",
		"product_ids": []string{binding.WebSocketSymbol},
		"channel":     "level2",
	})
}

type parser struct {
	book sideBook
}

func newParser() *parser {
	return &parser{book: newSideBook()}
}

func (p *parser) Parse(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time) (exchange.OrderBookSnapshot, bool, error) {
	var message wsMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, false, fmt.Errorf("decode coinbase l2: %w", err)
	}
	if message.Channel != "l2_data" {
		return exchange.OrderBookSnapshot{}, false, nil
	}
	eventTime := parseTime(message.Timestamp)
	for _, event := range message.Events {
		if event.ProductID != "" && event.ProductID != binding.WebSocketSymbol {
			continue
		}
		if event.Type == "snapshot" {
			p.book = newSideBook()
		}
		for _, update := range event.Updates {
			if update.EventTime != "" && eventTime.IsZero() {
				eventTime = parseTime(update.EventTime)
			}
			if err := p.book.apply(strings.ToLower(update.Side), update.PriceLevel, update.NewQuantity); err != nil {
				return exchange.OrderBookSnapshot{}, false, err
			}
		}
	}
	out, err := snapshot(binding, p.book.sorted(depth, true), p.book.sorted(depth, false), receivedAt, eventTime, 0, 0, exchange.TransportWebSocket, "level2 live")
	return out, err == nil, err
}

type wsMessage struct {
	Channel   string `json:"channel"`
	Timestamp string `json:"timestamp"`
	Events    []struct {
		Type      string `json:"type"`
		ProductID string `json:"product_id"`
		Updates   []struct {
			Side        string `json:"side"`
			PriceLevel  string `json:"price_level"`
			NewQuantity string `json:"new_quantity"`
			EventTime   string `json:"event_time"`
		} `json:"updates"`
	} `json:"events"`
}

func parseREST(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time, latency time.Duration) (exchange.OrderBookSnapshot, error) {
	var message struct {
		Sequence int64               `json:"sequence"`
		Bids     [][]json.RawMessage `json:"bids"`
		Asks     [][]json.RawMessage `json:"asks"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("decode coinbase REST book: %w", err)
	}
	bids, err := parseMixedLevels(message.Bids, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}
	asks, err := parseMixedLevels(message.Asks, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}
	return snapshot(binding, bids, asks, receivedAt, time.Time{}, latency, message.Sequence, exchange.TransportPolling, "REST level2 fallback")
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

func parseStringLevels(raw [][]string, depth int) ([]exchange.PriceLevel, error) {
	depth = min(max(depth, 0), len(raw))
	levels := make([]exchange.PriceLevel, 0, depth)
	for i := 0; i < depth; i++ {
		if len(raw[i]) < 2 {
			return nil, fmt.Errorf("level %d has %d fields", i, len(raw[i]))
		}
		level, err := exchange.NewPriceLevel(raw[i][0], raw[i][1])
		if err != nil {
			return nil, err
		}
		if !level.Quantity.IsZero() {
			levels = append(levels, level)
		}
	}
	return levels, nil
}

func parseMixedLevels(raw [][]json.RawMessage, depth int) ([]exchange.PriceLevel, error) {
	depth = min(max(depth, 0), len(raw))
	levels := make([]exchange.PriceLevel, 0, depth)
	for i := 0; i < depth; i++ {
		if len(raw[i]) < 2 {
			return nil, fmt.Errorf("level %d has %d fields", i, len(raw[i]))
		}
		price, err := rawText(raw[i][0])
		if err != nil {
			return nil, fmt.Errorf("level %d price: %w", i, err)
		}
		quantity, err := rawText(raw[i][1])
		if err != nil {
			return nil, fmt.Errorf("level %d quantity: %w", i, err)
		}
		level, err := exchange.NewPriceLevel(price, quantity)
		if err != nil {
			return nil, err
		}
		if !level.Quantity.IsZero() {
			levels = append(levels, level)
		}
	}
	return levels, nil
}

func rawText(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return "", err
	}
	return number.String(), nil
}

func snapshot(binding exchange.Binding, bids []exchange.PriceLevel, asks []exchange.PriceLevel, receivedAt time.Time, exchangeTime time.Time, latency time.Duration, sequence int64, transport exchange.Transport, message string) (exchange.OrderBookSnapshot, error) {
	if len(bids) == 0 || len(asks) == 0 {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("coinbase depth has empty side")
	}
	if !exchangeTime.IsZero() && latency == 0 && receivedAt.After(exchangeTime) {
		latency = receivedAt.Sub(exchangeTime)
	}
	return exchange.OrderBookSnapshot{
		Exchange:     exchange.Coinbase,
		Market:       binding.Market,
		Bids:         bids,
		Asks:         asks,
		ReceivedAt:   receivedAt,
		ExchangeTime: exchangeTime,
		Latency:      latency,
		Sequence:     sequence,
		Transport:    transport,
		Status:       exchange.StatusLive,
		Message:      message,
	}, nil
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err == nil {
		return parsed
	}
	return time.Time{}
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
