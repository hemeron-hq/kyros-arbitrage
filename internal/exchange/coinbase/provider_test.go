package coinbase

import (
	"bytes"
	"testing"
	"time"

	json "github.com/goccy/go-json"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

func TestParserEmitsSnapshot(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	binding := testBinding("BTC/USD", "BTC-USD")
	payload := `{"channel":"l2_data","timestamp":"2026-05-31T12:00:00.100Z","events":[{"type":"snapshot","product_id":"BTC-USD","updates":[{"side":"bid","price_level":"100","new_quantity":"1","event_time":"2026-05-31T12:00:00Z"},{"side":"offer","price_level":"101","new_quantity":"2","event_time":"2026-05-31T12:00:00Z"}]}]}`

	snapshot, ok, err := newParser().Parse([]byte(payload), binding, 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected parser to emit a snapshot")
	}
	assertBest(t, snapshot, "100", "101")
	if snapshot.Exchange != exchange.Coinbase {
		t.Fatalf("expected coinbase snapshot, got %s", snapshot.Exchange)
	}
	if snapshot.ExchangeTime.IsZero() {
		t.Fatal("expected exchange timestamp")
	}
}

func TestSubscribePayload(t *testing.T) {
	payload, err := New().subscribePayload(testBinding("BTC/USD", "BTC-USD"))
	if err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatal(err)
	}
	if message["type"] != "subscribe" || message["channel"] != "level2" {
		t.Fatalf("unexpected subscription payload: %s", payload)
	}
	if !bytes.Contains(payload, []byte(`"BTC-USD"`)) {
		t.Fatalf("expected product id in payload: %s", payload)
	}
}

func TestParseREST(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	payload := `{"sequence":123,"bids":[["100","1",3]],"asks":[["101","2",1]]}`

	snapshot, err := parseREST([]byte(payload), testBinding("BTC/USD", "BTC-USD"), 10, now, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	assertBest(t, snapshot, "100", "101")
	if snapshot.Transport != exchange.TransportPolling {
		t.Fatalf("expected polling transport, got %s", snapshot.Transport)
	}
	if snapshot.Sequence != 123 {
		t.Fatalf("expected sequence 123, got %d", snapshot.Sequence)
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
		Exchange:        exchange.Coinbase,
		Market:          parsed,
		WebSocketSymbol: symbol,
		RESTSymbol:      symbol,
		Enabled:         true,
	}
}
