package publicfeed

import (
	"fmt"
	"strconv"
	"time"

	json "github.com/goccy/go-json"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

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
