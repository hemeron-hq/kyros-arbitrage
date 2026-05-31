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

const (
	makerVisibleSizeRate  = 25
	makerConfirmBufferBPS = 2
	makerConfirmTTL       = 3 * time.Second
)

var directRouteStyles = []routeStyle{
	{BuyLiquidity: LiquidityTaker, SellLiquidity: LiquidityTaker},
	{BuyLiquidity: LiquidityMaker, SellLiquidity: LiquidityTaker},
	{BuyLiquidity: LiquidityTaker, SellLiquidity: LiquidityMaker},
}

type routeStyle struct {
	BuyLiquidity  LiquidityRole
	SellLiquidity LiquidityRole
}

type pendingMakerCandidate struct {
	firstSeen time.Time
}

type Engine struct {
	mu                sync.Mutex
	termsStore        *terms.Store
	ledger            portfolio.Store
	latency           *LatencyModel
	riskController    *risk.Controller
	executedIDs       map[string]struct{}
	pendingMaker      map[string]pendingMakerCandidate
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
		pendingMaker:   make(map[string]pendingMakerCandidate),
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
	e.expirePendingMaker(now)

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
		if e.needsMakerConfirmation(opportunities[0]) && !e.confirmMakerCandidate(opportunities[0], now) {
			opportunities[0].Decision = DecisionWait
			opportunities[0].ReasonCode = ReasonWaitingMakerConfirm
		} else {
			e.executeBest(&opportunities[0], now)
		}
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
				opportunities = append(opportunities, e.evaluatePair(buy, sell, now, execute)...)
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

func (e *Engine) evaluatePair(buy exchange.OrderBookSnapshot, sell exchange.OrderBookSnapshot, now time.Time, execute bool) []Opportunity {
	opportunities := make([]Opportunity, 0, len(directRouteStyles))
	for _, style := range directRouteStyles {
		opportunities = append(opportunities, e.evaluatePairStyle(buy, sell, now, execute, style))
	}
	return opportunities
}

func (e *Engine) evaluatePairStyle(buy exchange.OrderBookSnapshot, sell exchange.OrderBookSnapshot, now time.Time, execute bool, style routeStyle) Opportunity {
	market := buy.Market
	opportunity := Opportunity{
		Market:        market,
		BuyExchange:   buy.Exchange,
		SellExchange:  sell.Exchange,
		BuyLiquidity:  style.BuyLiquidity,
		SellLiquidity: style.SellLiquidity,
		Decision:      DecisionSkip,
		ReasonCode:    ReasonNoGrossEdge,
		CreatedAt:     now,
	}
	opportunity.ID = opportunityID(buy, sell, style)

	buyTerms, buyOK := e.termsStore.Snapshot(buy.Key())
	sellTerms, sellOK := e.termsStore.Snapshot(sell.Key())
	if !buyOK || !sellOK || !buyTerms.IsFresh(now) || !sellTerms.IsFresh(now) {
		opportunity.Decision = DecisionSkip
		opportunity.ReasonCode = ReasonTermsStale
		return opportunity
	}
	opportunity.TermsSource = terms.CombinedSource(buyTerms.Source, sellTerms.Source)

	buyLevels, sellLevels := levelsForStyle(buy, sell, style)
	topBuy, buyLevelOK := firstLevel(buyLevels)
	topSell, sellLevelOK := firstLevel(sellLevels)
	if !buyLevelOK || !sellLevelOK {
		return opportunity
	}

	buyFeeRate := feeRate(buyTerms.Fees, style.BuyLiquidity)
	sellFeeRate := feeRate(sellTerms.Fees, style.SellLiquidity)
	maxBaseAtTop, balanceOK := e.maxBaseByBalance(market, topBuy.Price, buy.Exchange, sell.Exchange, buyFeeRate)
	if !balanceOK || !maxBaseAtTop.IsPos() {
		opportunity.ReasonCode = ReasonInsufficientBalance
		return opportunity
	}
	if topSell.Price.Cmp(topBuy.Price) <= 0 {
		e.previewNoGrossEdge(&opportunity, topBuy, topSell, buyFeeRate, sellFeeRate, maxBaseAtTop)
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
	selection := selectBestNetDepth(
		buyLevels,
		sellLevels,
		quoteBalance,
		baseBalance,
		buyTerms,
		sellTerms,
		buyFeeRate,
		sellFeeRate,
		latencyBPS,
		routeBaseCap(buyLevels, sellLevels, style),
	)
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
	opportunity.RebalanceCost = decimal.Zero
	opportunity.RebalanceExposure, _ = rebalanceExposure(market, selection.Depth, buyTerms, sellTerms)

	topGross := mul(selection.Depth.Base, mustSub(topSell.Price, topBuy.Price))
	slippageCost, err := topGross.Sub(selection.GrossProfit)
	if err == nil && slippageCost.IsPos() {
		opportunity.SlippageCost = slippageCost
		opportunity.SlippageBPS = bps(slippageCost, selection.Depth.BuyNotional)
	}
	applyDiagnostics(&opportunity)

	if !opportunity.ExpectedNetProfit.IsPos() {
		opportunity.Decision = DecisionSkip
		opportunity.ReasonCode = ReasonNegativeNet
		return opportunity
	}
	if e.needsMakerConfirmation(opportunity) && opportunity.ExpectedNetBPS.Cmp(decimal.MustNew(makerConfirmBufferBPS, 0)) < 0 {
		opportunity.Decision = DecisionSkip
		opportunity.ReasonCode = ReasonBelowMakerBuffer
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

func selectBestNetDepth(
	buyLevels []exchange.PriceLevel,
	sellLevels []exchange.PriceLevel,
	quoteBalance decimal.Decimal,
	baseBalance decimal.Decimal,
	buyTerms terms.Snapshot,
	sellTerms terms.Snapshot,
	buyFeeRate decimal.Decimal,
	sellFeeRate decimal.Decimal,
	latencyBPS decimal.Decimal,
	baseCap decimal.Decimal,
) netDepthSelection {
	var current DepthResult
	var best netDepthSelection
	buyIndex := 0
	sellIndex := 0
	buyRemaining := decimal.Zero
	sellRemaining := decimal.Zero

	for buyIndex < len(buyLevels) && sellIndex < len(sellLevels) {
		buyLevel := buyLevels[buyIndex]
		sellLevel := sellLevels[sellIndex]
		if sellLevel.Price.Cmp(buyLevel.Price) <= 0 {
			break
		}

		if buyRemaining.IsZero() {
			buyRemaining = buyLevel.Quantity
		}
		if sellRemaining.IsZero() {
			sellRemaining = sellLevel.Quantity
		}

		liquiditySize := buyRemaining.Min(sellRemaining)
		size := liquiditySize
		balanceLimited := false

		if baseCap.IsPos() {
			remainingCap, ok := remainingBalance(baseCap, current.Base)
			if !ok {
				break
			}
			if remainingCap.Cmp(size) < 0 {
				size = remainingCap
				balanceLimited = true
			}
		}

		remainingBase, ok := remainingBalance(baseBalance, current.Base)
		if !ok {
			break
		}
		if remainingBase.Cmp(size) < 0 {
			size = remainingBase
			balanceLimited = true
		}

		remainingQuoteSize, ok := remainingQuoteBaseSize(quoteBalance, current.BuyNotional, buyLevel.Price, buyFeeRate)
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
		current.BuyNotional, err = addMul(current.BuyNotional, size, buyLevel.Price)
		if err != nil {
			return netDepthSelection{}
		}
		current.SellNotional, err = addMul(current.SellNotional, size, sellLevel.Price)
		if err != nil {
			return netDepthSelection{}
		}
		current.OK = true

		candidate, ok := buildNetDepthSelection(current, buyFeeRate, sellFeeRate, latencyBPS)
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

		buyRemaining, err = buyRemaining.Sub(size)
		if err != nil {
			return netDepthSelection{}
		}
		sellRemaining, err = sellRemaining.Sub(size)
		if err != nil {
			return netDepthSelection{}
		}
		if buyRemaining.IsZero() {
			buyIndex++
		}
		if sellRemaining.IsZero() {
			sellIndex++
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

func (e *Engine) previewNoGrossEdge(opportunity *Opportunity, topBuy exchange.PriceLevel, topSell exchange.PriceLevel, buyFeeRate decimal.Decimal, sellFeeRate decimal.Decimal, maxBaseByBalance decimal.Decimal) {
	size := topBuy.Quantity.Min(topSell.Quantity)
	if maxBaseByBalance.IsPos() {
		size = size.Min(maxBaseByBalance)
	}
	if !size.IsPos() {
		return
	}

	opportunity.BaseSize = size
	opportunity.BuyNotional = mul(size, topBuy.Price)
	opportunity.SellNotional = mul(size, topSell.Price)
	grossProfit, err := opportunity.SellNotional.Sub(opportunity.BuyNotional)
	if err != nil {
		return
	}
	opportunity.GrossProfit = grossProfit
	opportunity.GrossBPS = bps(grossProfit, opportunity.BuyNotional)
	opportunity.BuyFee = mul(opportunity.BuyNotional, buyFeeRate)
	opportunity.SellFee = mul(opportunity.SellNotional, sellFeeRate)
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
	applyDiagnostics(opportunity)
}

func rebalanceExposure(market exchange.Market, depth DepthResult, buyTerms terms.Snapshot, sellTerms terms.Snapshot) (decimal.Decimal, bool) {
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

func levelsForStyle(buy exchange.OrderBookSnapshot, sell exchange.OrderBookSnapshot, style routeStyle) ([]exchange.PriceLevel, []exchange.PriceLevel) {
	buyLevels := buy.Asks
	if style.BuyLiquidity == LiquidityMaker {
		buyLevels = buy.Bids
	}
	sellLevels := sell.Bids
	if style.SellLiquidity == LiquidityMaker {
		sellLevels = sell.Asks
	}
	return buyLevels, sellLevels
}

func firstLevel(levels []exchange.PriceLevel) (exchange.PriceLevel, bool) {
	if len(levels) == 0 {
		return exchange.PriceLevel{}, false
	}
	return levels[0], true
}

func feeRate(schedule exchange.FeeSchedule, role LiquidityRole) decimal.Decimal {
	if role == LiquidityMaker {
		return schedule.MakerRate
	}
	return schedule.TakerRate
}

func routeBaseCap(buyLevels []exchange.PriceLevel, sellLevels []exchange.PriceLevel, style routeStyle) decimal.Decimal {
	cap := decimal.Zero
	if style.BuyLiquidity == LiquidityMaker {
		cap = makerBaseCap(buyLevels)
	}
	if style.SellLiquidity == LiquidityMaker {
		sellCap := makerBaseCap(sellLevels)
		if !cap.IsPos() || sellCap.Cmp(cap) < 0 {
			cap = sellCap
		}
	}
	return cap
}

func makerBaseCap(levels []exchange.PriceLevel) decimal.Decimal {
	top, ok := firstLevel(levels)
	if !ok || !top.Quantity.IsPos() {
		return decimal.Zero
	}
	cap, err := top.Quantity.Mul(decimal.MustNew(makerVisibleSizeRate, 0))
	if err != nil {
		return decimal.Zero
	}
	cap, err = cap.Quo(decimal.MustNew(100, 0))
	if err != nil {
		return decimal.Zero
	}
	return cap
}

func applyDiagnostics(opportunity *Opportunity) {
	latencyAndFees, err := opportunity.TradingFeeBPS.Add(opportunity.LatencyPenaltyBPS)
	if err != nil {
		latencyAndFees = opportunity.TradingFeeBPS
	}
	rebalanceBPS := bps(opportunity.RebalanceCost, opportunity.BuyNotional)
	feeHurdle, err := latencyAndFees.Add(rebalanceBPS)
	if err != nil {
		feeHurdle = latencyAndFees
	}
	opportunity.FeeHurdleBPS = feeHurdle
	edgeAfterFees, err := opportunity.GrossBPS.Sub(feeHurdle)
	if err != nil {
		edgeAfterFees = decimal.Zero
	}
	opportunity.EdgeAfterFeesBPS = edgeAfterFees
	required := decimal.Zero
	if opportunity.BuyLiquidity == LiquidityMaker || opportunity.SellLiquidity == LiquidityMaker {
		required = decimal.MustNew(makerConfirmBufferBPS, 0)
	}
	if edgeAfterFees.Cmp(required) < 0 {
		missing, err := required.Sub(edgeAfterFees)
		if err == nil {
			opportunity.MissingBPS = missing
		}
	}
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
		ID:                opportunity.ID,
		Market:            opportunity.Market,
		BuyExchange:       opportunity.BuyExchange,
		SellExchange:      opportunity.SellExchange,
		BuyLiquidity:      string(opportunity.BuyLiquidity),
		SellLiquidity:     string(opportunity.SellLiquidity),
		BaseSize:          opportunity.BaseSize,
		BuyNotional:       opportunity.BuyNotional,
		SellNotional:      opportunity.SellNotional,
		BuyFee:            opportunity.BuyFee,
		SellFee:           opportunity.SellFee,
		LatencyPenalty:    opportunity.LatencyPenalty,
		RebalanceCost:     opportunity.RebalanceCost,
		RebalanceExposure: opportunity.RebalanceExposure,
		NetProfit:         opportunity.ExpectedNetProfit,
		TermsSource:       opportunity.TermsSource,
		ExecutedAt:        now,
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
	delete(e.pendingMaker, makerConfirmationKey(*opportunity))
}

func passesMarketConstraints(depth DepthResult, buyConstraints exchange.MarketConstraints, sellConstraints exchange.MarketConstraints) bool {
	minBase := buyConstraints.MinBase.Max(sellConstraints.MinBase)
	minNotional := buyConstraints.MinNotional.Max(sellConstraints.MinNotional)
	return depth.Base.Cmp(minBase) >= 0 && depth.BuyNotional.Cmp(minNotional) >= 0
}

func (e *Engine) needsMakerConfirmation(opportunity Opportunity) bool {
	return opportunity.BuyLiquidity == LiquidityMaker || opportunity.SellLiquidity == LiquidityMaker
}

func (e *Engine) confirmMakerCandidate(opportunity Opportunity, now time.Time) bool {
	key := makerConfirmationKey(opportunity)
	pending, ok := e.pendingMaker[key]
	if ok && now.Sub(pending.firstSeen) <= makerConfirmTTL {
		delete(e.pendingMaker, key)
		return true
	}
	e.pendingMaker[key] = pendingMakerCandidate{firstSeen: now}
	return false
}

func (e *Engine) expirePendingMaker(now time.Time) {
	for key, pending := range e.pendingMaker {
		if now.Sub(pending.firstSeen) > makerConfirmTTL {
			delete(e.pendingMaker, key)
		}
	}
}

func makerConfirmationKey(opportunity Opportunity) string {
	return fmt.Sprintf(
		"%s:%s:%s:%s:%s",
		opportunity.Market.ID(),
		opportunity.BuyExchange,
		opportunity.SellExchange,
		opportunity.BuyLiquidity,
		opportunity.SellLiquidity,
	)
}

func opportunityID(buy exchange.OrderBookSnapshot, sell exchange.OrderBookSnapshot, style routeStyle) string {
	return fmt.Sprintf(
		"%s:%s:%s:%s:%s:%d:%d:%d:%d",
		buy.Market.ID(),
		buy.Exchange,
		sell.Exchange,
		style.BuyLiquidity,
		style.SellLiquidity,
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
