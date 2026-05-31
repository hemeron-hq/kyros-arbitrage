package exchange

import (
	"context"
	"time"

	"github.com/govalues/decimal"
)

type FeeSchedule struct {
	MakerRate decimal.Decimal
	TakerRate decimal.Decimal
}

type MarketConstraints struct {
	MinBase     decimal.Decimal
	MinNotional decimal.Decimal
	StepSize    decimal.Decimal
	TickSize    decimal.Decimal
}

type AccountSnapshot struct {
	Exchange  ID
	Balances  map[string]decimal.Decimal
	UpdatedAt time.Time
	Message   string
}

type TransferFees map[string]decimal.Decimal

type TermsClient interface {
	Provider
	FetchFeeSchedule(ctx context.Context, binding Binding, now time.Time) (FeeSchedule, error)
	FetchMarketConstraints(ctx context.Context, binding Binding) (MarketConstraints, error)
	FetchAccount(ctx context.Context, now time.Time) (AccountSnapshot, error)
	FetchTransferFees(ctx context.Context, assets []string, now time.Time) (TransferFees, error)
	TermsUnavailableMessage() string
}
