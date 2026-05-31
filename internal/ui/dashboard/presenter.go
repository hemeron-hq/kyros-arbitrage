package dashboard

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/arbitrage"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
	"github.com/hemeron-hq/kyros-arbitrage/internal/history"
	"github.com/hemeron-hq/kyros-arbitrage/internal/market"
	"github.com/hemeron-hq/kyros-arbitrage/internal/risk"
	"github.com/hemeron-hq/kyros-arbitrage/internal/terms"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/exchanges"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/historic"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/live"
	riskui "github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/risk"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/speed"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/shared"
)

const (
	displayTimeLayout = "Jan 02 15:04:05"
	compactTimeLayout = "Jan 02 15:04"
	fullTimeLayout    = "2006-01-02 15:04:05 MST"
)

type PageOptions struct {
	ActiveTab     string
	HistoryLimit  int64
	HistoryOffset int64
}

func (h *Handler) model(ctx context.Context, ticks int, now time.Time, options PageOptions, streaming bool) Model {
	if options.ActiveTab == "" {
		options.ActiveTab = "overview"
	}
	liveModel := h.liveModel(ctx, now, streaming)
	return Model{
		Title:           "Kyros Arbitrage",
		StartedAt:       displayTimestamp(h.startedAt),
		StreamURL:       streamURL(options),
		Heartbeat:       h.heartbeatView(ticks, now),
		Metrics:         metrics(liveModel),
		Live:            liveModel,
		Speed:           h.speedView(now),
		Risk:            h.riskView(),
		Exchanges:       h.exchangesView(now),
		Historic:        h.cachedHistoricView(ctx, options.HistoryLimit, options.HistoryOffset, streaming),
		OverviewActive:  options.ActiveTab == "overview",
		ExchangesActive: options.ActiveTab == "connections",
		HistoricActive:  options.ActiveTab == "historic",
	}
}

func (h *Handler) heartbeatView(ticks int, now time.Time) HeartbeatView {
	return HeartbeatView{
		Connected:   true,
		StatusLabel: "Stream connected",
		StatusClass: "bg-emerald-500",
		ServerTime:  displayCompactTimestamp(now),
		ServerTitle: displayFullTimestamp(now),
		Ticks:       ticks,
		TicksLabel:  strconv.Itoa(ticks),
		Uptime:      time.Since(h.startedAt).Round(time.Second).String(),
	}
}

func metrics(model live.Model) shared.Metrics {
	return shared.Metrics{
		LiveFeeds:             strconv.Itoa(model.LiveFeeds),
		BestNetPnl:            model.BestNetPnl,
		BestNetState:          model.BestNetState,
		SessionPnl:            model.SessionPnl,
		Executed:              model.Executed,
		HistoryTotalPnl:       model.History.TotalPnl,
		HistoryExecutionCount: model.History.ExecutionCount,
		Rejected:              model.Rejected,
		StaleFeeds:            strconv.Itoa(model.StaleFeeds),
	}
}

func (h *Handler) liveModel(ctx context.Context, now time.Time, streaming bool) live.Model {
	snapshots := h.marketStore.Snapshot()
	projection := market.Project(snapshots, now)
	metricsSnapshot := h.metrics.Snapshot(now, projection.Feeds)
	feedMetrics := make(map[string]speed.FeedRow, len(metricsSnapshot.Feeds))
	for _, row := range metricsSnapshot.Feeds {
		feedMetrics[feedMetricKey(row.Exchange, row.Market)] = speed.FeedRow{
			AgeP50:     formatMillis(row.FeedAgeP50MS),
			AgeP95:     formatMillis(row.FeedAgeP95MS),
			UpdateRate: formatRate(row.UpdateRate),
		}
	}
	rows := make([]live.FeedRow, 0, len(projection.Feeds))
	liveFeeds := 0
	staleFeeds := 0
	for _, feedRow := range projection.Feeds {
		if feedRow.Status == exchange.StatusLive {
			liveFeeds++
		}
		if feedRow.Status == exchange.StatusStale || feedRow.Status == exchange.StatusError {
			staleFeeds++
		}
		metric := feedMetrics[feedMetricKey(feedRow.Exchange, feedRow.Market)]
		rows = append(rows, live.FeedRow{
			Exchange:    titleCase(string(feedRow.Exchange)),
			Market:      feedRow.Market,
			Status:      string(feedRow.Status),
			StatusClass: statusClass(feedRow.Status),
			Transport:   string(feedRow.Transport),
			Bid:         displayQuoteString(feedRow.Bid),
			BidSize:     displayBaseString(feedRow.BidSize),
			Ask:         displayQuoteString(feedRow.Ask),
			AskSize:     displayBaseString(feedRow.AskSize),
			Levels:      strconv.Itoa(feedRow.Levels),
			Age:         displayMillis(feedRow.AgeMS),
			AgeP50:      displayMetricValue(metric.AgeP50),
			AgeP95:      displayMetricValue(metric.AgeP95),
			UpdateRate:  displayMetricValue(metric.UpdateRate),
			Latency:     displayMillis(feedRow.LatencyMS),
			Sequence:    displaySequence(feedRow.Sequence),
			Message:     feedRow.Message,
		})
	}

	decisionSnapshot := h.decisionEngine.Project(snapshots, now)
	opportunities := make([]live.OpportunityRow, 0, len(decisionSnapshot.Opportunities))
	for _, opportunity := range decisionSnapshot.Opportunities {
		opportunities = append(opportunities, live.OpportunityRow{
			Route:             displayRoute(opportunity.BuyExchange, opportunity.SellExchange),
			Style:             displayLiquidityStyle(string(opportunity.BuyLiquidity), string(opportunity.SellLiquidity)),
			Market:            displayMarket(opportunity.Market),
			Size:              displayBase(opportunity.BaseSize),
			GrossPnl:          displaySignedQuote(opportunity.GrossProfit),
			GrossBPS:          displaySignedBPS(opportunity.GrossBPS),
			Fees:              displayQuote(opportunity.TradingFees),
			Slippage:          displayQuote(opportunity.SlippageCost),
			Latency:           displayQuote(opportunity.LatencyPenalty),
			Rebalance:         displayQuote(opportunity.RebalanceCost),
			RebalanceExposure: displayQuote(opportunity.RebalanceExposure),
			FeeHurdle:         displaySignedBPS(opportunity.FeeHurdleBPS),
			EdgeAfterFees:     displaySignedBPS(opportunity.EdgeAfterFeesBPS),
			Missing:           displayBPS(opportunity.MissingBPS),
			CostStack:         displayCostStack(opportunity),
			ExpectedNet:       displaySignedQuote(opportunity.ExpectedNetProfit),
			NetBPS:            displaySignedBPS(opportunity.ExpectedNetBPS),
			Decision:          string(opportunity.Decision),
			DecisionClass:     decisionClass(string(opportunity.Decision)),
			ReasonLabel:       displayReasonLabel(opportunity.ReasonCode),
			Reason:            opportunity.ReasonCode,
		})
	}
	termsRows := make([]live.TermsSourceRow, 0, len(decisionSnapshot.TermsHealth))
	for _, row := range decisionSnapshot.TermsHealth {
		termsRows = append(termsRows, live.TermsSourceRow{
			Exchange: titleCase(string(row.Exchange)),
			Market:   displayMarket(row.Market),
			Source:   string(row.Source),
			Status:   displayFresh(row.Fresh),
			Message:  row.Message,
			Updated:  displayTimestamp(row.UpdatedAt),
		})
	}
	balanceRows := make([]live.BalanceRow, 0, len(decisionSnapshot.Balances))
	for _, row := range decisionSnapshot.Balances {
		balanceRows = append(balanceRows, live.BalanceRow{
			Exchange: titleCase(string(row.Exchange)),
			Asset:    row.Asset,
			Amount:   displayAssetAmount(row.Asset, row.Amount),
			Source:   string(row.Source),
		})
	}
	bestNet, bestState := bestOpportunity(opportunities)
	if decisionSnapshot.HasSessionBestNet {
		bestNet = displaySignedQuote(decisionSnapshot.SessionBestNet.ExpectedNetProfit)
		bestState = bestNetState(decisionSnapshot.SessionBestNet)
	}

	return live.Model{
		FeedRows:        rows,
		OpportunityRows: opportunities,
		TermsRows:       termsRows,
		BalanceRows:     balanceRows,
		BalanceGroups:   balanceGroups(balanceRows),
		History:         h.cachedHistoryView(ctx, streaming),
		LiveFeeds:       liveFeeds,
		StaleFeeds:      staleFeeds,
		BestNetPnl:      bestNet,
		BestNetState:    bestState,
		SessionPnl:      displaySignedQuote(decisionSnapshot.SessionPNL),
		Executed:        strconv.Itoa(decisionSnapshot.Executed),
		Rejected:        strconv.Itoa(decisionSnapshot.Rejected),
		LastUpdated:     displayTimestamp(decisionSnapshot.LastUpdated),
	}
}

func (h *Handler) riskView() riskui.Model {
	state := h.riskController.State()
	return riskui.Model{
		Mode:               string(state.Mode),
		Status:             string(state.Status),
		StatusClass:        riskStatusClass(state.Status),
		Reasons:            state.Reasons,
		CircuitState:       displayCircuitState(state.CircuitOpen),
		CircuitReason:      displayCircuitReason(state.CircuitReason),
		HaltedAt:           displayTimestamp(state.HaltedAt),
		ResetVisible:       state.CircuitOpen,
		MaxSpread:          displayBPS(state.Thresholds.MaxSpreadBPS),
		MaxLatencyPenalty:  displayBPS(state.Thresholds.MaxLatencyPenaltyBPS),
		MaxDrawdown:        displayQuote(state.Thresholds.MaxDrawdown),
		Reserve:            displayPercent(state.Thresholds.ReserveRate),
		ConservativeActive: state.Mode == risk.ModeConservative,
		BalancedActive:     state.Mode == risk.ModeBalanced,
		AggressiveActive:   state.Mode == risk.ModeAggressive,
	}
}

func (h *Handler) speedView(now time.Time) speed.Model {
	projection := market.Project(h.marketStore.Snapshot(), now)
	snapshot := h.metrics.Snapshot(now, projection.Feeds)
	feedRows := make([]speed.FeedRow, 0, len(snapshot.Feeds))
	for _, row := range snapshot.Feeds {
		feedRows = append(feedRows, speed.FeedRow{
			Exchange:   titleCase(string(row.Exchange)),
			Market:     row.Market,
			AgeP50:     formatMillis(row.FeedAgeP50MS),
			AgeP95:     formatMillis(row.FeedAgeP95MS),
			UpdateRate: formatRate(row.UpdateRate),
			Latency:    displayMillis(row.LastLatency),
			LastSeen:   displayTimestamp(row.LastSeen),
		})
	}
	marketRows := make([]speed.MarketRow, 0, len(snapshot.Markets))
	for _, row := range snapshot.Markets {
		marketRows = append(marketRows, speed.MarketRow{
			Market: row.Market,
			Live:   strconv.Itoa(row.Live),
			Stale:  strconv.Itoa(row.Stale),
			Errors: strconv.Itoa(row.Errors),
		})
	}
	return speed.Model{
		LastDecisionLatency: formatMillis(snapshot.LastDecisionLatencyMS),
		EvaluationDuration:  formatMillis(snapshot.EvaluationDurationMS),
		DecisionP95:         formatMillis(snapshot.DecisionP95MS),
		FeedRows:            feedRows,
		MarketRows:          marketRows,
	}
}

func (h *Handler) exchangesView(now time.Time) exchanges.Model {
	snapshots := h.termsStore.All()
	exchangeCards := make([]exchanges.ExchangeCardView, 0, len(snapshots))
	exchangeCardIndexes := make(map[string]int, len(snapshots))

	for _, snapshot := range snapshots {
		exchangeName := titleCase(string(snapshot.Exchange))
		marketName := displayMarket(snapshot.Market)
		source := string(snapshot.Source)
		fresh := snapshot.IsFresh(now)
		status := displayFresh(fresh)

		ruleRow := exchanges.RuleRow{
			Exchange:    exchangeName,
			Market:      marketName,
			MakerFee:    displayRateBPS(snapshot.Fees.MakerRate),
			TakerFee:    displayRateBPS(snapshot.Fees.TakerRate),
			MinBase:     displayOptionalAssetAmount(snapshot.Market.Base, snapshot.Constraints.MinBase),
			MinNotional: displayOptionalAssetAmount(snapshot.Market.Quote, snapshot.Constraints.MinNotional),
			StepSize:    displayOptionalAssetAmount(snapshot.Market.Base, snapshot.Constraints.StepSize),
			TickSize:    displayOptionalAssetAmount(snapshot.Market.Quote, snapshot.Constraints.TickSize),
			Source:      source,
		}

		exchangeCardIndex, ok := exchangeCardIndexes[exchangeName]
		if !ok {
			exchangeCards = append(exchangeCards, exchanges.ExchangeCardView{
				Exchange:    exchangeName,
				Market:      marketName,
				Source:      source,
				Status:      status,
				StatusClass: termsStatusClass(fresh),
				Updated:     displayTimestamp(snapshot.UpdatedAt),
				Expires:     displayTimestamp(snapshot.ExpiresAt),
				Message:     snapshot.Message,
			})
			exchangeCardIndex = len(exchangeCards) - 1
			exchangeCardIndexes[exchangeName] = exchangeCardIndex
		}
		exchangeCards[exchangeCardIndex].Rules = append(exchangeCards[exchangeCardIndex].Rules, ruleRow)

		for _, asset := range []string{snapshot.Market.Base, snapshot.Market.Quote} {
			balanceRow := exchanges.BalanceRow{
				Exchange: exchangeName,
				Market:   marketName,
				Asset:    asset,
				Amount:   displayBalance(snapshot, asset),
				Source:   source,
			}
			transferRow := exchanges.TransferRow{
				Exchange: exchangeName,
				Market:   marketName,
				Asset:    asset,
				Fee:      displayTransferFee(snapshot, asset),
				Source:   source,
			}
			exchangeCards[exchangeCardIndex].Balances = append(exchangeCards[exchangeCardIndex].Balances, balanceRow)
			exchangeCards[exchangeCardIndex].Transfers = append(exchangeCards[exchangeCardIndex].Transfers, transferRow)
		}
	}

	return exchanges.Model{
		ExchangeCards: exchangeCards,
		LastUpdated:   displayTimestamp(now),
	}
}

func historyViewFromReport(report history.Report) live.HistoryView {
	opportunities := make([]live.HistoryOpportunityRow, 0, len(report.Opportunities))
	for _, row := range report.Opportunities {
		observed, observedFull := displayHistoryTime(row.ObservedAt)
		opportunities = append(opportunities, live.HistoryOpportunityRow{
			Observed:     observed,
			ObservedFull: observedFull,
			Route:        displayHistoryRoute(row.BuyExchange, row.SellExchange),
			Style:        displayLiquidityStyle(row.BuyLiquidity, row.SellLiquidity),
			Market:       displayHistoryMarket(row.Market),
			Size:         displayDecimalAsBase(row.BaseSize),
			ExpectedNet:  displaySignedDecimalAsQuote(row.ExpectedNetProfit),
			Decision:     row.Decision,
			Reason:       row.ReasonCode,
		})
	}
	executions := make([]live.HistoryExecutionRow, 0, len(report.Executions))
	for _, row := range report.Executions {
		executed, executedFull := displayHistoryTime(row.ExecutedAt)
		executions = append(executions, live.HistoryExecutionRow{
			Executed:     executed,
			ExecutedFull: executedFull,
			Route:        displayHistoryRoute(row.BuyExchange, row.SellExchange),
			Style:        displayLiquidityStyle(row.BuyLiquidity, row.SellLiquidity),
			Market:       displayHistoryMarket(row.Market),
			Size:         displayDecimalAsBase(row.BaseSize),
			NetProfit:    displaySignedDecimalAsQuote(row.NetProfit),
			TermsSource:  row.TermsSource,
		})
	}
	return live.HistoryView{
		Path:             report.Summary.Path,
		Status:           historyStatusPersisted,
		OpportunityCount: strconv.FormatInt(report.Summary.Opportunities, 10),
		ExecutionCount:   strconv.FormatInt(report.Summary.Executions, 10),
		TotalPnl:         displaySignedDecimalAsQuote(report.Summary.TotalPNL),
		OpportunityRows:  opportunities,
		ExecutionRows:    executions,
	}
}

func historicViewFromReport(report history.Report, limit int64, offset int64) historic.Model {
	opportunities := make([]historic.OpportunityRow, 0, len(report.Opportunities))
	for _, row := range report.Opportunities {
		observed, observedFull := displayHistoryTime(row.ObservedAt)
		opportunities = append(opportunities, historic.OpportunityRow{
			Observed:          observed,
			ObservedFull:      observedFull,
			Route:             displayHistoryRoute(row.BuyExchange, row.SellExchange),
			Style:             displayLiquidityStyle(row.BuyLiquidity, row.SellLiquidity),
			Market:            displayHistoryMarket(row.Market),
			Size:              displayDecimalAsBase(row.BaseSize),
			BuyNotional:       displayDecimalAsQuote(row.BuyNotional),
			SellNotional:      displayDecimalAsQuote(row.SellNotional),
			GrossProfit:       displaySignedDecimalAsQuote(row.GrossProfit),
			GrossBPS:          displaySignedDecimalAsBPS(row.GrossBPS),
			TradingFees:       displayDecimalAsQuote(row.TradingFees),
			SlippageCost:      displayDecimalAsQuote(row.SlippageCost),
			LatencyPenalty:    displayDecimalAsQuote(row.LatencyPenalty),
			RebalanceCost:     displayDecimalAsQuote(row.RebalanceCost),
			RebalanceExposure: displayDecimalAsQuote(row.RebalanceExposure),
			FeeHurdleBPS:      displaySignedDecimalAsBPS(row.FeeHurdleBPS),
			EdgeAfterFeesBPS:  displaySignedDecimalAsBPS(row.EdgeAfterFeesBPS),
			MissingBPS:        displayDecimalAsBPS(row.MissingBPS),
			ExpectedNetProfit: displaySignedDecimalAsQuote(row.ExpectedNetProfit),
			ExpectedNetBPS:    displaySignedDecimalAsBPS(row.ExpectedNetBPS),
			Decision:          row.Decision,
			Reason:            row.ReasonCode,
			Partial:           displayBool(row.Partial),
		})
	}

	executions := make([]historic.ExecutionRow, 0, len(report.Executions))
	for _, row := range report.Executions {
		executed, executedFull := displayHistoryTime(row.ExecutedAt)
		executions = append(executions, historic.ExecutionRow{
			Executed:          executed,
			ExecutedFull:      executedFull,
			Route:             displayHistoryRoute(row.BuyExchange, row.SellExchange),
			Style:             displayLiquidityStyle(row.BuyLiquidity, row.SellLiquidity),
			Market:            displayHistoryMarket(row.Market),
			Size:              displayDecimalAsBase(row.BaseSize),
			BuyNotional:       displayDecimalAsQuote(row.BuyNotional),
			SellNotional:      displayDecimalAsQuote(row.SellNotional),
			BuyFee:            displayDecimalAsQuote(row.BuyFee),
			SellFee:           displayDecimalAsQuote(row.SellFee),
			LatencyPenalty:    displayDecimalAsQuote(row.LatencyPenalty),
			RebalanceCost:     displayDecimalAsQuote(row.RebalanceCost),
			RebalanceExposure: displayDecimalAsQuote(row.RebalanceExposure),
			NetProfit:         displaySignedDecimalAsQuote(row.NetProfit),
			TermsSource:       row.TermsSource,
		})
	}

	return historic.Model{
		Path:             report.Summary.Path,
		Status:           historyStatusPersisted,
		OpportunityCount: strconv.FormatInt(report.Summary.Opportunities, 10),
		ExecutionCount:   strconv.FormatInt(report.Summary.Executions, 10),
		TotalPnl:         displaySignedDecimalAsQuote(report.Summary.TotalPNL),
		Page:             historicPage(report.Pagination.Opportunities, report.Pagination.Executions),
		OpportunityRows:  opportunities,
		ExecutionRows:    executions,
	}
}

func titleCase(value string) string {
	switch value {
	case "okx":
		return "OKX"
	case "kucoin":
		return "KuCoin"
	case "gate":
		return "Gate.io"
	}
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func statusClass(status exchange.FeedStatus) string {
	switch status {
	case exchange.StatusLive:
		return "bg-emerald-500"
	case exchange.StatusStale:
		return "bg-amber-500"
	case exchange.StatusError:
		return "bg-red-500"
	case exchange.StatusConnecting:
		return "bg-sky-500"
	default:
		return "bg-muted-foreground"
	}
}

func decisionClass(decision string) string {
	switch strings.ToLower(decision) {
	case "execute":
		return "decision-pill-execute"
	case "wait":
		return "decision-pill-wait"
	case "skip":
		return "decision-pill-skip"
	default:
		return "decision-pill-muted"
	}
}

func riskStatusClass(status risk.Status) string {
	switch status {
	case risk.StatusNormal:
		return "bg-emerald-500"
	case risk.StatusWarning:
		return "bg-amber-500"
	case risk.StatusHalted:
		return "bg-red-500"
	default:
		return "bg-muted-foreground"
	}
}

func feedMetricKey(exchangeID exchange.ID, market string) string {
	return string(exchangeID) + "|" + market
}

func displayMetricValue(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func displayReasonLabel(reason string) string {
	if reason == "" {
		return "review"
	}
	value := reason
	for _, prefix := range []string{"EXECUTED_", "SKIP_", "WAITING_", "WAIT_"} {
		value = strings.TrimPrefix(value, prefix)
	}
	value = strings.ToLower(strings.ReplaceAll(value, "_", " "))
	if len(value) > 28 {
		return strings.TrimSpace(value[:28])
	}
	return value
}

func balanceGroups(rows []live.BalanceRow) []live.BalanceGroup {
	groups := make([]live.BalanceGroup, 0)
	indexes := make(map[string]int)
	for _, row := range rows {
		index, ok := indexes[row.Exchange]
		if !ok {
			index = len(groups)
			indexes[row.Exchange] = index
			groups = append(groups, live.BalanceGroup{Exchange: row.Exchange})
		}
		groups[index].Assets = append(groups[index].Assets, live.BalanceAssetRow{
			Asset:  row.Asset,
			Amount: row.Amount,
		})
	}
	return groups
}

func displayCircuitState(open bool) string {
	if open {
		return "open"
	}
	return "armed"
}

func displayCircuitReason(reason string) string {
	if reason == "" {
		return "-"
	}
	return reason
}

func displayMillis(value *int64) string {
	if value == nil {
		return "-"
	}
	return formatMillis(*value)
}

func displayTimestamp(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format(displayTimeLayout)
}

func displayCompactTimestamp(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format(compactTimeLayout)
}

func displayFullTimestamp(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format(fullTimeLayout)
}

func formatMillis(value int64) string {
	if value <= 0 {
		return "<1 ms"
	}
	if value < 1000 {
		return strconv.FormatInt(value, 10) + " ms"
	}
	return strconv.FormatFloat(float64(value)/1000, 'f', 2, 64) + " s"
}

func formatRate(value float64) string {
	if value < 0.01 {
		return "<0.01/s"
	}
	if value < 10 {
		return strconv.FormatFloat(value, 'f', 2, 64) + "/s"
	}
	return strconv.FormatFloat(value, 'f', 1, 64) + "/s"
}

func displayQuoteString(value string) string {
	parsed, ok := parseDecimal(value)
	if !ok {
		return "-"
	}
	return displayQuote(parsed)
}

func displayBaseString(value string) string {
	parsed, ok := parseDecimal(value)
	if !ok {
		return "-"
	}
	return displayBase(parsed)
}

func displayQuote(value decimal.Decimal) string {
	return fmt.Sprintf("%.2f", value)
}

func displaySignedQuote(value decimal.Decimal) string {
	if value.IsPos() {
		return "+" + displayQuote(value)
	}
	return displayQuote(value)
}

func displaySignedBPS(value decimal.Decimal) string {
	if value.IsPos() {
		return "+" + displayBPS(value)
	}
	return displayBPS(value)
}

func displayBase(value decimal.Decimal) string {
	return trimTrailingZeros(fmt.Sprintf("%.8f", value))
}

func displayDecimalAsBase(value string) string {
	parsed, ok := parseDecimal(value)
	if !ok {
		return "-"
	}
	return displayBase(parsed)
}

func displayDecimalAsQuote(value string) string {
	parsed, ok := parseDecimal(value)
	if !ok {
		return "-"
	}
	return displayQuote(parsed)
}

func displaySignedDecimalAsQuote(value string) string {
	parsed, ok := parseDecimal(value)
	if !ok {
		return "-"
	}
	return displaySignedQuote(parsed)
}

func displaySignedDecimalAsBPS(value string) string {
	parsed, ok := parseDecimal(value)
	if !ok {
		return "-"
	}
	return displaySignedBPS(parsed)
}

func displayDecimalAsBPS(value string) string {
	parsed, ok := parseDecimal(value)
	if !ok {
		return "-"
	}
	return displayBPS(parsed)
}

func displayAssetAmount(asset string, value decimal.Decimal) string {
	switch asset {
	case "BTC":
		return displayBase(value)
	default:
		return displayQuote(value)
	}
}

func displayOptionalAssetAmount(asset string, value decimal.Decimal) string {
	if value.IsZero() {
		return "-"
	}
	return displayAssetAmount(asset, value)
}

func displayBPS(value decimal.Decimal) string {
	return fmt.Sprintf("%.2f bps", value)
}

func displayRateBPS(value decimal.Decimal) string {
	if value.IsZero() {
		return "-"
	}
	bpsValue, err := value.Mul(decimal.MustNew(10000, 0))
	if err != nil {
		return "-"
	}
	return displayBPS(bpsValue)
}

func displayPercent(value decimal.Decimal) string {
	percent, err := value.Mul(decimal.MustNew(100, 0))
	if err != nil {
		return "-"
	}
	return fmt.Sprintf("%.2f%%", percent)
}

func displayBalance(snapshot terms.Snapshot, asset string) string {
	if snapshot.Balances == nil {
		return "-"
	}
	value, ok := snapshot.Balances[asset]
	if !ok {
		return "-"
	}
	return displayAssetAmount(asset, value)
}

func displayTransferFee(snapshot terms.Snapshot, asset string) string {
	if snapshot.TransferFees == nil {
		return "-"
	}
	value, ok := snapshot.TransferFees[asset]
	if !ok {
		return "-"
	}
	return displayAssetAmount(asset, value)
}

func parseDecimal(value string) (decimal.Decimal, bool) {
	if value == "" {
		return decimal.Zero, false
	}
	parsed, err := decimal.Parse(value)
	if err != nil {
		return decimal.Zero, false
	}
	return parsed, true
}

func trimTrailingZeros(value string) string {
	value = strings.TrimRight(value, "0")
	value = strings.TrimRight(value, ".")
	if value == "" || value == "-0" {
		return "0"
	}
	return value
}

func displaySequence(value int64) string {
	if value == 0 {
		return "-"
	}
	return strconv.FormatInt(value, 10)
}

func displayRoute(buyExchange exchange.ID, sellExchange exchange.ID) string {
	if buyExchange == "" || sellExchange == "" {
		return "-"
	}
	return titleCase(string(buyExchange)) + " -> " + titleCase(string(sellExchange))
}

func displayHistoryRoute(buyExchange string, sellExchange string) string {
	if buyExchange == "" || sellExchange == "" {
		return "-"
	}
	return titleCase(buyExchange) + " -> " + titleCase(sellExchange)
}

func displayLiquidityStyle(buyLiquidity string, sellLiquidity string) string {
	if buyLiquidity == "" {
		buyLiquidity = string(arbitrage.LiquidityTaker)
	}
	if sellLiquidity == "" {
		sellLiquidity = string(arbitrage.LiquidityTaker)
	}
	return buyLiquidity + "/" + sellLiquidity
}

func displayMarket(market exchange.Market) string {
	if market.Base == "" || market.Quote == "" {
		return "BTC/USDT"
	}
	return market.ID()
}

func displayHistoryMarket(market string) string {
	if market == "" {
		return "-"
	}
	return market
}

func displayHistoryTimestamp(value string) string {
	if value == "" {
		return "-"
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return displayTimestamp(parsed)
}

func displayHistoryTime(value string) (string, string) {
	if value == "" {
		return "-", "-"
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value, value
	}
	return displayCompactTimestamp(parsed), displayFullTimestamp(parsed)
}

func displayFresh(fresh bool) string {
	if fresh {
		return "fresh"
	}
	return "stale"
}

func termsStatusClass(fresh bool) string {
	if fresh {
		return "bg-emerald-500"
	}
	return "bg-amber-400"
}

func bestOpportunity(rows []live.OpportunityRow) (string, string) {
	if len(rows) == 0 {
		return "-", "waiting for live books"
	}
	return rows[0].ExpectedNet, rows[0].Reason
}

func bestNetState(opportunity arbitrage.Opportunity) string {
	route := displayRoute(opportunity.BuyExchange, opportunity.SellExchange)
	if route == "-" {
		return "session best: " + opportunity.ReasonCode
	}
	return "session best: " + route + " / " + opportunity.ReasonCode
}

func displayCostStack(opportunity arbitrage.Opportunity) string {
	return "style " + displayLiquidityStyle(string(opportunity.BuyLiquidity), string(opportunity.SellLiquidity)) +
		" / hurdle " + displayBPS(opportunity.FeeHurdleBPS) +
		" / edge after fees " + displayBPS(opportunity.EdgeAfterFeesBPS) +
		" / missing " + displayBPS(opportunity.MissingBPS) +
		" / fees " + displayQuote(opportunity.TradingFees) +
		" / slip " + displayQuote(opportunity.SlippageCost) +
		" / latency " + displayQuote(opportunity.LatencyPenalty) +
		" / rebalance charged " + displayQuote(opportunity.RebalanceCost) +
		" / rebalance exposure " + displayQuote(opportunity.RebalanceExposure)
}

func displayBool(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func historicPage(opportunities history.Page, executions history.Page) historic.PageView {
	limit := opportunities.Limit
	offset := opportunities.Offset
	total := opportunities.Total
	if executions.Total > total {
		total = executions.Total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	start := int64(0)
	if total > 0 {
		start = offset + 1
	}
	prevOffset := offset - limit
	if prevOffset < 0 {
		prevOffset = 0
	}
	return historic.PageView{
		Label:   fmt.Sprintf("%d-%d / %d", start, end, total),
		PrevURL: historicURL(limit, prevOffset),
		NextURL: historicURL(limit, offset+limit),
		HasPrev: offset > 0,
		HasNext: opportunities.HasNext || executions.HasNext,
	}
}

func historicURL(limit int64, offset int64) string {
	return fmt.Sprintf("/?tab=historic&history_limit=%d&history_offset=%d", limit, offset)
}
