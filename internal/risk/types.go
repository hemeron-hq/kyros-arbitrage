package risk

import (
	"errors"
	"strings"
	"time"

	"github.com/govalues/decimal"
)

type Mode string

const (
	ModeConservative Mode = "conservative"
	ModeBalanced     Mode = "balanced"
	ModeAggressive   Mode = "aggressive"
)

type Status string

const (
	StatusNormal  Status = "normal"
	StatusWarning Status = "warning"
	StatusHalted  Status = "halted"
)

const (
	ReasonDrawdown       = "SKIP_RISK_DRAWDOWN"
	ReasonSpreadOutlier  = "SKIP_RISK_SPREAD_OUTLIER"
	ReasonLatencyTooHigh = "SKIP_RISK_LATENCY"
	ReasonReserve        = "SKIP_RISK_RESERVE"
	ReasonCircuitOpen    = "SKIP_RISK_CIRCUIT_OPEN"
)

var ErrInvalidMode = errors.New("invalid risk mode")

type Thresholds struct {
	MaxSpreadBPS         decimal.Decimal `json:"maxSpreadBps"`
	MaxLatencyPenaltyBPS decimal.Decimal `json:"maxLatencyPenaltyBps"`
	MaxDrawdown          decimal.Decimal `json:"maxDrawdown"`
	ReserveRate          decimal.Decimal `json:"reserveRate"`
}

type State struct {
	Mode          Mode       `json:"mode"`
	Status        Status     `json:"status"`
	Reasons       []string   `json:"reasons"`
	Thresholds    Thresholds `json:"thresholds"`
	CircuitOpen   bool       `json:"circuitOpen"`
	CircuitReason string     `json:"circuitReason"`
	HaltedAt      time.Time  `json:"haltedAt"`
}

type Candidate struct {
	GrossBPS          decimal.Decimal
	LatencyPenaltyBPS decimal.Decimal
	SessionPNL        decimal.Decimal
	BuyQuoteBalance   decimal.Decimal
	BuyQuoteAfter     decimal.Decimal
	SellBaseBalance   decimal.Decimal
	SellBaseAfter     decimal.Decimal
}

type Decision struct {
	Allowed bool
	Status  Status
	Reason  string
	Reasons []string
}

func ParseMode(value string) (Mode, error) {
	mode := Mode(strings.ToLower(strings.TrimSpace(value)))
	switch mode {
	case ModeConservative, ModeBalanced, ModeAggressive:
		return mode, nil
	default:
		return "", ErrInvalidMode
	}
}

func ThresholdsFor(mode Mode) Thresholds {
	switch mode {
	case ModeConservative:
		return Thresholds{
			MaxSpreadBPS:         decimal.MustNew(75, 0),
			MaxLatencyPenaltyBPS: decimal.MustNew(6, 0),
			MaxDrawdown:          decimal.MustNew(50, 0),
			ReserveRate:          decimal.MustNew(5, 2),
		}
	case ModeAggressive:
		return Thresholds{
			MaxSpreadBPS:         decimal.MustNew(250, 0),
			MaxLatencyPenaltyBPS: decimal.MustNew(20, 0),
			MaxDrawdown:          decimal.MustNew(250, 0),
			ReserveRate:          decimal.MustNew(1, 2),
		}
	default:
		return Thresholds{
			MaxSpreadBPS:         decimal.MustNew(150, 0),
			MaxLatencyPenaltyBPS: decimal.MustNew(12, 0),
			MaxDrawdown:          decimal.MustNew(100, 0),
			ReserveRate:          decimal.MustNew(3, 2),
		}
	}
}
