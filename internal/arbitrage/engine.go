package arbitrage

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
	"github.com/hemeron-hq/kyros-arbitrage/internal/portfolio"
	"github.com/hemeron-hq/kyros-arbitrage/internal/terms"
)

type Engine struct {
	mu          sync.Mutex
	termsStore  *terms.Store
	ledger      portfolio.Store
	latency     *LatencyModel
	executedIDs map[string]struct{}
}

func NewEngine(termsStore *terms.Store, ledger portfolio.Store) *Engine {
	return &Engine{
		termsStore:  termsStore,
		ledger:      ledger,
		latency:     NewLatencyModel(5 * time.Minute),
		executedIDs: make(map[string]struct{}),
	}
}

func (e *Engine) Evaluate(snapshots []exchange.OrderBookSnapshot, now time.Time) Snapshot {
	return e.evaluate(snapshots, now, true)
}

func (e *Engine) Project(snapshots []exchange.OrderBookSnapshot, now time.Time) Snapshot {
	return e.evaluate(snapshots, now, false)
}

func (e *Engine) evaluate(snapshots []exchange.OrderBookSnapshot, now time.Time, execute bool) Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.latency.Observe(snapshots, now)
	e.seedActualBalances(now)

	opportunities := e.findOpportunities(snapshots, now)
	sort.Slice(opportunities, func(i, j int) bool {
		left := opportunities[i]
		right := opportunities[j]
		if left.Decision != right.Decision {
			return decisionRank(left.Decision) < decisionRank(right.Decision)
		}
		if left.ReasonCode == ReasonNoGrossEdge && right.ReasonCode != ReasonNoGrossEdge {
			return false
		}
		if right.ReasonCode == ReasonNoGrossEdge && left.ReasonCode != ReasonNoGrossEdge {
			return true
		}
		if !left.ExpectedNetProfit.Equal(right.ExpectedNetProfit) {
			return left.ExpectedNetProfit.Cmp(right.ExpectedNetProfit) > 0
		}
		return left.GrossBPS.Cmp(right.GrossBPS) > 0
	})

	rejected := 0
	for i := range opportunities {
		opportunities[i].Rank = i + 1
		if opportunities[i].Decision == DecisionSkip {
			rejected++
		}
	}
	if execute && len(opportunities) > 0 && opportunities[0].Decision == DecisionExecute {
		e.executeBest(&opportunities[0], now)
	}

	stats := e.ledger.Stats()
	return Snapshot{
		Opportunities: opportunities,
		Balances:      e.ledger.Balances(),
		TermsHealth:   e.termsStore.Health(now),
		SessionPNL:    stats.SessionPNL,
		Executed:      stats.Executed,
		Rejected:      rejected,
		LastUpdated:   now,
	}
}

func (e *Engine) seedActualBalances(now time.Time) {
	for _, snapshot := range e.termsStore.All() {
		if snapshot.IsFresh(now) {
			e.ledger.SeedAuthenticatedOnce(snapshot)
		}
	}
}

func (e *Engine) findOpportunities(snapshots []exchange.OrderBookSnapshot, now time.Time) []Opportunity {
	byMarket := make(map[string][]exchange.OrderBookSnapshot)
	for _, snapshot := range snapshots {
		if snapshot.Status != exchange.StatusLive || !snapshot.HasBook() {
			continue
		}
		byMarket[snapshot.Market.ID()] = append(byMarket[snapshot.Market.ID()], snapshot)
	}

	opportunities := make([]Opportunity, 0)
	for _, marketSnapshots := range byMarket {
		for _, buy := range marketSnapshots {
			for _, sell := range marketSnapshots {
				if buy.Exchange == sell.Exchange {
					continue
				}
				opportunity := e.evaluatePair(buy, sell, now)
				opportunities = append(opportunities, opportunity)
			}
		}
	}
	if len(opportunities) == 0 {
		opportunities = append(opportunities, Opportunity{
			ID:         "waiting",
			Market:     exchange.Market{Base: "BTC", Quote: "USDT"},
			Decision:   DecisionWait,
			ReasonCode: ReasonNoLiveRoute,
			CreatedAt:  now,
		})
	}
	return opportunities
}

func (e *Engine) evaluatePair(buy exchange.OrderBookSnapshot, sell exchange.OrderBookSnapshot, now time.Time) Opportunity {
	market := buy.Market
	opportunity := Opportunity{
		ID:           opportunityID(buy, sell),
		Market:       market,
		BuyExchange:  buy.Exchange,
		SellExchange: sell.Exchange,
		Decision:     DecisionSkip,
		ReasonCode:   ReasonNoGrossEdge,
		CreatedAt:    now,
	}

	buyTerms, buyOK := e.termsStore.Snapshot(buy.Key())
	sellTerms, sellOK := e.termsStore.Snapshot(sell.Key())
	if !buyOK || !sellOK || !buyTerms.IsFresh(now) || !sellTerms.IsFresh(now) {
		opportunity.Decision = DecisionSkip
		opportunity.ReasonCode = ReasonTermsStale
		return opportunity
	}
	opportunity.TermsSource = terms.CombinedSource(buyTerms.Source, sellTerms.Source)

	topAsk, askOK := buy.BestAsk()
	topBid, bidOK := sell.BestBid()
	if !askOK || !bidOK {
		return opportunity
	}

	maxBaseByBalance, balanceOK := e.maxBaseByBalance(market, topAsk.Price, buy.Exchange, sell.Exchange, buyTerms.Fees.TakerRate)
	if !balanceOK || !maxBaseByBalance.IsPos() {
		opportunity.ReasonCode = ReasonInsufficientBalance
		return opportunity
	}
	if topBid.Price.Cmp(topAsk.Price) <= 0 {
		e.previewNoGrossEdge(&opportunity, topAsk, topBid, buyTerms, sellTerms, maxBaseByBalance)
		return opportunity
	}

	fullDepth := WalkExecutableDepth(buy.Asks, sell.Bids, decimal.Zero)
	if !fullDepth.OK || !fullDepth.Base.IsPos() {
		return opportunity
	}
	depth := fullDepth
	if maxBaseByBalance.Cmp(fullDepth.Base) < 0 {
		depth = WalkExecutableDepth(buy.Asks, sell.Bids, maxBaseByBalance)
		opportunity.Partial = true
	}
	if !depth.OK || !depth.Base.IsPos() {
		return opportunity
	}
	opportunity.BaseSize = depth.Base
	opportunity.BuyNotional = depth.BuyNotional
	opportunity.SellNotional = depth.SellNotional

	if !passesMarketConstraints(depth, buyTerms.Constraints, sellTerms.Constraints) {
		opportunity.ReasonCode = ReasonBelowMarketMinimum
		return opportunity
	}

	grossProfit, err := depth.SellNotional.Sub(depth.BuyNotional)
	if err != nil {
		return opportunity
	}
	opportunity.GrossProfit = grossProfit
	opportunity.GrossBPS = bps(grossProfit, depth.BuyNotional)

	buyFee := mul(depth.BuyNotional, buyTerms.Fees.TakerRate)
	sellFee := mul(depth.SellNotional, sellTerms.Fees.TakerRate)
	opportunity.BuyFee = buyFee
	opportunity.SellFee = sellFee
	tradingFees, err := buyFee.Add(sellFee)
	if err != nil {
		return opportunity
	}
	opportunity.TradingFees = tradingFees
	opportunity.TradingFeeBPS = bps(tradingFees, depth.BuyNotional)

	topGross := mul(depth.Base, mustSub(topBid.Price, topAsk.Price))
	slippageCost, err := topGross.Sub(grossProfit)
	if err == nil && slippageCost.IsPos() {
		opportunity.SlippageCost = slippageCost
		opportunity.SlippageBPS = bps(slippageCost, depth.BuyNotional)
	}

	latencyBPS, latencyOK := e.latency.PenaltyBPS(buy.Key(), sell.Key())
	if !latencyOK {
		opportunity.Decision = DecisionWait
		opportunity.ReasonCode = ReasonWaitingLatencyModel
		return opportunity
	}
	opportunity.LatencyPenaltyBPS = latencyBPS
	opportunity.LatencyPenalty = mul(depth.BuyNotional, rateFromBPS(latencyBPS))

	net := grossProfit
	net, err = net.Sub(tradingFees)
	if err != nil {
		return opportunity
	}
	net, err = net.Sub(opportunity.LatencyPenalty)
	if err != nil {
		return opportunity
	}
	net, err = net.Sub(opportunity.RebalanceCost)
	if err != nil {
		return opportunity
	}
	opportunity.ExpectedNetProfit = net
	opportunity.ExpectedNetBPS = bps(net, depth.BuyNotional)
	if !net.IsPos() {
		opportunity.Decision = DecisionSkip
		opportunity.ReasonCode = ReasonNegativeNet
		return opportunity
	}

	opportunity.Decision = DecisionExecute
	opportunity.ReasonCode = ReasonExecuted
	if _, seen := e.executedIDs[opportunity.ID]; seen {
		opportunity.Decision = DecisionWait
		opportunity.ReasonCode = ReasonWaitingNewBook
	}
	return opportunity
}

func (e *Engine) previewNoGrossEdge(opportunity *Opportunity, topAsk exchange.PriceLevel, topBid exchange.PriceLevel, buyTerms terms.Snapshot, sellTerms terms.Snapshot, maxBaseByBalance decimal.Decimal) {
	size := topAsk.Quantity.Min(topBid.Quantity)
	if maxBaseByBalance.IsPos() {
		size = size.Min(maxBaseByBalance)
	}
	if !size.IsPos() {
		return
	}

	opportunity.BaseSize = size
	opportunity.BuyNotional = mul(size, topAsk.Price)
	opportunity.SellNotional = mul(size, topBid.Price)
	grossProfit, err := opportunity.SellNotional.Sub(opportunity.BuyNotional)
	if err != nil {
		return
	}
	opportunity.GrossProfit = grossProfit
	opportunity.GrossBPS = bps(grossProfit, opportunity.BuyNotional)
	opportunity.BuyFee = mul(opportunity.BuyNotional, buyTerms.Fees.TakerRate)
	opportunity.SellFee = mul(opportunity.SellNotional, sellTerms.Fees.TakerRate)
	tradingFees, err := opportunity.BuyFee.Add(opportunity.SellFee)
	if err != nil {
		return
	}
	opportunity.TradingFees = tradingFees
	opportunity.TradingFeeBPS = bps(tradingFees, opportunity.BuyNotional)
	net, err := grossProfit.Sub(tradingFees)
	if err != nil {
		return
	}
	opportunity.ExpectedNetProfit = net
	opportunity.ExpectedNetBPS = bps(net, opportunity.BuyNotional)
}

func (e *Engine) maxBaseByBalance(market exchange.Market, topAsk decimal.Decimal, buyExchange exchange.ID, sellExchange exchange.ID, buyFeeRate decimal.Decimal) (decimal.Decimal, bool) {
	quoteBalance := e.ledger.Balance(buyExchange, market.Quote)
	baseBalance := e.ledger.Balance(sellExchange, market.Base)
	if !quoteBalance.IsPos() || !baseBalance.IsPos() || !topAsk.IsPos() {
		return decimal.Zero, false
	}
	onePlusFee, err := decimal.MustNew(1, 0).Add(buyFeeRate)
	if err != nil {
		return decimal.Zero, false
	}
	effectiveAsk, err := topAsk.Mul(onePlusFee)
	if err != nil || !effectiveAsk.IsPos() {
		return decimal.Zero, false
	}
	buyBase, err := quoteBalance.Quo(effectiveAsk)
	if err != nil {
		return decimal.Zero, false
	}
	return buyBase.Min(baseBalance), true
}

func (e *Engine) executeBest(opportunity *Opportunity, now time.Time) {
	if _, seen := e.executedIDs[opportunity.ID]; seen {
		opportunity.Decision = DecisionWait
		opportunity.ReasonCode = ReasonWaitingNewBook
		return
	}

	execution := portfolio.Execution{
		ID:             opportunity.ID,
		Market:         opportunity.Market,
		BuyExchange:    opportunity.BuyExchange,
		SellExchange:   opportunity.SellExchange,
		BaseSize:       opportunity.BaseSize,
		BuyNotional:    opportunity.BuyNotional,
		SellNotional:   opportunity.SellNotional,
		BuyFee:         opportunity.BuyFee,
		SellFee:        opportunity.SellFee,
		LatencyPenalty: opportunity.LatencyPenalty,
		RebalanceCost:  opportunity.RebalanceCost,
		NetProfit:      opportunity.ExpectedNetProfit,
		TermsSource:    opportunity.TermsSource,
		ExecutedAt:     now,
	}
	if !e.ledger.Apply(execution) {
		opportunity.Decision = DecisionSkip
		opportunity.ReasonCode = ReasonLedgerApplyFailed
		return
	}
	e.executedIDs[opportunity.ID] = struct{}{}
}

func passesMarketConstraints(depth DepthResult, buyConstraints exchange.MarketConstraints, sellConstraints exchange.MarketConstraints) bool {
	minBase := buyConstraints.MinBase.Max(sellConstraints.MinBase)
	minNotional := buyConstraints.MinNotional.Max(sellConstraints.MinNotional)
	return depth.Base.Cmp(minBase) >= 0 && depth.BuyNotional.Cmp(minNotional) >= 0
}

func opportunityID(buy exchange.OrderBookSnapshot, sell exchange.OrderBookSnapshot) string {
	return fmt.Sprintf(
		"%s:%s:%s:%d:%d:%d:%d",
		buy.Market.ID(),
		buy.Exchange,
		sell.Exchange,
		buy.Sequence,
		sell.Sequence,
		buy.ReceivedAt.UnixNano(),
		sell.ReceivedAt.UnixNano(),
	)
}

func decisionRank(decision Decision) int {
	switch decision {
	case DecisionExecute:
		return 0
	case DecisionWait:
		return 1
	default:
		return 2
	}
}

func bps(value decimal.Decimal, basis decimal.Decimal) decimal.Decimal {
	if !basis.IsPos() {
		return decimal.Zero
	}
	ratio, err := value.Quo(basis)
	if err != nil {
		return decimal.Zero
	}
	result, err := ratio.Mul(decimal.MustNew(10_000, 0))
	if err != nil {
		return decimal.Zero
	}
	return result
}

func rateFromBPS(value decimal.Decimal) decimal.Decimal {
	rate, err := value.Quo(decimal.MustNew(10_000, 0))
	if err != nil {
		return decimal.Zero
	}
	return rate
}

func mul(left decimal.Decimal, right decimal.Decimal) decimal.Decimal {
	value, err := left.Mul(right)
	if err != nil {
		return decimal.Zero
	}
	return value
}

func mustSub(left decimal.Decimal, right decimal.Decimal) decimal.Decimal {
	value, err := left.Sub(right)
	if err != nil {
		return decimal.Zero
	}
	return value
}
