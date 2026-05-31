package arbitrage

import (
	"context"
	"testing"
	"time"

	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
	"github.com/hemeron-hq/kyros-arbitrage/internal/portfolio/paper"
	"github.com/hemeron-hq/kyros-arbitrage/internal/risk"
	"github.com/hemeron-hq/kyros-arbitrage/internal/terms"
)

func TestEvaluateSubtractsRebalanceCost(t *testing.T) {
	now := time.Now()
	engine := NewEngine(terms.NewStore(now), paper.NewLedger(), nil)
	snapshots := profitableSnapshots(now, "100", "150")

	engine.Evaluate(snapshots, now)
	result := engine.Evaluate(snapshots, now.Add(time.Millisecond))
	if len(result.Opportunities) == 0 {
		t.Fatal("expected opportunities")
	}
	opportunity := result.Opportunities[0]
	if opportunity.Decision != DecisionExecute {
		t.Fatalf("expected executable route, got %s/%s", opportunity.Decision, opportunity.ReasonCode)
	}
	if !opportunity.RebalanceCost.Equal(decimal.MustNew(801, 2)) {
		t.Fatalf("expected 8.01 rebalance cost, got %s", opportunity.RebalanceCost)
	}
	if !opportunity.ExpectedNetProfit.Equal(decimal.MustNew(41290, 3)) {
		t.Fatalf("expected net profit after rebalance, got %s", opportunity.ExpectedNetProfit)
	}
}

func TestEvaluateSkipsAuthenticatedTermsWithoutTransferFees(t *testing.T) {
	now := time.Now()
	termsStore := terms.NewStore(now)
	termsStore.Apply(authenticatedTerms(exchange.Binance, now))
	termsStore.Apply(authenticatedTerms(exchange.Kraken, now))
	engine := NewEngine(termsStore, paper.NewLedger(), nil)
	snapshots := profitableSnapshots(now, "100", "150")

	engine.Evaluate(snapshots, now)
	result := engine.Evaluate(snapshots, now.Add(time.Millisecond))
	if len(result.Opportunities) == 0 {
		t.Fatal("expected opportunities")
	}
	if result.Opportunities[0].ReasonCode != ReasonTransferFeeMissing {
		t.Fatalf("expected missing transfer fee skip, got %s", result.Opportunities[0].ReasonCode)
	}
}

func TestEvaluateKeepsUSDAndUSDTMarketsSeparate(t *testing.T) {
	now := time.Now()
	engine := NewEngine(terms.NewStore(now), paper.NewLedger(), nil)
	snapshots := []exchange.OrderBookSnapshot{
		testBook(exchange.Binance, exchange.Market{Base: "BTC", Quote: "USDT"}, now, "100", "101", 1),
		testBook(exchange.Kraken, exchange.Market{Base: "BTC", Quote: "USDT"}, now, "103", "104", 1),
		testBook(exchange.Coinbase, exchange.Market{Base: "BTC", Quote: "USD"}, now, "100", "101", 1),
		testBook(exchange.Gemini, exchange.Market{Base: "BTC", Quote: "USD"}, now, "103", "104", 1),
	}

	engine.Evaluate(snapshots, now)
	result := engine.Evaluate(snapshots, now.Add(time.Millisecond))
	for _, opportunity := range result.Opportunities {
		if opportunity.BuyExchange == "" || opportunity.SellExchange == "" {
			continue
		}
		if opportunity.Market.Quote == "USDT" {
			if opportunity.BuyExchange == exchange.Coinbase || opportunity.SellExchange == exchange.Coinbase ||
				opportunity.BuyExchange == exchange.Gemini || opportunity.SellExchange == exchange.Gemini {
				t.Fatalf("USDT route crossed into USD exchange: %+v", opportunity)
			}
		}
		if opportunity.Market.Quote == "USD" {
			if opportunity.BuyExchange == exchange.Binance || opportunity.SellExchange == exchange.Binance ||
				opportunity.BuyExchange == exchange.Kraken || opportunity.SellExchange == exchange.Kraken {
				t.Fatalf("USD route crossed into USDT exchange: %+v", opportunity)
			}
		}
	}
}

func TestEvaluateSkipsWhenRiskCircuitOpen(t *testing.T) {
	now := time.Now()
	controller, err := risk.NewController(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	controller.Halt(risk.ReasonDrawdown, "session drawdown exceeded")
	engine := NewEngine(terms.NewStore(now), paper.NewLedger(), controller)
	snapshots := profitableSnapshots(now, "100", "150")

	engine.Evaluate(snapshots, now)
	result := engine.Evaluate(snapshots, now.Add(time.Millisecond))
	if len(result.Opportunities) == 0 {
		t.Fatal("expected opportunities")
	}
	if result.Opportunities[0].Decision != DecisionSkip || result.Opportunities[0].ReasonCode != risk.ReasonCircuitOpen {
		t.Fatalf("expected circuit open skip, got %s/%s", result.Opportunities[0].Decision, result.Opportunities[0].ReasonCode)
	}
}

func profitableSnapshots(now time.Time, ask string, bid string) []exchange.OrderBookSnapshot {
	market := exchange.Market{Base: "BTC", Quote: "USDT"}
	return []exchange.OrderBookSnapshot{
		testBook(exchange.Binance, market, now, "99", ask, 1),
		testBook(exchange.Kraken, market, now, bid, "151", 1),
	}
}

func testBook(exchangeID exchange.ID, market exchange.Market, now time.Time, bid string, ask string, sequence int64) exchange.OrderBookSnapshot {
	askLevel, err := exchange.NewPriceLevel(ask, "1")
	if err != nil {
		panic(err)
	}
	bidLevel, err := exchange.NewPriceLevel(bid, "1")
	if err != nil {
		panic(err)
	}
	return exchange.OrderBookSnapshot{
		Exchange:   exchangeID,
		Market:     market,
		Bids:       []exchange.PriceLevel{bidLevel},
		Asks:       []exchange.PriceLevel{askLevel},
		ReceivedAt: now,
		Sequence:   sequence,
		Status:     exchange.StatusLive,
		Transport:  exchange.TransportWebSocket,
	}
}

func authenticatedTerms(exchangeID exchange.ID, now time.Time) terms.Snapshot {
	return terms.Snapshot{
		Exchange: exchangeID,
		Market:   exchange.Market{Base: "BTC", Quote: "USDT"},
		Source:   terms.SourceAuthenticated,
		Fees: exchange.FeeSchedule{
			MakerRate: decimal.MustNew(1, 3),
			TakerRate: decimal.MustNew(1, 3),
		},
		Constraints: terms.FallbackConstraints(),
		Balances: map[string]decimal.Decimal{
			"BTC":  decimal.MustNew(1, 0),
			"USDT": decimal.MustNew(1000, 0),
		},
		TransferFees: nil,
		UpdatedAt:    now,
		ExpiresAt:    now.Add(time.Hour),
	}
}
