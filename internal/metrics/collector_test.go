package metrics

import (
	"testing"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
	"github.com/hemeron-hq/kyros-arbitrage/internal/market"
)

func TestCollectorRecordsMarketEventDecisionLatency(t *testing.T) {
	collector := NewCollector()
	bookTime := time.Unix(1_800_000_000, 0)
	receivedAt := bookTime.Add(4 * time.Millisecond)
	finishedAt := receivedAt.Add(2 * time.Millisecond)

	collector.ObserveMarket(metricsTestSnapshot(exchange.Binance, receivedAt, bookTime), receivedAt)
	collector.ObserveDecision(receivedAt, receivedAt.Add(time.Millisecond), finishedAt)
	snapshot := collector.Snapshot(finishedAt, []market.FeedProjection{
		{Exchange: exchange.Binance, Market: "BTC/USDT", Status: exchange.StatusLive},
	})

	if snapshot.LastDecisionLatencyMS != 2 {
		t.Fatalf("expected 2ms decision latency, got %d", snapshot.LastDecisionLatencyMS)
	}
	if len(snapshot.Feeds) != 1 || snapshot.Feeds[0].FeedAgeP95MS != 4 {
		t.Fatalf("expected feed age sample, got %+v", snapshot.Feeds)
	}
	if len(snapshot.Markets) != 1 || snapshot.Markets[0].Live != 1 {
		t.Fatalf("expected market status counts, got %+v", snapshot.Markets)
	}
}

func metricsTestSnapshot(exchangeID exchange.ID, receivedAt time.Time, exchangeTime time.Time) exchange.OrderBookSnapshot {
	bid, err := exchange.NewPriceLevel("100", "1")
	if err != nil {
		panic(err)
	}
	ask, err := exchange.NewPriceLevel("101", "1")
	if err != nil {
		panic(err)
	}
	return exchange.OrderBookSnapshot{
		Exchange:     exchangeID,
		Market:       exchange.Market{Base: "BTC", Quote: "USDT"},
		Bids:         []exchange.PriceLevel{bid},
		Asks:         []exchange.PriceLevel{ask},
		ReceivedAt:   receivedAt,
		ExchangeTime: exchangeTime,
		Status:       exchange.StatusLive,
		Transport:    exchange.TransportWebSocket,
	}
}
