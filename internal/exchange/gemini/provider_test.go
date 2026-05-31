package gemini

import (
	"strings"
	"testing"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

func TestParserEmitsSnapshot(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	payload := `{"timestampms":1800000000000,"events":[{"type":"change","side":"bid","price":"100","remaining":"1"},{"type":"change","side":"ask","price":"101","remaining":"2"}]}`

	snapshot, ok, err := newParser().Parse([]byte(payload), testBinding("BTC/USD", "btcusd"), 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected parser to emit a snapshot")
	}
	assertBest(t, snapshot, "100", "101")
	if snapshot.Exchange != exchange.Gemini {
		t.Fatalf("expected gemini snapshot, got %s", snapshot.Exchange)
	}
	if snapshot.ExchangeTime.IsZero() {
		t.Fatal("expected exchange timestamp")
	}
}

func TestParserUsesFreshTimestampForLatency(t *testing.T) {
	receivedAt := time.UnixMilli(1_800_000_000_250)
	payload := `{"timestampms":1800000000000,"events":[{"type":"change","side":"bid","price":"100","remaining":"1"},{"type":"change","side":"ask","price":"101","remaining":"2"}]}`

	snapshot, ok, err := newParser().Parse([]byte(payload), testBinding("BTC/USD", "btcusd"), 10, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected parser to emit a snapshot")
	}
	if snapshot.ExchangeTime.IsZero() {
		t.Fatal("expected fresh exchange timestamp")
	}
	if snapshot.Latency != 250*time.Millisecond {
		t.Fatalf("expected 250ms latency, got %s", snapshot.Latency)
	}
}

func TestParserIgnoresStaleTimestampForLatency(t *testing.T) {
	receivedAt := time.UnixMilli(1_800_000_011_001)
	payload := `{"timestampms":1800000000000,"events":[{"type":"change","side":"bid","price":"100","remaining":"1"},{"type":"change","side":"ask","price":"101","remaining":"2"}]}`

	snapshot, ok, err := newParser().Parse([]byte(payload), testBinding("BTC/USD", "btcusd"), 10, receivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected parser to emit a snapshot")
	}
	assertBest(t, snapshot, "100", "101")
	if !snapshot.ExchangeTime.IsZero() {
		t.Fatalf("expected stale advisory timestamp to be ignored, got %s", snapshot.ExchangeTime)
	}
	if snapshot.Latency != 0 {
		t.Fatalf("expected no exchange timestamp latency, got %s", snapshot.Latency)
	}
	if snapshot.Status != exchange.StatusLive {
		t.Fatalf("expected valid book to remain live, got %s", snapshot.Status)
	}
}

func TestWebSocketURLUsesMarketdataEndpoint(t *testing.T) {
	rawURL := New().websocketURL(testBinding("BTC/USD", "btcusd"))
	if !strings.HasPrefix(rawURL, "wss://api.gemini.com/v1/marketdata/btcusd?") {
		t.Fatalf("unexpected websocket URL: %s", rawURL)
	}
	for _, want := range []string{"top_of_book=false", "bids=true", "offers=true"} {
		if !strings.Contains(rawURL, want) {
			t.Fatalf("expected %q in websocket URL: %s", want, rawURL)
		}
	}
}

func TestParseREST(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	payload := `{"bids":[{"price":"100","amount":"1","timestamp":"1800000000"}],"asks":[{"price":"101","amount":"2","timestamp":"1800000000"}]}`

	snapshot, err := parseREST([]byte(payload), testBinding("BTC/USD", "btcusd"), 10, now, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	assertBest(t, snapshot, "100", "101")
	if snapshot.Transport != exchange.TransportPolling {
		t.Fatalf("expected polling transport, got %s", snapshot.Transport)
	}
}

func assertBest(t *testing.T, snapshot exchange.OrderBookSnapshot, bidText string, askText string) {
	t.Helper()
	bid, ok := snapshot.BestBid()
	if !ok {
		t.Fatal("missing best bid")
	}
	ask, ok := snapshot.BestAsk()
	if !ok {
		t.Fatal("missing best ask")
	}
	if bid.PriceText != bidText || ask.PriceText != askText {
		t.Fatalf("expected bid/ask %s/%s, got %s/%s", bidText, askText, bid.PriceText, ask.PriceText)
	}
}

func testBinding(market string, symbol string) exchange.Binding {
	parsed, err := exchange.NewMarket(market)
	if err != nil {
		panic(err)
	}
	return exchange.Binding{
		Exchange:        exchange.Gemini,
		Market:          parsed,
		WebSocketSymbol: symbol,
		RESTSymbol:      symbol,
		Enabled:         true,
	}
}
