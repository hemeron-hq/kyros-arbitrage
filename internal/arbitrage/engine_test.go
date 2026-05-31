package arbitrage

import (
	"testing"
	"time"

	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
	"github.com/hemeron-hq/kyros-arbitrage/internal/portfolio/paper"
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
	if !opportunity.RebalanceCost.Equal(decimal.MustNew(251, 2)) {
		t.Fatalf("expected 2.51 rebalance cost, got %s", opportunity.RebalanceCost)
	}
	if !opportunity.ExpectedNetProfit.Equal(decimal.MustNew(9815, 3)) {
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

func profitableSnapshots(now time.Time, ask string, bid string) []exchange.OrderBookSnapshot {
	market := exchange.Market{Base: "BTC", Quote: "USDT"}
	askLevel, err := exchange.NewPriceLevel(ask, "1")
	if err != nil {
		panic(err)
	}
	bidLevel, err := exchange.NewPriceLevel(bid, "1")
	if err != nil {
		panic(err)
	}
	return []exchange.OrderBookSnapshot{
		{
			Exchange:   exchange.Binance,
			Market:     market,
			Bids:       []exchange.PriceLevel{{Price: decimal.MustNew(99, 0), Quantity: decimal.MustNew(1, 0)}},
			Asks:       []exchange.PriceLevel{askLevel},
			ReceivedAt: now,
			Sequence:   1,
			Status:     exchange.StatusLive,
			Transport:  exchange.TransportWebSocket,
		},
		{
			Exchange:   exchange.Kraken,
			Market:     market,
			Bids:       []exchange.PriceLevel{bidLevel},
			Asks:       []exchange.PriceLevel{{Price: decimal.MustNew(151, 0), Quantity: decimal.MustNew(1, 0)}},
			ReceivedAt: now,
			Sequence:   1,
			Status:     exchange.StatusLive,
			Transport:  exchange.TransportWebSocket,
		},
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
