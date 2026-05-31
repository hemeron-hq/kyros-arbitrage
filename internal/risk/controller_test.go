package risk

import (
	"context"
	"testing"

	"github.com/govalues/decimal"
)

type memoryModeStore struct {
	mode Mode
}

func (s *memoryModeStore) LoadMode(context.Context) (Mode, error) {
	if s.mode == "" {
		return ModeBalanced, nil
	}
	return s.mode, nil
}

func (s *memoryModeStore) SaveMode(_ context.Context, mode Mode) error {
	s.mode = mode
	return nil
}

func TestControllerEvaluateModes(t *testing.T) {
	controller, err := NewController(context.Background(), &memoryModeStore{mode: ModeBalanced})
	if err != nil {
		t.Fatal(err)
	}

	allowed := controller.Evaluate(Candidate{
		GrossBPS:          decimal.MustNew(120, 0),
		LatencyPenaltyBPS: decimal.MustNew(8, 0),
		SessionPNL:        decimal.Zero,
		BuyQuoteBalance:   decimal.MustNew(1000, 0),
		BuyQuoteAfter:     decimal.MustNew(100, 0),
		SellBaseBalance:   decimal.MustNew(1, 0),
		SellBaseAfter:     decimal.MustNew(1, 1),
	})
	if !allowed.Allowed {
		t.Fatalf("expected balanced mode to allow candidate, got %s", allowed.Reason)
	}

	if err := controller.SetMode(context.Background(), ModeConservative); err != nil {
		t.Fatal(err)
	}
	blocked := controller.Evaluate(Candidate{
		GrossBPS:          decimal.MustNew(120, 0),
		LatencyPenaltyBPS: decimal.MustNew(8, 0),
		SessionPNL:        decimal.Zero,
		BuyQuoteBalance:   decimal.MustNew(1000, 0),
		BuyQuoteAfter:     decimal.MustNew(100, 0),
		SellBaseBalance:   decimal.MustNew(1, 0),
		SellBaseAfter:     decimal.MustNew(1, 1),
	})
	if blocked.Allowed || blocked.Reason != ReasonSpreadOutlier {
		t.Fatalf("expected conservative spread block, got allowed=%v reason=%s", blocked.Allowed, blocked.Reason)
	}

	if err := controller.SetMode(context.Background(), ModeAggressive); err != nil {
		t.Fatal(err)
	}
	drawdown := controller.Evaluate(Candidate{
		GrossBPS:          decimal.MustNew(50, 0),
		LatencyPenaltyBPS: decimal.Zero,
		SessionPNL:        decimal.MustNew(-251, 0),
		BuyQuoteBalance:   decimal.MustNew(1000, 0),
		BuyQuoteAfter:     decimal.MustNew(900, 0),
		SellBaseBalance:   decimal.MustNew(1, 0),
		SellBaseAfter:     decimal.MustNew(9, 1),
	})
	if drawdown.Allowed || drawdown.Status != StatusHalted || drawdown.Reason != ReasonDrawdown {
		t.Fatalf("expected drawdown halt, got allowed=%v status=%s reason=%s", drawdown.Allowed, drawdown.Status, drawdown.Reason)
	}
}
