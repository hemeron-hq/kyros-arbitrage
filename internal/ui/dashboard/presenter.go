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
	"github.com/hemeron-hq/kyros-arbitrage/internal/market"
	"github.com/hemeron-hq/kyros-arbitrage/internal/risk"
	"github.com/hemeron-hq/kyros-arbitrage/internal/terms"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/exchanges"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/live"
	riskui "github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/risk"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/speed"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/shared"
)

const displayTimeLayout = "Jan 2, 2006 15:04:05"

func (h *Handler) model(ctx context.Context, ticks int, now time.Time) Model {
	liveModel := h.liveModel(ctx, now)
	return Model{
		Title:     "Kyros Arbitrage",
		StartedAt: displayTimestamp(h.startedAt),
		Heartbeat: h.heartbeatView(ticks, now),
		Metrics:   metrics(liveModel),
		Live:      liveModel,
		Speed:     h.speedView(now),
		Risk:      h.riskView(),
		Exchanges: h.exchangesView(now),
	}
}

func (h *Handler) heartbeatView(ticks int, now time.Time) HeartbeatView {
	return HeartbeatView{
		Connected:   true,
		StatusLabel: "Stream connected",
		StatusClass: "bg-emerald-500",
		ServerTime:  displayTimestamp(now),
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

func (h *Handler) liveModel(ctx context.Context, now time.Time) live.Model {
	snapshots := h.marketStore.Snapshot()
	projection := market.Project(snapshots, now)
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
			Latency:     displayMillis(feedRow.LatencyMS),
			Sequence:    displaySequence(feedRow.Sequence),
			Message:     feedRow.Message,
		})
	}

	decisionSnapshot := h.decisionEngine.Project(snapshots, now)
	opportunities := make([]live.OpportunityRow, 0, len(decisionSnapshot.Opportunities))
	for _, opportunity := range decisionSnapshot.Opportunities {
		opportunities = append(opportunities, live.OpportunityRow{
			Route:       displayRoute(opportunity.BuyExchange, opportunity.SellExchange),
			Market:      displayMarket(opportunity.Market),
			Size:        displayBase(opportunity.BaseSize),
			GrossPnl:    displaySignedQuote(opportunity.GrossProfit),
			Fees:        displayQuote(opportunity.TradingFees),
			Slippage:    displayQuote(opportunity.SlippageCost),
			Latency:     displayQuote(opportunity.LatencyPenalty),
			Rebalance:   displayQuote(opportunity.RebalanceCost),
			ExpectedNet: displaySignedQuote(opportunity.ExpectedNetProfit),
			Decision:    string(opportunity.Decision),
			Reason:      opportunity.ReasonCode,
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
		History:         h.historyView(ctx),
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

func (h *Handler) historyView(ctx context.Context) live.HistoryView {
	reportCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	report, err := h.historyStore.Report(reportCtx, 8)
	if err != nil {
		return live.HistoryView{
			Path:   h.database.Path(),
			Status: "history unavailable",
		}
	}
	opportunities := make([]live.HistoryOpportunityRow, 0, len(report.Opportunities))
	for _, row := range report.Opportunities {
		opportunities = append(opportunities, live.HistoryOpportunityRow{
			Observed:    displayHistoryTimestamp(row.ObservedAt),
			Route:       displayHistoryRoute(row.BuyExchange, row.SellExchange),
			Market:      displayHistoryMarket(row.Market),
			Size:        displayDecimalAsBase(row.BaseSize),
			ExpectedNet: displaySignedDecimalAsQuote(row.ExpectedNetProfit),
			Decision:    row.Decision,
			Reason:      row.ReasonCode,
		})
	}
	executions := make([]live.HistoryExecutionRow, 0, len(report.Executions))
	for _, row := range report.Executions {
		executions = append(executions, live.HistoryExecutionRow{
			Executed:    displayHistoryTimestamp(row.ExecutedAt),
			Route:       displayHistoryRoute(row.BuyExchange, row.SellExchange),
			Market:      displayHistoryMarket(row.Market),
			Size:        displayDecimalAsBase(row.BaseSize),
			NetProfit:   displaySignedDecimalAsQuote(row.NetProfit),
			TermsSource: row.TermsSource,
		})
	}
	return live.HistoryView{
		Path:             report.Summary.Path,
		Status:           "persisted",
		OpportunityCount: strconv.FormatInt(report.Summary.Opportunities, 10),
		ExecutionCount:   strconv.FormatInt(report.Summary.Executions, 10),
		TotalPnl:         displaySignedDecimalAsQuote(report.Summary.TotalPNL),
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
