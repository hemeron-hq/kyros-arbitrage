package bitfinex

import (
	"bytes"
	"testing"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

func TestParserEmitsSnapshot(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	snapshot, ok, err := newParser().Parse([]byte(`[1,[[100,1,1],[101,1,-2]]]`), testBinding("BTC/USD", "tBTCUSD"), 10, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected parser to emit a snapshot")
	}
	assertBest(t, snapshot, "100", "101")
	if snapshot.Exchange != exchange.Bitfinex {
		t.Fatalf("expected bitfinex snapshot, got %s", snapshot.Exchange)
	}
}

func TestBookLengthMapsDepthToSupportedLength(t *testing.T) {
	tests := map[int]int{
		1:   1,
		10:  25,
		25:  25,
		26:  100,
		100: 100,
	}
	for depth, want := range tests {
		if got := bookLength(depth); got != want {
			t.Fatalf("bookLength(%d) = %d, want %d", depth, got, want)
		}
	}
}

func TestSubscribePayloadUsesSupportedLength(t *testing.T) {
	payload, err := New().subscribePayload(testBinding("BTC/USD", "tBTCUSD"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(payload, []byte(`"len":"25"`)) {
		t.Fatalf("expected supported length in payload: %s", payload)
	}
}

func TestParseREST(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	payload := `[[100,1,1],[101,1,-2]]`

	snapshot, err := parseREST([]byte(payload), testBinding("BTC/USD", "tBTCUSD"), 10, now, time.Second)
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
		Exchange:        exchange.Bitfinex,
		Market:          parsed,
		WebSocketSymbol: symbol,
		RESTSymbol:      symbol,
		Enabled:         true,
	}
}
