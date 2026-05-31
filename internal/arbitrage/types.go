package arbitrage

import (
	"time"

	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
	"github.com/hemeron-hq/kyros-arbitrage/internal/portfolio"
	"github.com/hemeron-hq/kyros-arbitrage/internal/terms"
)

type Decision string

const (
	DecisionExecute Decision = "execute"
	DecisionSkip    Decision = "skip"
	DecisionWait    Decision = "wait"
)

const (
	ReasonExecuted            = "EXECUTED"
	ReasonWaitingLatencyModel = "WAITING_LATENCY_MODEL"
	ReasonWaitingNewBook      = "WAITING_NEW_BOOK"
	ReasonNoGrossEdge         = "SKIP_NO_GROSS_EDGE"
	ReasonTermsStale          = "SKIP_TERMS_STALE"
	ReasonBelowMarketMinimum  = "SKIP_BELOW_MARKET_MIN"
	ReasonInsufficientBalance = "SKIP_INSUFFICIENT_BALANCE"
	ReasonNegativeNet         = "SKIP_NEGATIVE_NET"
	ReasonLedgerApplyFailed   = "SKIP_LEDGER_APPLY_FAILED"
	ReasonNoLiveRoute         = "WAITING_FOR_LIVE_BOOKS"
)

type Opportunity struct {
	ID                string
	Rank              int
	Market            exchange.Market
	BuyExchange       exchange.ID
	SellExchange      exchange.ID
	BaseSize          decimal.Decimal
	BuyNotional       decimal.Decimal
	SellNotional      decimal.Decimal
	GrossProfit       decimal.Decimal
	GrossBPS          decimal.Decimal
	BuyFee            decimal.Decimal
	SellFee           decimal.Decimal
	TradingFees       decimal.Decimal
	TradingFeeBPS     decimal.Decimal
	SlippageCost      decimal.Decimal
	SlippageBPS       decimal.Decimal
	LatencyPenalty    decimal.Decimal
	LatencyPenaltyBPS decimal.Decimal
	RebalanceCost     decimal.Decimal
	ExpectedNetProfit decimal.Decimal
	ExpectedNetBPS    decimal.Decimal
	Decision          Decision
	ReasonCode        string
	TermsSource       terms.Source
	Partial           bool
	CreatedAt         time.Time
}

type Snapshot struct {
	Opportunities []Opportunity
	Balances      []portfolio.BalanceRow
	TermsHealth   []terms.Health
	SessionPNL    decimal.Decimal
	Executed      int
	Rejected      int
	LastUpdated   time.Time
}
