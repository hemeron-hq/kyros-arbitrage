package publicfeed

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	json "github.com/goccy/go-json"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

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

type coinbaseParser struct {
	book sideBook
}

func newCoinbaseParser() *coinbaseParser {
	return &coinbaseParser{book: newSideBook()}
}

func (p *coinbaseParser) Parse(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time) (exchange.OrderBookSnapshot, bool, error) {
	var message coinbaseWSMessage
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
			side := strings.ToLower(update.Side)
			if err := p.book.apply(side, update.PriceLevel, update.NewQuantity); err != nil {
				return exchange.OrderBookSnapshot{}, false, err
			}
		}
	}
	bids := p.book.sorted(depth, true)
	asks := p.book.sorted(depth, false)
	out, err := snapshot(exchange.Coinbase, binding, bids, asks, receivedAt, eventTime, 0, 0, exchange.TransportWebSocket, "level2 live")
	return out, err == nil, err
}

type coinbaseWSMessage struct {
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

func parseCoinbaseREST(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time, latency time.Duration) (exchange.OrderBookSnapshot, error) {
	var message struct {
		Sequence int64      `json:"sequence"`
		Bids     [][]string `json:"bids"`
		Asks     [][]string `json:"asks"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("decode coinbase REST book: %w", err)
	}
	bids, err := parseStringLevels(message.Bids, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}
	asks, err := parseStringLevels(message.Asks, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}
	return snapshot(exchange.Coinbase, binding, bids, asks, receivedAt, time.Time{}, latency, message.Sequence, exchange.TransportPolling, "REST level2 fallback")
}

type okxParser struct{}

func (okxParser) Parse(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time) (exchange.OrderBookSnapshot, bool, error) {
	var message okxBookResponse
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, false, fmt.Errorf("decode okx book: %w", err)
	}
	if len(message.Data) == 0 {
		return exchange.OrderBookSnapshot{}, false, nil
	}
	return okxSnapshot(message.Data[0], binding, depth, receivedAt, exchange.TransportWebSocket)
}

func parseOKXREST(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time, latency time.Duration) (exchange.OrderBookSnapshot, error) {
	var message okxBookResponse
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("decode okx REST book: %w", err)
	}
	if len(message.Data) == 0 {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("okx REST returned no data")
	}
	out, _, err := okxSnapshot(message.Data[0], binding, depth, receivedAt, exchange.TransportPolling)
	out.Latency = latency
	return out, err
}

type okxBookResponse struct {
	Data []struct {
		Bids  [][]string `json:"bids"`
		Asks  [][]string `json:"asks"`
		TS    string     `json:"ts"`
		SeqID any        `json:"seqId"`
	} `json:"data"`
}

func okxSnapshot(data struct {
	Bids  [][]string `json:"bids"`
	Asks  [][]string `json:"asks"`
	TS    string     `json:"ts"`
	SeqID any        `json:"seqId"`
}, binding exchange.Binding, depth int, receivedAt time.Time, transport exchange.Transport) (exchange.OrderBookSnapshot, bool, error) {
	bids, err := parseStringLevels(data.Bids, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, false, err
	}
	asks, err := parseStringLevels(data.Asks, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, false, err
	}
	out, err := snapshot(exchange.OKX, binding, bids, asks, receivedAt, unixMillisString(data.TS), 0, parseInt64Any(data.SeqID), transport, "books live")
	return out, err == nil, err
}

type bybitParser struct{}

func (bybitParser) Parse(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time) (exchange.OrderBookSnapshot, bool, error) {
	var message bybitBookMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, false, fmt.Errorf("decode bybit book: %w", err)
	}
	if message.Topic == "" || message.Data.Symbol == "" {
		return exchange.OrderBookSnapshot{}, false, nil
	}
	return bybitSnapshot(message.Data, binding, depth, receivedAt, unixMillis(message.TS), exchange.TransportWebSocket)
}

func parseBybitREST(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time, latency time.Duration) (exchange.OrderBookSnapshot, error) {
	var message struct {
		Result bybitBookData `json:"result"`
		Time   int64         `json:"time"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("decode bybit REST book: %w", err)
	}
	out, _, err := bybitSnapshot(message.Result, binding, depth, receivedAt, unixMillis(message.Time), exchange.TransportPolling)
	out.Latency = latency
	return out, err
}

type bybitBookMessage struct {
	Topic string        `json:"topic"`
	TS    int64         `json:"ts"`
	Data  bybitBookData `json:"data"`
}

type bybitBookData struct {
	Symbol string     `json:"s"`
	Bids   [][]string `json:"b"`
	Asks   [][]string `json:"a"`
	Update int64      `json:"u"`
}

func bybitSnapshot(data bybitBookData, binding exchange.Binding, depth int, receivedAt time.Time, exchangeTime time.Time, transport exchange.Transport) (exchange.OrderBookSnapshot, bool, error) {
	bids, err := parseStringLevels(data.Bids, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, false, err
	}
	asks, err := parseStringLevels(data.Asks, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, false, err
	}
	out, err := snapshot(exchange.Bybit, binding, bids, asks, receivedAt, exchangeTime, 0, data.Update, transport, "orderbook.50 live")
	return out, err == nil, err
}

type bitfinexParser struct {
	book sideBook
}

func newBitfinexParser() *bitfinexParser {
	return &bitfinexParser{book: newSideBook()}
}

func (p *bitfinexParser) Parse(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time) (exchange.OrderBookSnapshot, bool, error) {
	var event map[string]any
	if err := json.Unmarshal(payload, &event); err == nil && event["event"] != nil {
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
	if raw[1][0] == '[' {
		var nested []json.RawMessage
		if err := json.Unmarshal(raw[1], &nested); err != nil {
			return exchange.OrderBookSnapshot{}, false, err
		}
		if len(nested) > 0 && nested[0][0] == '[' {
			p.book = newSideBook()
			for _, item := range nested {
				if err := p.applyBitfinexLevel(item); err != nil {
					return exchange.OrderBookSnapshot{}, false, err
				}
			}
		} else if err := p.applyBitfinexLevel(raw[1]); err != nil {
			return exchange.OrderBookSnapshot{}, false, err
		}
	}
	out, err := snapshot(exchange.Bitfinex, binding, p.book.sorted(depth, true), p.book.sorted(depth, false), receivedAt, time.Time{}, 0, 0, exchange.TransportWebSocket, "book v2 live")
	return out, err == nil, err
}

func (p *bitfinexParser) applyBitfinexLevel(raw json.RawMessage) error {
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

func parseBitfinexREST(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time, latency time.Duration) (exchange.OrderBookSnapshot, error) {
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
	return snapshot(exchange.Bitfinex, binding, book.sorted(depth, true), book.sorted(depth, false), receivedAt, time.Time{}, latency, 0, exchange.TransportPolling, "REST book fallback")
}

type kuCoinParser struct{}

func (kuCoinParser) Parse(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time) (exchange.OrderBookSnapshot, bool, error) {
	var message struct {
		Type    string `json:"type"`
		Subject string `json:"subject"`
		Data    struct {
			Bids      [][]string `json:"bids"`
			Asks      [][]string `json:"asks"`
			Timestamp int64      `json:"timestamp"`
			Sequence  string     `json:"sequence"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, false, fmt.Errorf("decode kucoin book: %w", err)
	}
	if message.Type != "message" || len(message.Data.Bids) == 0 || len(message.Data.Asks) == 0 {
		return exchange.OrderBookSnapshot{}, false, nil
	}
	return kuCoinSnapshot(message.Data.Bids, message.Data.Asks, message.Data.Timestamp, message.Data.Sequence, binding, depth, receivedAt, exchange.TransportWebSocket)
}

func parseKuCoinREST(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time, latency time.Duration) (exchange.OrderBookSnapshot, error) {
	var message struct {
		Data struct {
			Bids     [][]string `json:"bids"`
			Asks     [][]string `json:"asks"`
			Time     int64      `json:"time"`
			Sequence string     `json:"sequence"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("decode kucoin REST book: %w", err)
	}
	out, _, err := kuCoinSnapshot(message.Data.Bids, message.Data.Asks, message.Data.Time, message.Data.Sequence, binding, depth, receivedAt, exchange.TransportPolling)
	out.Latency = latency
	return out, err
}

func kuCoinSnapshot(rawBids [][]string, rawAsks [][]string, ts int64, seq string, binding exchange.Binding, depth int, receivedAt time.Time, transport exchange.Transport) (exchange.OrderBookSnapshot, bool, error) {
	bids, err := parseStringLevels(rawBids, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, false, err
	}
	asks, err := parseStringLevels(rawAsks, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, false, err
	}
	sequence, _ := strconv.ParseInt(seq, 10, 64)
	out, err := snapshot(exchange.KuCoin, binding, bids, asks, receivedAt, unixMillis(ts), 0, sequence, transport, "level2Depth50 live")
	return out, err == nil, err
}

type gateParser struct{}

func (gateParser) Parse(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time) (exchange.OrderBookSnapshot, bool, error) {
	var message struct {
		Event  string `json:"event"`
		Result struct {
			Timestamp int64           `json:"t"`
			ID        int64           `json:"id"`
			Bids      json.RawMessage `json:"bids"`
			Asks      json.RawMessage `json:"asks"`
		} `json:"result"`
		TimeMS int64 `json:"time_ms"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, false, fmt.Errorf("decode gate book: %w", err)
	}
	if message.Event != "update" && message.Event != "all" {
		return exchange.OrderBookSnapshot{}, false, nil
	}
	return gateSnapshot(message.Result.Bids, message.Result.Asks, message.Result.ID, message.TimeMS, binding, depth, receivedAt, exchange.TransportWebSocket)
}

func parseGateREST(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time, latency time.Duration) (exchange.OrderBookSnapshot, error) {
	var message struct {
		ID   int64           `json:"id"`
		Bids json.RawMessage `json:"bids"`
		Asks json.RawMessage `json:"asks"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("decode gate REST book: %w", err)
	}
	out, _, err := gateSnapshot(message.Bids, message.Asks, message.ID, 0, binding, depth, receivedAt, exchange.TransportPolling)
	out.Latency = latency
	return out, err
}

func gateSnapshot(rawBids json.RawMessage, rawAsks json.RawMessage, sequence int64, timeMS int64, binding exchange.Binding, depth int, receivedAt time.Time, transport exchange.Transport) (exchange.OrderBookSnapshot, bool, error) {
	bids, err := parseFlexibleLevels(rawBids, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, false, err
	}
	asks, err := parseFlexibleLevels(rawAsks, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, false, err
	}
	out, err := snapshot(exchange.Gate, binding, bids, asks, receivedAt, unixMillis(timeMS), 0, sequence, transport, "spot.order_book live")
	return out, err == nil, err
}

type bitstampParser struct{}

func (bitstampParser) Parse(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time) (exchange.OrderBookSnapshot, bool, error) {
	var message struct {
		Event string `json:"event"`
		Data  struct {
			Microtimestamp string     `json:"microtimestamp"`
			Bids           [][]string `json:"bids"`
			Asks           [][]string `json:"asks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, false, fmt.Errorf("decode bitstamp book: %w", err)
	}
	if message.Event != "data" {
		return exchange.OrderBookSnapshot{}, false, nil
	}
	return bitstampSnapshot(message.Data.Bids, message.Data.Asks, message.Data.Microtimestamp, binding, depth, receivedAt, exchange.TransportWebSocket)
}

func parseBitstampREST(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time, latency time.Duration) (exchange.OrderBookSnapshot, error) {
	var message struct {
		Microtimestamp string     `json:"microtimestamp"`
		Bids           [][]string `json:"bids"`
		Asks           [][]string `json:"asks"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("decode bitstamp REST book: %w", err)
	}
	out, _, err := bitstampSnapshot(message.Bids, message.Asks, message.Microtimestamp, binding, depth, receivedAt, exchange.TransportPolling)
	out.Latency = latency
	return out, err
}

func bitstampSnapshot(rawBids [][]string, rawAsks [][]string, timestamp string, binding exchange.Binding, depth int, receivedAt time.Time, transport exchange.Transport) (exchange.OrderBookSnapshot, bool, error) {
	bids, err := parseStringLevels(rawBids, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, false, err
	}
	asks, err := parseStringLevels(rawAsks, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, false, err
	}
	out, err := snapshot(exchange.Bitstamp, binding, bids, asks, receivedAt, unixMicroString(timestamp), 0, 0, transport, "order_book live")
	return out, err == nil, err
}

type geminiParser struct {
	book sideBook
}

func newGeminiParser() *geminiParser {
	return &geminiParser{book: newSideBook()}
}

func (p *geminiParser) Parse(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time) (exchange.OrderBookSnapshot, bool, error) {
	var depthMessage struct {
		Stream string `json:"stream"`
		Data   struct {
			EventTime int64           `json:"E"`
			Bids      json.RawMessage `json:"bids"`
			Asks      json.RawMessage `json:"asks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &depthMessage); err == nil && depthMessage.Stream != "" {
		bids, err := parseFlexibleLevels(depthMessage.Data.Bids, depth)
		if err != nil {
			return exchange.OrderBookSnapshot{}, false, err
		}
		asks, err := parseFlexibleLevels(depthMessage.Data.Asks, depth)
		if err != nil {
			return exchange.OrderBookSnapshot{}, false, err
		}
		out, err := snapshot(exchange.Gemini, binding, bids, asks, receivedAt, unixMillis(depthMessage.Data.EventTime), 0, 0, exchange.TransportWebSocket, "depth10 live")
		return out, err == nil, err
	}

	var legacy struct {
		Type      string `json:"type"`
		Timestamp int64  `json:"timestampms"`
		Events    []struct {
			Type      string `json:"type"`
			Side      string `json:"side"`
			Price     string `json:"price"`
			Remaining string `json:"remaining"`
		} `json:"events"`
	}
	if err := json.Unmarshal(payload, &legacy); err != nil {
		return exchange.OrderBookSnapshot{}, false, fmt.Errorf("decode gemini book: %w", err)
	}
	if len(legacy.Events) == 0 {
		return exchange.OrderBookSnapshot{}, false, nil
	}
	for _, event := range legacy.Events {
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
	out, err := snapshot(exchange.Gemini, binding, p.book.sorted(depth, true), p.book.sorted(depth, false), receivedAt, unixMillis(legacy.Timestamp), 0, 0, exchange.TransportWebSocket, "marketdata live")
	return out, err == nil, err
}

func parseGeminiREST(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time, latency time.Duration) (exchange.OrderBookSnapshot, error) {
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
	return snapshot(exchange.Gemini, binding, bids, asks, receivedAt, time.Time{}, latency, 0, exchange.TransportPolling, "REST book fallback")
}

func parseFlexibleLevels(raw json.RawMessage, depth int) ([]exchange.PriceLevel, error) {
	var stringLevels [][]string
	if err := json.Unmarshal(raw, &stringLevels); err == nil && len(stringLevels) > 0 {
		return parseStringLevels(stringLevels, depth)
	}
	var objectLevels []objectLevel
	if err := json.Unmarshal(raw, &objectLevels); err == nil && len(objectLevels) > 0 {
		return parseObjectLevels(objectLevels, depth)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	return nil, fmt.Errorf("unsupported level shape")
}

func parseInt64Any(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	default:
		return 0
	}
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
