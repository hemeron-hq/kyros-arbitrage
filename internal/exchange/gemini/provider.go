package gemini

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
	defaultRESTBaseURL         = "https://api.gemini.com"
	defaultWSBaseURL           = "wss://api.gemini.com"
	maxAdvisoryExchangeTimeAge = 10 * time.Second
)

type Provider struct {
	HTTPClient  *http.Client
	RESTBaseURL string
	WSBaseURL   string
	Now         func() time.Time
}

var _ exchange.MarketDataProvider = (*Provider)(nil)

type Option func(*Provider)

func New(options ...Option) *Provider {
	provider := &Provider{
		HTTPClient:  http.DefaultClient,
		RESTBaseURL: defaultRESTBaseURL,
		WSBaseURL:   defaultWSBaseURL,
		Now:         time.Now,
	}
	for _, option := range options {
		option(provider)
	}
	return provider
}

func (p *Provider) Exchange() exchange.ID {
	return exchange.Gemini
}

func (p *Provider) Stream(ctx context.Context, binding exchange.Binding, depth int, out chan<- exchange.OrderBookSnapshot) error {
	conn, _, err := websocket.Dial(ctx, p.websocketURL(binding), nil)
	if err != nil {
		return fmt.Errorf("dial gemini websocket: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 20)

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
	endpoint := fmt.Sprintf("%s/v1/book/%s?limit_bids=%d&limit_asks=%d", strings.TrimRight(p.restBaseURL(), "/"), url.PathEscape(binding.RESTSymbol), depth, depth)
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
		return exchange.OrderBookSnapshot{}, fmt.Errorf("gemini depth status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}
	now := p.now()
	return parseREST(payload, binding, depth, now, now.Sub(startedAt))
}

func (p *Provider) websocketURL(binding exchange.Binding) string {
	endpoint := strings.TrimRight(p.wsBaseURL(), "/") + "/v1/marketdata/" + url.PathEscape(binding.WebSocketSymbol)
	query := url.Values{}
	query.Set("top_of_book", "false")
	query.Set("bids", "true")
	query.Set("offers", "true")
	return endpoint + "?" + query.Encode()
}

type parser struct {
	book sideBook
}

func newParser() *parser {
	return &parser{book: newSideBook()}
}

func (p *parser) Parse(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time) (exchange.OrderBookSnapshot, bool, error) {
	var message struct {
		Timestamp int64 `json:"timestampms"`
		Events    []struct {
			Type      string `json:"type"`
			Side      string `json:"side"`
			Price     string `json:"price"`
			Remaining string `json:"remaining"`
		} `json:"events"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, false, fmt.Errorf("decode gemini book: %w", err)
	}
	if len(message.Events) == 0 {
		return exchange.OrderBookSnapshot{}, false, nil
	}
	for _, event := range message.Events {
		if event.Type != "change" {
			continue
		}
		side := "bid"
		if event.Side == "ask" || event.Side == "offer" {
			side = "ask"
		}
		if err := p.book.apply(side, event.Price, event.Remaining); err != nil {
			return exchange.OrderBookSnapshot{}, false, err
		}
	}
	out, err := snapshot(binding, p.book.sorted(depth, true), p.book.sorted(depth, false), receivedAt, advisoryExchangeTime(message.Timestamp, receivedAt), 0, exchange.TransportWebSocket, "marketdata live")
	return out, err == nil, err
}

func parseREST(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time, latency time.Duration) (exchange.OrderBookSnapshot, error) {
	var message struct {
		Bids []objectLevel `json:"bids"`
		Asks []objectLevel `json:"asks"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("decode gemini REST book: %w", err)
	}
	bids, err := parseObjectLevels(message.Bids, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}
	asks, err := parseObjectLevels(message.Asks, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}
	return snapshot(binding, bids, asks, receivedAt, time.Time{}, latency, exchange.TransportPolling, "REST book fallback")
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

type objectLevel struct {
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
	Amount   string `json:"amount"`
}

func parseObjectLevels(raw []objectLevel, depth int) ([]exchange.PriceLevel, error) {
	depth = min(max(depth, 0), len(raw))
	levels := make([]exchange.PriceLevel, 0, depth)
	for i := 0; i < depth; i++ {
		level, err := exchange.NewPriceLevel(raw[i].Price, firstNonEmpty(raw[i].Quantity, raw[i].Amount))
		if err != nil {
			return nil, err
		}
		if !level.Quantity.IsZero() {
			levels = append(levels, level)
		}
	}
	return levels, nil
}

func snapshot(binding exchange.Binding, bids []exchange.PriceLevel, asks []exchange.PriceLevel, receivedAt time.Time, exchangeTime time.Time, latency time.Duration, transport exchange.Transport, message string) (exchange.OrderBookSnapshot, error) {
	if len(bids) == 0 || len(asks) == 0 {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("gemini depth has empty side")
	}
	if !exchangeTime.IsZero() && latency == 0 && receivedAt.After(exchangeTime) {
		latency = receivedAt.Sub(exchangeTime)
	}
	return exchange.OrderBookSnapshot{
		Exchange:     exchange.Gemini,
		Market:       binding.Market,
		Bids:         bids,
		Asks:         asks,
		ReceivedAt:   receivedAt,
		ExchangeTime: exchangeTime,
		Latency:      latency,
		Transport:    transport,
		Status:       exchange.StatusLive,
		Message:      message,
	}, nil
}

func advisoryExchangeTime(timestampMS int64, receivedAt time.Time) time.Time {
	exchangeTime := unixMillis(timestampMS)
	if exchangeTime.IsZero() || receivedAt.IsZero() {
		return time.Time{}
	}
	if exchangeTime.After(receivedAt) {
		return time.Time{}
	}
	if receivedAt.Sub(exchangeTime) > maxAdvisoryExchangeTimeAge {
		return time.Time{}
	}
	return exchangeTime
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func unixMillis(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value)
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

func (p *Provider) wsBaseURL() string {
	if p.WSBaseURL != "" {
		return p.WSBaseURL
	}
	return defaultWSBaseURL
}

func (p *Provider) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}
