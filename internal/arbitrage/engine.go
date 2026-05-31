package arbitrage

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
	"github.com/hemeron-hq/kyros-arbitrage/internal/portfolio"
	"github.com/hemeron-hq/kyros-arbitrage/internal/risk"
	"github.com/hemeron-hq/kyros-arbitrage/internal/terms"
)

type Engine struct {
	mu                sync.Mutex
	termsStore        *terms.Store
	ledger            portfolio.Store
	latency           *LatencyModel
	riskController    *risk.Controller
	executedIDs       map[string]struct{}
	sessionBestNet    Opportunity
	hasSessionBestNet bool
}

func NewEngine(termsStore *terms.Store, ledger portfolio.Store, riskController *risk.Controller) *Engine {
	return &Engine{
		termsStore:     termsStore,
		ledger:         ledger,
		latency:        NewLatencyModel(5 * time.Minute),
		riskController: riskController,
		executedIDs:    make(map[string]struct{}),
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

	opportunities := e.findOpportunities(snapshots, now, execute)
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
	e.observeSessionBestNet(opportunities)

	stats := e.ledger.Stats()
	return Snapshot{
		Opportunities:     opportunities,
		Balances:          e.ledger.Balances(),
		TermsHealth:       e.termsStore.Health(now),
		SessionPNL:        stats.SessionPNL,
		SessionBestNet:    e.sessionBestNet,
		HasSessionBestNet: e.hasSessionBestNet,
		Executed:          stats.Executed,
		Rejected:          rejected,
		LastUpdated:       now,
	}
}

func (e *Engine) observeSessionBestNet(opportunities []Opportunity) {
	for _, opportunity := range opportunities {
		if !isNetComparable(opportunity) {
			continue
		}
		if !e.hasSessionBestNet || opportunity.ExpectedNetProfit.Cmp(e.sessionBestNet.ExpectedNetProfit) > 0 {
			e.sessionBestNet = opportunity
			e.hasSessionBestNet = true
		}
	}
}

func isNetComparable(opportunity Opportunity) bool {
	return opportunity.BuyExchange != "" &&
		opportunity.SellExchange != "" &&
		opportunity.BaseSize.IsPos() &&
		opportunity.BuyNotional.IsPos()
}

func (e *Engine) seedActualBalances(now time.Time) {
	for _, snapshot := range e.termsStore.All() {
		if snapshot.IsFresh(now) {
			e.ledger.SeedAuthenticatedOnce(snapshot)
		}
	}
}

func (e *Engine) findOpportunities(snapshots []exchange.OrderBookSnapshot, now time.Time, execute bool) []Opportunity {
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
				opportunity := e.evaluatePair(buy, sell, now, execute)
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

func (e *Engine) evaluatePair(buy exchange.OrderBookSnapshot, sell exchange.OrderBookSnapshot, now time.Time, execute bool) Opportunity {
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

	maxBaseAtTop, balanceOK := e.maxBaseByBalance(market, topAsk.Price, buy.Exchange, sell.Exchange, buyTerms.Fees.TakerRate)
	if !balanceOK || !maxBaseAtTop.IsPos() {
		opportunity.ReasonCode = ReasonInsufficientBalance
		return opportunity
	}
	if topBid.Price.Cmp(topAsk.Price) <= 0 {
		e.previewNoGrossEdge(&opportunity, topAsk, topBid, buyTerms, sellTerms, maxBaseAtTop)
		return opportunity
	}

	latencyBPS, latencyOK := e.latency.PenaltyBPS(buy.Key(), sell.Key())
	if !latencyOK {
		opportunity.Decision = DecisionWait
		opportunity.ReasonCode = ReasonWaitingLatencyModel
		return opportunity
	}

	quoteBalance := e.ledger.Balance(buy.Exchange, market.Quote)
	baseBalance := e.ledger.Balance(sell.Exchange, market.Base)
	selection := selectBestNetDepth(buy.Asks, sell.Bids, quoteBalance, baseBalance, buyTerms, sellTerms, latencyBPS)
	if !selection.HasDepth {
		opportunity.ReasonCode = ReasonInsufficientBalance
		return opportunity
	}
	if !selection.MeetsMinimum {
		opportunity.ReasonCode = ReasonBelowMarketMinimum
		return opportunity
	}
	opportunity.BaseSize = selection.Depth.Base
	opportunity.BuyNotional = selection.Depth.BuyNotional
	opportunity.SellNotional = selection.Depth.SellNotional
	opportunity.GrossProfit = selection.GrossProfit
	opportunity.GrossBPS = selection.GrossBPS
	opportunity.BuyFee = selection.BuyFee
	opportunity.SellFee = selection.SellFee
	opportunity.TradingFees = selection.TradingFees
	opportunity.TradingFeeBPS = selection.TradingFeeBPS
	opportunity.LatencyPenaltyBPS = latencyBPS
	opportunity.LatencyPenalty = selection.LatencyPenalty
	opportunity.ExpectedNetProfit = selection.ExpectedNetProfit
	opportunity.ExpectedNetBPS = selection.ExpectedNetBPS
	opportunity.Partial = selection.Partial
	rebalanceCost, rebalanceOK := rebalanceCost(market, selection.Depth, buyTerms, sellTerms)
	if !rebalanceOK {
		opportunity.Decision = DecisionSkip
		opportunity.ReasonCode = ReasonTransferFeeMissing
		return opportunity
	}
	opportunity.RebalanceCost = rebalanceCost
	if rebalanceCost.IsPos() {
		net, err := opportunity.ExpectedNetProfit.Sub(rebalanceCost)
		if err != nil {
			opportunity.Decision = DecisionSkip
			opportunity.ReasonCode = ReasonNegativeNet
			return opportunity
		}
		opportunity.ExpectedNetProfit = net
		opportunity.ExpectedNetBPS = bps(net, opportunity.BuyNotional)
	}

	topGross := mul(selection.Depth.Base, mustSub(topBid.Price, topAsk.Price))
	slippageCost, err := topGross.Sub(selection.GrossProfit)
	if err == nil && slippageCost.IsPos() {
		opportunity.SlippageCost = slippageCost
		opportunity.SlippageBPS = bps(slippageCost, selection.Depth.BuyNotional)
	}

	if !opportunity.ExpectedNetProfit.IsPos() {
		opportunity.Decision = DecisionSkip
		opportunity.ReasonCode = ReasonNegativeNet
		return opportunity
	}
	riskDecision := e.evaluateRisk(opportunity, quoteBalance, baseBalance, execute)
	if !riskDecision.Allowed {
		opportunity.Decision = DecisionSkip
		opportunity.ReasonCode = riskDecision.Reason
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

type netDepthSelection struct {
	Depth             DepthResult
	GrossProfit       decimal.Decimal
	GrossBPS          decimal.Decimal
	BuyFee            decimal.Decimal
	SellFee           decimal.Decimal
	TradingFees       decimal.Decimal
	TradingFeeBPS     decimal.Decimal
	LatencyPenalty    decimal.Decimal
	ExpectedNetProfit decimal.Decimal
	ExpectedNetBPS    decimal.Decimal
	HasDepth          bool
	MeetsMinimum      bool
	Partial           bool
}

func selectBestNetDepth(asks []exchange.PriceLevel, bids []exchange.PriceLevel, quoteBalance decimal.Decimal, baseBalance decimal.Decimal, buyTerms terms.Snapshot, sellTerms terms.Snapshot, latencyBPS decimal.Decimal) netDepthSelection {
	var current DepthResult
	var best netDepthSelection
	askIndex := 0
	bidIndex := 0
	askRemaining := decimal.Zero
	bidRemaining := decimal.Zero

	for askIndex < len(asks) && bidIndex < len(bids) {
		ask := asks[askIndex]
		bid := bids[bidIndex]
		if bid.Price.Cmp(ask.Price) <= 0 {
			break
		}

		if askRemaining.IsZero() {
			askRemaining = ask.Quantity
		}
		if bidRemaining.IsZero() {
			bidRemaining = bid.Quantity
		}

		liquiditySize := askRemaining.Min(bidRemaining)
		size := liquiditySize
		balanceLimited := false

		remainingBase, ok := remainingBalance(baseBalance, current.Base)
		if !ok {
			break
		}
		if remainingBase.Cmp(size) < 0 {
			size = remainingBase
			balanceLimited = true
		}

		remainingQuoteSize, ok := remainingQuoteBaseSize(quoteBalance, current.BuyNotional, ask.Price, buyTerms.Fees.TakerRate)
		if !ok {
			break
		}
		if remainingQuoteSize.Cmp(size) < 0 {
			size = remainingQuoteSize
			balanceLimited = true
		}
		if !size.IsPos() {
			break
		}

		var err error
		current.Base, err = current.Base.Add(size)
		if err != nil {
			return netDepthSelection{}
		}
		current.BuyNotional, err = addMul(current.BuyNotional, size, ask.Price)
		if err != nil {
			return netDepthSelection{}
		}
		current.SellNotional, err = addMul(current.SellNotional, size, bid.Price)
		if err != nil {
			return netDepthSelection{}
		}
		current.OK = true

		candidate, ok := buildNetDepthSelection(current, buyTerms.Fees.TakerRate, sellTerms.Fees.TakerRate, latencyBPS)
		if !ok {
			return netDepthSelection{}
		}
		candidate.HasDepth = true
		candidate.Partial = balanceLimited
		if passesMarketConstraints(current, buyTerms.Constraints, sellTerms.Constraints) {
			candidate.MeetsMinimum = true
			if !best.MeetsMinimum || candidate.ExpectedNetProfit.Cmp(best.ExpectedNetProfit) > 0 {
				best = candidate
			}
		} else if !best.HasDepth {
			best = candidate
		}

		askRemaining, err = askRemaining.Sub(size)
		if err != nil {
			return netDepthSelection{}
		}
		bidRemaining, err = bidRemaining.Sub(size)
		if err != nil {
			return netDepthSelection{}
		}
		if askRemaining.IsZero() {
			askIndex++
		}
		if bidRemaining.IsZero() {
			bidIndex++
		}
		if balanceLimited {
			break
		}
	}

	return best
}

func buildNetDepthSelection(depth DepthResult, buyFeeRate decimal.Decimal, sellFeeRate decimal.Decimal, latencyBPS decimal.Decimal) (netDepthSelection, bool) {
	grossProfit, err := depth.SellNotional.Sub(depth.BuyNotional)
	if err != nil {
		return netDepthSelection{}, false
	}
	buyFee := mul(depth.BuyNotional, buyFeeRate)
	sellFee := mul(depth.SellNotional, sellFeeRate)
	tradingFees, err := buyFee.Add(sellFee)
	if err != nil {
		return netDepthSelection{}, false
	}
	latencyPenalty := mul(depth.BuyNotional, rateFromBPS(latencyBPS))
	net, err := grossProfit.Sub(tradingFees)
	if err != nil {
		return netDepthSelection{}, false
	}
	net, err = net.Sub(latencyPenalty)
	if err != nil {
		return netDepthSelection{}, false
	}
	return netDepthSelection{
		Depth:             depth,
		GrossProfit:       grossProfit,
		GrossBPS:          bps(grossProfit, depth.BuyNotional),
		BuyFee:            buyFee,
		SellFee:           sellFee,
		TradingFees:       tradingFees,
		TradingFeeBPS:     bps(tradingFees, depth.BuyNotional),
		LatencyPenalty:    latencyPenalty,
		ExpectedNetProfit: net,
		ExpectedNetBPS:    bps(net, depth.BuyNotional),
	}, true
}

func remainingBalance(balance decimal.Decimal, used decimal.Decimal) (decimal.Decimal, bool) {
	if !balance.IsPos() {
		return decimal.Zero, false
	}
	remaining, err := balance.Sub(used)
	if err != nil || !remaining.IsPos() {
		return decimal.Zero, false
	}
	return remaining, true
}

func remainingQuoteBaseSize(quoteBalance decimal.Decimal, buyNotional decimal.Decimal, askPrice decimal.Decimal, buyFeeRate decimal.Decimal) (decimal.Decimal, bool) {
	remainingQuote, ok := remainingQuoteBalance(quoteBalance, buyNotional, buyFeeRate)
	if !ok || !askPrice.IsPos() {
		return decimal.Zero, false
	}
	onePlusFee, err := decimal.MustNew(1, 0).Add(buyFeeRate)
	if err != nil {
		return decimal.Zero, false
	}
	effectiveAsk, err := askPrice.Mul(onePlusFee)
	if err != nil || !effectiveAsk.IsPos() {
		return decimal.Zero, false
	}
	baseSize, err := remainingQuote.Quo(effectiveAsk)
	if err != nil {
		return decimal.Zero, false
	}
	return baseSize, baseSize.IsPos()
}

func remainingQuoteBalance(quoteBalance decimal.Decimal, buyNotional decimal.Decimal, buyFeeRate decimal.Decimal) (decimal.Decimal, bool) {
	if !quoteBalance.IsPos() {
		return decimal.Zero, false
	}
	onePlusFee, err := decimal.MustNew(1, 0).Add(buyFeeRate)
	if err != nil {
		return decimal.Zero, false
	}
	usedQuote, err := buyNotional.Mul(onePlusFee)
	if err != nil {
		return decimal.Zero, false
	}
	remaining, err := quoteBalance.Sub(usedQuote)
	if err != nil || !remaining.IsPos() {
		return decimal.Zero, false
	}
	return remaining, true
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

func rebalanceCost(market exchange.Market, depth DepthResult, buyTerms terms.Snapshot, sellTerms terms.Snapshot) (decimal.Decimal, bool) {
	if !depth.Base.IsPos() || !depth.BuyNotional.IsPos() {
		return decimal.Zero, false
	}
	baseFee, baseOK := transferFeeFor(buyTerms, market.Base)
	quoteFee, quoteOK := transferFeeFor(sellTerms, market.Quote)
	if !baseOK || !quoteOK {
		return decimal.Zero, false
	}
	avgBuyPrice, err := depth.BuyNotional.Quo(depth.Base)
	if err != nil {
		return decimal.Zero, false
	}
	baseCost, err := baseFee.Mul(avgBuyPrice)
	if err != nil {
		return decimal.Zero, false
	}
	total, err := baseCost.Add(quoteFee)
	if err != nil {
		return decimal.Zero, false
	}
	return total, true
}

func transferFeeFor(snapshot terms.Snapshot, asset string) (decimal.Decimal, bool) {
	if snapshot.TransferFees == nil {
		return decimal.Zero, false
	}
	value, ok := snapshot.TransferFees[asset]
	if !ok {
		return decimal.Zero, false
	}
	if snapshot.Source != terms.SourceFallback && value.IsZero() {
		return decimal.Zero, false
	}
	return value, true
}

func (e *Engine) evaluateRisk(opportunity Opportunity, quoteBalance decimal.Decimal, baseBalance decimal.Decimal, execute bool) risk.Decision {
	if e.riskController == nil {
		return risk.Decision{Allowed: true}
	}
	buyCost, err := opportunity.BuyNotional.Add(opportunity.BuyFee)
	if err != nil {
		return risk.Decision{Allowed: false, Reason: risk.ReasonReserve}
	}
	quoteAfter, err := quoteBalance.Sub(buyCost)
	if err != nil {
		return risk.Decision{Allowed: false, Reason: risk.ReasonReserve}
	}
	baseAfter, err := baseBalance.Sub(opportunity.BaseSize)
	if err != nil {
		return risk.Decision{Allowed: false, Reason: risk.ReasonReserve}
	}
	candidate := risk.Candidate{
		GrossBPS:          opportunity.GrossBPS,
		LatencyPenaltyBPS: opportunity.LatencyPenaltyBPS,
		SessionPNL:        e.ledger.Stats().SessionPNL,
		BuyQuoteBalance:   quoteBalance,
		BuyQuoteAfter:     quoteAfter,
		SellBaseBalance:   baseBalance,
		SellBaseAfter:     baseAfter,
	}
	if execute {
		return e.riskController.Evaluate(candidate)
	}
	return e.riskController.Preview(candidate)
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
		if e.riskController != nil {
			e.riskController.Halt(ReasonLedgerApplyFailed, "paper ledger rejected simulated execution")
		}
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
