package portfolio

import (
	"time"

	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
	"github.com/hemeron-hq/kyros-arbitrage/internal/terms"
)

type BalanceRow struct {
	Exchange exchange.ID
	Asset    string
	Amount   decimal.Decimal
	Source   terms.Source
}

type Execution struct {
	ID                string
	Market            exchange.Market
	BuyExchange       exchange.ID
	SellExchange      exchange.ID
	BuyLiquidity      string
	SellLiquidity     string
	BaseSize          decimal.Decimal
	BuyNotional       decimal.Decimal
	SellNotional      decimal.Decimal
	BuyFee            decimal.Decimal
	SellFee           decimal.Decimal
	LatencyPenalty    decimal.Decimal
	RebalanceCost     decimal.Decimal
	RebalanceExposure decimal.Decimal
	NetProfit         decimal.Decimal
	TermsSource       terms.Source
	ExecutedAt        time.Time
}

type Stats struct {
	SessionPNL decimal.Decimal
	Executed   int
}

type Store interface {
	Seed(exchangeID exchange.ID, balances map[string]decimal.Decimal, source terms.Source)
	SeedAuthenticatedOnce(snapshot terms.Snapshot)
	Balance(exchangeID exchange.ID, asset string) decimal.Decimal
	Balances() []BalanceRow
	Apply(execution Execution) bool
	Stats() Stats
}
