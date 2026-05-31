package kraken

import (
	"context"
	stdjson "encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	json "github.com/goccy/go-json"
	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

const (
	defaultRESTBaseURL = "https://api.kraken.com"
	defaultWSBaseURL   = "wss://ws.kraken.com/v2"
)

type Provider struct {
	HTTPClient  *http.Client
	RESTBaseURL string
	WSBaseURL   string
	Now         func() time.Time
}

var _ exchange.MarketDataProvider = (*Provider)(nil)

func New() *Provider {
	provider := new(Provider)
	provider.HTTPClient = http.DefaultClient
	provider.RESTBaseURL = defaultRESTBaseURL
	provider.WSBaseURL = defaultWSBaseURL
	provider.Now = time.Now
	return provider
}

func (p *Provider) Venue() exchange.Venue {
	return exchange.VenueKraken
}

func (p *Provider) Stream(ctx context.Context, binding exchange.Binding, depth int, out chan<- exchange.OrderBookSnapshot) error {
	conn, _, err := websocket.Dial(ctx, p.wsBaseURL(), nil)
	if err != nil {
		return fmt.Errorf("dial kraken websocket: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(2 << 20)

	subscribe := subscribeRequest{
		Method: "subscribe",
		Params: subscribeParams{
			Channel:  "book",
			Symbol:   []string{binding.WebSocketSymbol},
			Depth:    depth,
			Snapshot: true,
		},
	}
	payload, err := json.Marshal(subscribe)
	if err != nil {
		return err
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		return fmt.Errorf("subscribe kraken book: %w", err)
	}

	book := NewBook(depth)
	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return err
		}

		snapshot, ok, err := ParseBookMessage(payload, binding, book, depth, p.now())
		if err != nil {
			return err
		}
		if !ok {
			continue
		}

		out <- snapshot
	}
}

func (p *Provider) Poll(ctx context.Context, binding exchange.Binding, depth int) (exchange.OrderBookSnapshot, error) {
	startedAt := p.now()
	endpoint, err := url.Parse(strings.TrimRight(p.restBaseURL(), "/") + "/0/public/Depth")
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}

	query := endpoint.Query()
	query.Set("pair", binding.RESTSymbol)
	query.Set("count", strconv.Itoa(depth))
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
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
		return exchange.OrderBookSnapshot{}, fmt.Errorf("kraken depth status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}

	snapshot, err := ParseRESTDepth(payload, binding, depth, p.now(), p.now().Sub(startedAt))
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}
	return snapshot, nil
}

type subscribeRequest struct {
	Method string          `json:"method"`
	Params subscribeParams `json:"params"`
}

type subscribeParams struct {
	Channel  string   `json:"channel"`
	Symbol   []string `json:"symbol"`
	Depth    int      `json:"depth"`
	Snapshot bool     `json:"snapshot"`
}

type Book struct {
	depth int
	bids  map[string]exchange.PriceLevel
	asks  map[string]exchange.PriceLevel
}

func NewBook(depth int) *Book {
	if depth <= 0 {
		depth = 10
	}

	return &Book{
		depth: depth,
		bids:  make(map[string]exchange.PriceLevel),
		asks:  make(map[string]exchange.PriceLevel),
	}
}

func ParseBookMessage(payload []byte, binding exchange.Binding, book *Book, depth int, receivedAt time.Time) (exchange.OrderBookSnapshot, bool, error) {
	var envelope bookEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return exchange.OrderBookSnapshot{}, false, fmt.Errorf("decode kraken book: %w", err)
	}
	if envelope.Channel != "book" || len(envelope.Data) == 0 {
		return exchange.OrderBookSnapshot{}, false, nil
	}

	if book == nil {
		book = NewBook(depth)
	}
	data := envelope.Data[0]
	if data.Symbol != "" && data.Symbol != binding.WebSocketSymbol {
		return exchange.OrderBookSnapshot{}, false, nil
	}
	if envelope.Type == "snapshot" {
		book.bids = make(map[string]exchange.PriceLevel)
		book.asks = make(map[string]exchange.PriceLevel)
	}

	if err := book.apply(data.Bids, true); err != nil {
		return exchange.OrderBookSnapshot{}, false, err
	}
	if err := book.apply(data.Asks, false); err != nil {
		return exchange.OrderBookSnapshot{}, false, err
	}

	bids := book.sorted(true)
	asks := book.sorted(false)
	if len(bids) == 0 || len(asks) == 0 {
		return exchange.OrderBookSnapshot{}, false, fmt.Errorf("kraken book has empty side")
	}

	if data.Checksum != 0 {
		calculated := Checksum(asks, bids)
		if calculated != data.Checksum {
			return exchange.OrderBookSnapshot{}, false, fmt.Errorf("kraken checksum mismatch: expected=%d calculated=%d", data.Checksum, calculated)
		}
	}

	exchangeTime := parseKrakenTime(data.Timestamp)
	latency := time.Duration(0)
	if !exchangeTime.IsZero() && receivedAt.After(exchangeTime) {
		latency = receivedAt.Sub(exchangeTime)
	}

	return exchange.OrderBookSnapshot{
		Venue:        exchange.VenueKraken,
		Market:       binding.Market,
		Bids:         bids,
		Asks:         asks,
		ReceivedAt:   receivedAt,
		ExchangeTime: exchangeTime,
		Latency:      latency,
		Sequence:     int64(data.Checksum),
		Transport:    exchange.TransportWebSocket,
		Status:       exchange.StatusLive,
		Message:      "depth10 live",
	}, true, nil
}

func ParseRESTDepth(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time, latency time.Duration) (exchange.OrderBookSnapshot, error) {
	var response restDepthResponse
	if err := stdjson.Unmarshal(payload, &response); err != nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("decode kraken rest depth: %w", err)
	}
	if len(response.Errors) > 0 {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("kraken rest error: %s", strings.Join(response.Errors, "; "))
	}
	if len(response.Result) == 0 {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("kraken rest depth empty result")
	}

	var book restBook
	for _, candidate := range response.Result {
		book = candidate
		break
	}

	bids, bidTime, err := parseRESTLevels(book.Bids, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("decode kraken rest bids: %w", err)
	}
	asks, askTime, err := parseRESTLevels(book.Asks, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("decode kraken rest asks: %w", err)
	}
	if len(bids) == 0 || len(asks) == 0 {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("kraken rest depth has empty side")
	}

	exchangeTime := bidTime
	if askTime.After(exchangeTime) {
		exchangeTime = askTime
	}

	return exchange.OrderBookSnapshot{
		Venue:        exchange.VenueKraken,
		Market:       binding.Market,
		Bids:         bids,
		Asks:         asks,
		ReceivedAt:   receivedAt,
		ExchangeTime: exchangeTime,
		Latency:      latency,
		Transport:    exchange.TransportPolling,
		Status:       exchange.StatusLive,
		Message:      "depth10 polling fallback",
	}, nil
}

type bookEnvelope struct {
	Channel string     `json:"channel"`
	Type    string     `json:"type"`
	Data    []bookData `json:"data"`
}

type bookData struct {
	Symbol    string      `json:"symbol"`
	Bids      []bookLevel `json:"bids"`
	Asks      []bookLevel `json:"asks"`
	Checksum  uint32      `json:"checksum"`
	Timestamp string      `json:"timestamp"`
}

type bookLevel struct {
	Price numberText `json:"price"`
	Qty   numberText `json:"qty"`
}

type numberText struct {
	Text    string
	Decimal decimal.Decimal
}

func (n *numberText) UnmarshalJSON(payload []byte) error {
	text := string(payload)
	if strings.HasPrefix(text, `"`) {
		if err := stdjson.Unmarshal(payload, &text); err != nil {
			return err
		}
	}

	value, err := decimal.Parse(text)
	if err != nil {
		return err
	}

	n.Text = text
	n.Decimal = value
	return nil
}

func (b *Book) apply(levels []bookLevel, bid bool) error {
	side := b.asks
	if bid {
		side = b.bids
	}

	for _, raw := range levels {
		level := exchange.PriceLevel{
			Price:        raw.Price.Decimal,
			Quantity:     raw.Qty.Decimal,
			PriceText:    raw.Price.Text,
			QuantityText: raw.Qty.Text,
		}
		key := level.Price.String()
		if level.Quantity.IsZero() {
			delete(side, key)
			continue
		}
		side[key] = level
	}

	b.truncate(bid)
	return nil
}

func (b *Book) sorted(bid bool) []exchange.PriceLevel {
	side := b.asks
	if bid {
		side = b.bids
	}

	levels := make([]exchange.PriceLevel, 0, len(side))
	for _, level := range side {
		levels = append(levels, level)
	}

	sort.Slice(levels, func(i, j int) bool {
		if bid {
			return levels[i].Price.Cmp(levels[j].Price) > 0
		}
		return levels[i].Price.Cmp(levels[j].Price) < 0
	})

	return levels[:min(len(levels), b.depth)]
}

func (b *Book) truncate(bid bool) {
	levels := b.sorted(bid)
	if len(levels) < b.depth {
		return
	}

	keep := make(map[string]struct{}, len(levels))
	for _, level := range levels {
		keep[level.Price.String()] = struct{}{}
	}

	side := b.asks
	if bid {
		side = b.bids
	}
	for key := range side {
		if _, ok := keep[key]; !ok {
			delete(side, key)
		}
	}
}

func Checksum(asks []exchange.PriceLevel, bids []exchange.PriceLevel) uint32 {
	var builder strings.Builder
	for i := 0; i < min(len(asks), 10); i++ {
		builder.WriteString(checksumPart(asks[i].PriceText))
		builder.WriteString(checksumPart(asks[i].QuantityText))
	}
	for i := 0; i < min(len(bids), 10); i++ {
		builder.WriteString(checksumPart(bids[i].PriceText))
		builder.WriteString(checksumPart(bids[i].QuantityText))
	}

	return crc32.ChecksumIEEE([]byte(builder.String()))
}

func checksumPart(value string) string {
	value = strings.ReplaceAll(value, ".", "")
	value = strings.TrimLeft(value, "0")
	if value == "" {
		return "0"
	}
	return value
}

type restDepthResponse struct {
	Errors []string            `json:"error"`
	Result map[string]restBook `json:"result"`
}

type restBook struct {
	Asks [][]stdjson.RawMessage `json:"asks"`
	Bids [][]stdjson.RawMessage `json:"bids"`
}

func parseRESTLevels(rawLevels [][]stdjson.RawMessage, depth int) ([]exchange.PriceLevel, time.Time, error) {
	depth = min(max(depth, 0), len(rawLevels))

	levels := make([]exchange.PriceLevel, 0, depth)
	var exchangeTime time.Time
	for i := 0; i < depth; i++ {
		if len(rawLevels[i]) < 2 {
			return nil, time.Time{}, fmt.Errorf("level %d has %d fields", i, len(rawLevels[i]))
		}

		price, err := rawText(rawLevels[i][0])
		if err != nil {
			return nil, time.Time{}, err
		}
		quantity, err := rawText(rawLevels[i][1])
		if err != nil {
			return nil, time.Time{}, err
		}
		level, err := exchange.NewPriceLevel(price, quantity)
		if err != nil {
			return nil, time.Time{}, err
		}
		if !level.Quantity.IsZero() {
			levels = append(levels, level)
		}
		if len(rawLevels[i]) >= 3 {
			ts, err := rawTimestamp(rawLevels[i][2])
			if err == nil && ts.After(exchangeTime) {
				exchangeTime = ts
			}
		}
	}

	return levels, exchangeTime, nil
}

func rawText(payload stdjson.RawMessage) (string, error) {
	text := string(payload)
	if strings.HasPrefix(text, `"`) {
		if err := stdjson.Unmarshal(payload, &text); err != nil {
			return "", err
		}
	}
	return text, nil
}

func rawTimestamp(payload stdjson.RawMessage) (time.Time, error) {
	text, err := rawText(payload)
	if err != nil {
		return time.Time{}, err
	}

	wholeText, fractionText, ok := strings.Cut(strings.TrimSpace(text), ".")
	whole, err := strconv.ParseInt(wholeText, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	if !ok {
		return time.Unix(whole, 0), nil
	}

	if len(fractionText) > 9 {
		fractionText = fractionText[:9]
	}
	fractionText += strings.Repeat("0", 9-len(fractionText))

	nanos, err := strconv.ParseInt(fractionText, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(whole, nanos), nil
}

func parseKrakenTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
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
