package terms

import (
	"time"

	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

type Source string

const (
	SourceAuthenticated Source = "authenticated"
	SourceFallback      Source = "fallback"
	SourceMixed         Source = "mixed"
)

type Snapshot struct {
	Exchange     exchange.ID
	Market       exchange.Market
	Source       Source
	Fees         exchange.FeeSchedule
	Constraints  exchange.MarketConstraints
	Balances     map[string]decimal.Decimal
	TransferFees exchange.TransferFees
	UpdatedAt    time.Time
	ExpiresAt    time.Time
	Message      string
}

func (s Snapshot) Key() exchange.Key {
	return exchange.Key{Exchange: s.Exchange, MarketID: s.Market.ID()}
}

func (s Snapshot) IsFresh(now time.Time) bool {
	return !s.UpdatedAt.IsZero() && now.Before(s.ExpiresAt)
}

func (s Snapshot) Clone() Snapshot {
	clone := s
	clone.Balances = cloneDecimalMap(s.Balances)
	clone.TransferFees = exchange.TransferFees(cloneDecimalMap(s.TransferFees))
	return clone
}

func (s Snapshot) Balance(asset string) decimal.Decimal {
	if s.Balances == nil {
		return decimal.Zero
	}
	return s.Balances[asset]
}

func (s Snapshot) TransferFee(asset string) decimal.Decimal {
	if s.TransferFees == nil {
		return decimal.Zero
	}
	return s.TransferFees[asset]
}

type Health struct {
	Exchange  exchange.ID
	Market    exchange.Market
	Source    Source
	Fresh     bool
	UpdatedAt time.Time
	ExpiresAt time.Time
	Message   string
}

func CombinedSource(left Source, right Source) Source {
	if left == SourceAuthenticated && right == SourceAuthenticated {
		return SourceAuthenticated
	}
	if left == SourceFallback && right == SourceFallback {
		return SourceFallback
	}
	return SourceMixed
}

func cloneDecimalMap(values map[string]decimal.Decimal) map[string]decimal.Decimal {
	if values == nil {
		return nil
	}
	clone := make(map[string]decimal.Decimal, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}
