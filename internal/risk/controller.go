package risk

import (
	"context"
	"sync"
	"time"

	"github.com/govalues/decimal"
)

type ModeStore interface {
	LoadMode(context.Context) (Mode, error)
	SaveMode(context.Context, Mode) error
}

type Controller struct {
	mu         sync.RWMutex
	store      ModeStore
	mode       Mode
	status     Status
	reasons    []string
	open       bool
	openReason string
	haltedAt   time.Time
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
	if c.open {
		c.status = StatusHalted
		c.reasons = []string{"risk mode updated while circuit remains open", "manual reset required"}
	} else {
		c.status = StatusNormal
		c.reasons = []string{"risk mode updated"}
	}
	c.mu.Unlock()
	return nil
}

func (c *Controller) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.open = false
	c.openReason = ""
	c.haltedAt = time.Time{}
	c.status = StatusNormal
	c.reasons = []string{"circuit reset manually"}
}

func (c *Controller) Halt(reason string, reasons ...string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.openCircuitLocked(reason, reasons)
}

func (c *Controller) State() State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return State{
		Mode:          c.mode,
		Status:        c.status,
		Reasons:       append([]string(nil), c.reasons...),
		Thresholds:    ThresholdsFor(c.mode),
		CircuitOpen:   c.open,
		CircuitReason: c.openReason,
		HaltedAt:      c.haltedAt,
	}
}

func (c *Controller) Evaluate(candidate Candidate) Decision {
	if c == nil {
		return Decision{Allowed: true, Status: StatusNormal}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.open {
		return c.openDecisionLocked()
	}

	decision := assessCandidate(candidate, ThresholdsFor(c.mode))
	if decision.Status == StatusHalted {
		c.openCircuitLocked(decision.Reason, decision.Reasons)
		return decision
	}
	c.status = decision.Status
	c.reasons = append([]string(nil), decision.Reasons...)
	return decision
}

func (c *Controller) Preview(candidate Candidate) Decision {
	if c == nil {
		return Decision{Allowed: true, Status: StatusNormal}
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.open {
		return c.openDecisionLocked()
	}
	return assessCandidate(candidate, ThresholdsFor(c.mode))
}

func assessCandidate(candidate Candidate, thresholds Thresholds) Decision {
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
	return Decision{
		Allowed: reason == "",
		Status:  status,
		Reason:  reason,
		Reasons: reasons,
	}
}

func (c *Controller) openCircuitLocked(reason string, reasons []string) {
	if reason == "" {
		reason = ReasonCircuitOpen
	}
	c.open = true
	c.openReason = reason
	c.haltedAt = time.Now()
	c.status = StatusHalted
	if len(reasons) == 0 {
		reasons = []string{"circuit breaker opened"}
	}
	c.reasons = append([]string(nil), reasons...)
	c.reasons = append(c.reasons, "manual reset required")
}

func (c *Controller) openDecisionLocked() Decision {
	reasons := append([]string(nil), c.reasons...)
	if len(reasons) == 0 {
		reasons = []string{"circuit breaker open"}
	}
	return Decision{
		Allowed: false,
		Status:  StatusHalted,
		Reason:  ReasonCircuitOpen,
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
