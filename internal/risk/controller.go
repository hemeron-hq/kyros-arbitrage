package risk

import (
	"context"
	"sync"

	"github.com/govalues/decimal"
)

type ModeStore interface {
	LoadMode(context.Context) (Mode, error)
	SaveMode(context.Context, Mode) error
}

type Controller struct {
	mu      sync.RWMutex
	store   ModeStore
	mode    Mode
	status  Status
	reasons []string
}

func NewController(ctx context.Context, store ModeStore) (*Controller, error) {
	mode := ModeBalanced
	if store != nil {
		stored, err := store.LoadMode(ctx)
		if err != nil {
			return nil, err
		}
		mode = stored
	}
	return &Controller{
		store:   store,
		mode:    mode,
		status:  StatusNormal,
		reasons: []string{"within risk thresholds"},
	}, nil
}

func (c *Controller) SetMode(ctx context.Context, mode Mode) error {
	if _, err := ParseMode(string(mode)); err != nil {
		return err
	}
	if c.store != nil {
		if err := c.store.SaveMode(ctx, mode); err != nil {
			return err
		}
	}

	c.mu.Lock()
	c.mode = mode
	c.status = StatusNormal
	c.reasons = []string{"risk mode updated"}
	c.mu.Unlock()
	return nil
}

func (c *Controller) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return State{
		Mode:       c.mode,
		Status:     c.status,
		Reasons:    append([]string(nil), c.reasons...),
		Thresholds: ThresholdsFor(c.mode),
	}
}

func (c *Controller) Evaluate(candidate Candidate) Decision {
	if c == nil {
		return Decision{Allowed: true, Status: StatusNormal}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	thresholds := ThresholdsFor(c.mode)
	status := StatusNormal
	reason := ""
	reasons := make([]string, 0, 4)

	if candidate.SessionPNL.IsNeg() && candidate.SessionPNL.Abs().Cmp(thresholds.MaxDrawdown) > 0 {
		status = StatusHalted
		reason = ReasonDrawdown
		reasons = append(reasons, "session drawdown exceeded")
	} else {
		if candidate.GrossBPS.Cmp(thresholds.MaxSpreadBPS) > 0 {
			status = StatusWarning
			reason = ReasonSpreadOutlier
			reasons = append(reasons, "gross spread outlier")
		}
		if reason == "" && candidate.LatencyPenaltyBPS.Cmp(thresholds.MaxLatencyPenaltyBPS) > 0 {
			status = StatusWarning
			reason = ReasonLatencyTooHigh
			reasons = append(reasons, "latency penalty exceeded")
		}
		if reason == "" && violatesReserve(candidate.BuyQuoteBalance, candidate.BuyQuoteAfter, thresholds.ReserveRate) {
			status = StatusWarning
			reason = ReasonReserve
			reasons = append(reasons, "buy wallet reserve breached")
		}
		if reason == "" && violatesReserve(candidate.SellBaseBalance, candidate.SellBaseAfter, thresholds.ReserveRate) {
			status = StatusWarning
			reason = ReasonReserve
			reasons = append(reasons, "sell wallet reserve breached")
		}
	}

	if reason == "" {
		reasons = append(reasons, "within risk thresholds")
	}
	c.status = status
	c.reasons = append([]string(nil), reasons...)
	return Decision{
		Allowed: reason == "",
		Status:  status,
		Reason:  reason,
		Reasons: reasons,
	}
}

func violatesReserve(start decimal.Decimal, after decimal.Decimal, reserveRate decimal.Decimal) bool {
	if !start.IsPos() {
		return true
	}
	minReserve, err := start.Mul(reserveRate)
	if err != nil {
		return true
	}
	return after.Cmp(minReserve) < 0
}
