package publicfeed

import (
	"os"
	"testing"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

// okxRESTBooksFixture mirrors GET /api/v5/market/books for BTC-USDT (top 5 levels).
const okxRESTBooksFixture = `{
  "code": "0",
  "data": [{
    "asks": [
      ["73920.1","1.5","0","3"],
      ["73920.2","0.8","0","2"],
      ["73920.3","2.1","0","4"],
      ["73920.4","0.3","0","1"],
      ["73920.5","1.0","0","2"]
    ],
    "bids": [
      ["73920.0","2.0","0","3"],
      ["73919.9","1.2","0","2"],
      ["73919.8","0.9","0","1"],
      ["73919.7","1.5","0","2"],
      ["73919.6","0.5","0","1"]
    ],
    "ts": "1800000000000",
    "seqId": 999
  }]
}`

// okxBooks5WSFixture mirrors books5 WebSocket push (full snapshot each tick).
const okxBooks5WSFixture = `{
  "arg": {"channel": "books5", "instId": "BTC-USDT"},
  "data": [{
    "asks": [
      ["73920.1","1.5","0","3"],
      ["73920.2","0.8","0","2"],
      ["73920.3","2.1","0","4"],
      ["73920.4","0.3","0","1"],
      ["73920.5","1.0","0","2"]
    ],
    "bids": [
      ["73920.0","2.0","0","3"],
      ["73919.9","1.2","0","2"],
      ["73919.8","0.9","0","1"],
      ["73919.7","1.5","0","2"],
      ["73919.6","0.5","0","1"]
    ],
    "ts": "1800000000000",
    "seqId": 999
  }]
}`

func TestOKXBooks5WebSocketMatchesREST(t *testing.T) {
	binding := testBinding(exchange.OKX, "BTC/USDT", "BTC-USDT")
	now := time.Unix(1_800_000_000, 0)
	depth := 5

	wsSnapshot, ok, err := okxParser{}.Parse([]byte(okxBooks5WSFixture), binding, depth, now)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected books5 websocket snapshot")
	}

	restSnapshot, err := parseOKXREST([]byte(okxRESTBooksFixture), binding, depth, now, 0)
	if err != nil {
		t.Fatal(err)
	}

	wsBid, _ := wsSnapshot.BestBid()
	wsAsk, _ := wsSnapshot.BestAsk()
	restBid, _ := restSnapshot.BestBid()
	restAsk, _ := restSnapshot.BestAsk()

	if wsBid.PriceText != restBid.PriceText || wsAsk.PriceText != restAsk.PriceText {
		t.Fatalf("top of book mismatch: ws %s/%s rest %s/%s",
			wsBid.PriceText, wsAsk.PriceText, restBid.PriceText, restAsk.PriceText)
	}
	if wsBid.PriceText != "73920.0" || wsAsk.PriceText != "73920.1" {
		t.Fatalf("unexpected top of book: bid %s ask %s", wsBid.PriceText, wsAsk.PriceText)
	}
	if wsSnapshot.Message != "books5 live" {
		t.Fatalf("expected books5 live message, got %q", wsSnapshot.Message)
	}
	if restSnapshot.Message != "books live" {
		t.Fatalf("expected books live message for REST, got %q", restSnapshot.Message)
	}
}

func TestOKXLiveRESTTopOfBook(t *testing.T) {
	if os.Getenv("OKX_LIVE_TEST") == "" {
		t.Skip("set OKX_LIVE_TEST=1 to compare against live OKX REST")
	}
	provider := NewOKX()
	binding := testBinding(exchange.OKX, "BTC/USDT", "BTC-USDT")
	now := time.Now()
	//nolint:noctx // live integration probe
	snapshot, err := provider.Poll(t.Context(), binding, 5)
	if err != nil {
		t.Fatal(err)
	}
	bid, bidOK := snapshot.BestBid()
	ask, askOK := snapshot.BestAsk()
	if !bidOK || !askOK {
		t.Fatal("expected live REST book")
	}
	if bid.Price.Cmp(ask.Price) >= 0 {
		t.Fatalf("crossed live book at %s: bid %s ask %s", now, bid.PriceText, ask.PriceText)
	}
	t.Logf("OKX live REST top: bid %s ask %s", bid.PriceText, ask.PriceText)
}
