package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
	"github.com/hemeron-hq/kyros-arbitrage/internal/history"
	"github.com/hemeron-hq/kyros-arbitrage/internal/market"
	"github.com/hemeron-hq/kyros-arbitrage/internal/platform/config"
)

func TestDecisionLoopRecordsOnMarketEvent(t *testing.T) {
	t.Chdir("../..")
	store := market.NewStore(time.Second)
	databaseURL := "file:" + t.TempDir() + "/app.db"
	httpServer, err := New(config.Config{
		Environment: config.EnvironmentDevelopment,
		Port:        8090,
		DatabaseURL: databaseURL,
	}, WithMarketStore(store), WithMarketServiceDisabled())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = httpServer.Shutdown(context.Background())
	}()

	initial := historyCount(t, httpServer.Handler)
	now := time.Now()
	store.Apply(serverTestSnapshot(exchange.Binance, now, "100", "101", 1))
	store.Apply(serverTestSnapshot(exchange.Kraken, now, "101.1", "102", 1))

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if historyCount(t, httpServer.Handler) > initial {
			metrics := metricsSnapshot(t, httpServer.Handler)
			if metrics.LastDecisionLatencyMS < 0 {
				t.Fatalf("expected non-negative decision latency, got %d", metrics.LastDecisionLatencyMS)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected market event to record history without waiting for old 500ms ticker")
}

func metricsSnapshot(t *testing.T, handler http.Handler) struct {
	LastDecisionLatencyMS int64 `json:"lastDecisionLatencyMs"`
} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/metrics", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status %d: %s", rec.Code, rec.Body.String())
	}
	var snapshot struct {
		LastDecisionLatencyMS int64 `json:"lastDecisionLatencyMs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func historyCount(t *testing.T, handler http.Handler) int64 {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("history status %d: %s", rec.Code, rec.Body.String())
	}
	var report history.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	return report.Summary.Opportunities
}

func serverTestSnapshot(exchangeID exchange.ID, now time.Time, bid string, ask string, sequence int64) exchange.OrderBookSnapshot {
	bidLevel, err := exchange.NewPriceLevel(bid, "1")
	if err != nil {
		panic(err)
	}
	askLevel, err := exchange.NewPriceLevel(ask, "1")
	if err != nil {
		panic(err)
	}
	return exchange.OrderBookSnapshot{
		Exchange:   exchangeID,
		Market:     exchange.Market{Base: "BTC", Quote: "USDT"},
		Bids:       []exchange.PriceLevel{bidLevel},
		Asks:       []exchange.PriceLevel{askLevel},
		ReceivedAt: now,
		Latency:    time.Millisecond,
		Sequence:   sequence,
		Transport:  exchange.TransportWebSocket,
		Status:     exchange.StatusLive,
	}
}
