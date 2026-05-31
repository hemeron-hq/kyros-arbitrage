package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	json "github.com/goccy/go-json"
	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/arbitrage"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange/binance"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange/kraken"
	"github.com/hemeron-hq/kyros-arbitrage/internal/history"
	"github.com/hemeron-hq/kyros-arbitrage/internal/market"
	"github.com/hemeron-hq/kyros-arbitrage/internal/platform/config"
	"github.com/hemeron-hq/kyros-arbitrage/internal/platform/database"
	"github.com/hemeron-hq/kyros-arbitrage/internal/portfolio"
	"github.com/hemeron-hq/kyros-arbitrage/internal/portfolio/paper"
	"github.com/hemeron-hq/kyros-arbitrage/internal/risk"
	"github.com/hemeron-hq/kyros-arbitrage/internal/terms"
	"github.com/hemeron-hq/kyros-arbitrage/internal/view"
	"github.com/starfederation/datastar-go/datastar"
	"github.com/templui/templui/utils"
)

const (
	assetsDir         = "assets"
	displayTimeLayout = "Jan 2, 2006 15:04:05"
	readHeaderTimeout = 5 * time.Second
	uiPatchInterval   = 250 * time.Millisecond
)

type Server struct {
	cfg                  config.Config
	startedAt            time.Time
	marketStore          *market.Store
	termsStore           *terms.Store
	ledger               portfolio.Store
	decisionEngine       *arbitrage.Engine
	riskController       *risk.Controller
	database             *database.Database
	historyStore         *history.Store
	disableMarketService bool
}

type Option func(*Server)

func WithMarketStore(store *market.Store) Option {
	return func(s *Server) {
		s.marketStore = store
	}
}

func WithMarketServiceDisabled() Option {
	return func(s *Server) {
		s.disableMarketService = true
	}
}

func New(cfg config.Config, opts ...Option) (*http.Server, error) {
	marketConfig := market.DefaultServiceConfig()
	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	appDatabase, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open database: %w", err)
	}
	historyStore := history.New(appDatabase)
	riskStore := risk.NewStore(appDatabase)
	riskController, err := risk.NewController(ctx, riskStore)
	if err != nil {
		cancel()
		_ = appDatabase.Close()
		return nil, fmt.Errorf("load risk settings: %w", err)
	}
	termsStore := terms.NewStore(now)
	ledger := paper.NewLedger()
	app := &Server{
		cfg:            cfg,
		startedAt:      now,
		marketStore:    market.NewStore(marketConfig.StaleAfter),
		termsStore:     termsStore,
		ledger:         ledger,
		riskController: riskController,
		database:       appDatabase,
		historyStore:   historyStore,
	}
	app.decisionEngine = arbitrage.NewEngine(app.termsStore, app.ledger, app.riskController)
	for _, opt := range opts {
		opt(app)
	}

	binanceProvider := binance.New(binance.WithCredentials(cfg.BinanceAPIKey, cfg.BinanceAPISecret))
	krakenProvider := kraken.New(kraken.WithCredentials(cfg.KrakenAPIKey, cfg.KrakenAPISecret))
	termsService := terms.NewService(
		app.termsStore,
		map[exchange.ID]exchange.TermsClient{
			exchange.Binance: binanceProvider,
			exchange.Kraken:  krakenProvider,
		},
		exchange.DefaultBindings(),
		terms.DefaultServiceConfig(),
	)
	termsService.Start(ctx)
	if !app.disableMarketService {
		service := market.NewService(
			app.marketStore,
			map[exchange.ID]exchange.MarketDataProvider{
				exchange.Binance: binanceProvider,
				exchange.Kraken:  krakenProvider,
			},
			exchange.DefaultBindings(),
			marketConfig,
		)
		service.Start(ctx)
	}
	go app.runDecisionLoop(ctx)

	httpServer := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           app.routes(),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	httpServer.RegisterOnShutdown(func() {
		cancel()
		_ = appDatabase.Close()
	})

	return httpServer, nil
}

func (s *Server) runDecisionLoop(ctx context.Context) {
	events := s.marketStore.Subscribe(ctx.Done())

	evaluate := func(now time.Time) {
		snapshot := s.decisionEngine.Evaluate(s.marketStore.Snapshot(), now)
		_ = s.historyStore.RecordSnapshot(ctx, snapshot)
	}
	evaluate(time.Now())

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				return
			}
			for {
				select {
				case _, ok := <-events:
					if !ok {
						return
					}
					continue
				default:
				}
				break
			}
			evaluate(time.Now())
		}
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/history", s.historyAPI)
	mux.HandleFunc("GET /api/risk", s.riskAPI)
	mux.HandleFunc("POST /api/risk/mode", s.setRiskMode)
	mux.HandleFunc("GET /stream", s.stream)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", s.assetHandler()))

	utils.SetupScriptRoutes(mux, s.cfg.Environment.IsDevelopment())

	return mux
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := view.Page(s.pageModel(r.Context())).Render(r.Context(), w); err != nil {
		http.Error(w, "render page", http.StatusInternalServerError)
	}
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	snapshot := market.Project(s.marketStore.Snapshot(), time.Now())
	live, stale, errors := countFeedStatuses(snapshot.Feeds)
	_ = json.NewEncoder(w).Encode(struct {
		OK      bool   `json:"ok"`
		Status  string `json:"status"`
		Uptime  string `json:"uptime"`
		Feeds   int    `json:"feeds"`
		Live    int    `json:"live"`
		Stale   int    `json:"stale"`
		Errors  int    `json:"errors"`
		Markets int    `json:"markets"`
	}{
		OK:      true,
		Status:  healthStatus(len(snapshot.Feeds), live, stale, errors),
		Uptime:  time.Since(s.startedAt).Round(time.Second).String(),
		Feeds:   len(snapshot.Feeds),
		Live:    live,
		Stale:   stale,
		Errors:  errors,
		Markets: len(exchange.DefaultBindings()),
	})
}

func (s *Server) historyAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	report, err := s.historyStore.Report(r.Context(), 25)
	if err != nil {
		http.Error(w, "load history", http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(report)
}

func (s *Server) riskAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(s.riskController.State())
}

func (s *Server) setRiskMode(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "parse risk mode", http.StatusBadRequest)
		return
	}
	mode, err := risk.ParseMode(r.FormValue("mode"))
	if err != nil {
		http.Error(w, "invalid risk mode", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := s.riskController.SetMode(ctx, mode); err != nil {
		http.Error(w, "save risk mode", http.StatusInternalServerError)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	ticker := time.NewTicker(uiPatchInterval)
	defer ticker.Stop()

	ticks := 0
	if !s.patchDashboard(sse, ticks, time.Now()) {
		return
	}

	events := s.marketStore.Subscribe(sse.Context().Done())
	dirty := false
	nextHeartbeat := time.Now().Add(time.Second)

	for {
		select {
		case <-sse.Context().Done():
			return
		case _, ok := <-events:
			if !ok {
				return
			}
			dirty = true
		case now := <-ticker.C:
			if sse.IsClosed() {
				return
			}
			heartbeatDue := !now.Before(nextHeartbeat)
			if !dirty && !heartbeatDue {
				continue
			}
			if heartbeatDue {
				ticks++
				nextHeartbeat = now.Add(time.Second)
			}
			if !s.patchDashboard(sse, ticks, now) {
				return
			}
			dirty = false
		}
	}
}

func (s *Server) assetHandler() http.Handler {
	files := http.FileServer(http.Dir(assetsDir))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Environment.IsDevelopment() {
			w.Header().Set("Cache-Control", "no-store")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=31536000")
		}
		files.ServeHTTP(w, r)
	})
}

func (s *Server) patchDashboard(sse *datastar.ServerSentEventGenerator, ticks int, now time.Time) bool {
	heartbeat := s.heartbeatView(ticks, now)
	live := s.liveDashboardView(sse.Context(), now)
	signals := view.DashboardSignals{
		Connected:  heartbeat.Connected,
		ServerTime: heartbeat.ServerTime,
		Ticks:      heartbeat.Ticks,
		Streaming:  false,
		LiveFeeds:  live.LiveFeeds,
		StaleFeeds: live.StaleFeeds,
	}

	if err := sse.MarshalAndPatchSignals(signals); err != nil {
		return false
	}
	if err := sse.PatchElementTempl(view.MetricStrip(live)); err != nil {
		return false
	}
	if err := sse.PatchElementTempl(view.HeartbeatPanel(heartbeat)); err != nil {
		return false
	}
	if err := sse.PatchElementTempl(view.RiskPanel(live.Risk)); err != nil {
		return false
	}
	if err := sse.PatchElementTempl(view.LiveDashboard(live)); err != nil {
		return false
	}
	if err := sse.PatchElementTempl(view.ConnectionDashboard(live.Connection)); err != nil {
		return false
	}

	return true
}

func (s *Server) pageModel(ctx context.Context) view.PageModel {
	return view.PageModel{
		Title:     "Kyros Arbitrage",
		StartedAt: displayTimestamp(s.startedAt),
		Heartbeat: s.heartbeatView(0, time.Now()),
		Live:      s.liveDashboardView(ctx, time.Now()),
	}
}

func (s *Server) heartbeatView(ticks int, now time.Time) view.HeartbeatView {
	return view.HeartbeatView{
		Connected:   true,
		StatusLabel: "Stream connected",
		StatusClass: "bg-emerald-500",
		ServerTime:  displayTimestamp(now),
		Ticks:       ticks,
		TicksLabel:  strconv.Itoa(ticks),
		Uptime:      time.Since(s.startedAt).Round(time.Second).String(),
	}
}

func (s *Server) liveDashboardView(ctx context.Context, now time.Time) view.LiveDashboardView {
	snapshots := s.marketStore.Snapshot()
	projection := market.Project(snapshots, now)
	rows := make([]view.FeedRow, 0, len(projection.Feeds))
	liveFeeds := 0
	staleFeeds := 0
	for _, feedRow := range projection.Feeds {
		if feedRow.Status == exchange.StatusLive {
			liveFeeds++
		}
		if feedRow.Status == exchange.StatusStale || feedRow.Status == exchange.StatusError {
			staleFeeds++
		}
		rows = append(rows, view.FeedRow{
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

	decisionSnapshot := s.decisionEngine.Project(snapshots, now)
	opportunities := make([]view.OpportunityRow, 0, len(decisionSnapshot.Opportunities))
	for _, opportunity := range decisionSnapshot.Opportunities {
		opportunities = append(opportunities, view.OpportunityRow{
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
	termsRows := make([]view.TermsSourceRow, 0, len(decisionSnapshot.TermsHealth))
	for _, row := range decisionSnapshot.TermsHealth {
		termsRows = append(termsRows, view.TermsSourceRow{
			Exchange: titleCase(string(row.Exchange)),
			Market:   displayMarket(row.Market),
			Source:   string(row.Source),
			Status:   displayFresh(row.Fresh),
			Message:  row.Message,
			Updated:  displayTimestamp(row.UpdatedAt),
		})
	}
	balanceRows := make([]view.BalanceRow, 0, len(decisionSnapshot.Balances))
	for _, row := range decisionSnapshot.Balances {
		balanceRows = append(balanceRows, view.BalanceRow{
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
	historyView := s.historyView(ctx)

	return view.LiveDashboardView{
		FeedRows:        rows,
		OpportunityRows: opportunities,
		TermsRows:       termsRows,
		BalanceRows:     balanceRows,
		Risk:            s.riskView(),
		Connection:      s.connectionTermsView(now),
		History:         historyView,
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

func (s *Server) riskView() view.RiskView {
	state := s.riskController.State()
	return view.RiskView{
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

func (s *Server) connectionTermsView(now time.Time) view.ConnectionTermsView {
	snapshots := s.termsStore.All()
	summaryRows := make([]view.ConnectionSummaryRow, 0, len(snapshots))
	ruleRows := make([]view.ConnectionRuleRow, 0, len(snapshots))
	balanceRows := make([]view.ConnectionBalanceRow, 0, len(snapshots)*2)
	transferRows := make([]view.ConnectionTransferRow, 0, len(snapshots)*2)

	for _, snapshot := range snapshots {
		exchangeName := titleCase(string(snapshot.Exchange))
		marketName := displayMarket(snapshot.Market)
		source := string(snapshot.Source)

		summaryRows = append(summaryRows, view.ConnectionSummaryRow{
			Exchange: exchangeName,
			Market:   marketName,
			Source:   source,
			Status:   displayFresh(snapshot.IsFresh(now)),
			Updated:  displayTimestamp(snapshot.UpdatedAt),
			Expires:  displayTimestamp(snapshot.ExpiresAt),
			Message:  snapshot.Message,
		})
		ruleRows = append(ruleRows, view.ConnectionRuleRow{
			Exchange:    exchangeName,
			Market:      marketName,
			MakerFee:    displayRateBPS(snapshot.Fees.MakerRate),
			TakerFee:    displayRateBPS(snapshot.Fees.TakerRate),
			MinBase:     displayOptionalAssetAmount(snapshot.Market.Base, snapshot.Constraints.MinBase),
			MinNotional: displayOptionalAssetAmount(snapshot.Market.Quote, snapshot.Constraints.MinNotional),
			StepSize:    displayOptionalAssetAmount(snapshot.Market.Base, snapshot.Constraints.StepSize),
			TickSize:    displayOptionalAssetAmount(snapshot.Market.Quote, snapshot.Constraints.TickSize),
			Source:      source,
		})

		for _, asset := range []string{snapshot.Market.Base, snapshot.Market.Quote} {
			balanceRows = append(balanceRows, view.ConnectionBalanceRow{
				Exchange: exchangeName,
				Market:   marketName,
				Asset:    asset,
				Amount:   displayBalance(snapshot, asset),
				Source:   source,
			})
			transferRows = append(transferRows, view.ConnectionTransferRow{
				Exchange: exchangeName,
				Market:   marketName,
				Asset:    asset,
				Fee:      displayTransferFee(snapshot, asset),
				Source:   source,
			})
		}
	}

	return view.ConnectionTermsView{
		SummaryRows:  summaryRows,
		RuleRows:     ruleRows,
		BalanceRows:  balanceRows,
		TransferRows: transferRows,
		LastUpdated:  displayTimestamp(now),
	}
}

func (s *Server) historyView(ctx context.Context) view.HistoryView {
	reportCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	report, err := s.historyStore.Report(reportCtx, 8)
	if err != nil {
		return view.HistoryView{
			Path:   s.database.Path(),
			Status: "history unavailable",
		}
	}
	opportunities := make([]view.HistoryOpportunityRow, 0, len(report.Opportunities))
	for _, row := range report.Opportunities {
		opportunities = append(opportunities, view.HistoryOpportunityRow{
			Observed:    displayHistoryTimestamp(row.ObservedAt),
			Route:       displayHistoryRoute(row.BuyExchange, row.SellExchange),
			Market:      displayHistoryMarket(row.Market),
			Size:        displayDecimalAsBase(row.BaseSize),
			ExpectedNet: displaySignedDecimalAsQuote(row.ExpectedNetProfit),
			Decision:    row.Decision,
			Reason:      row.ReasonCode,
		})
	}
	executions := make([]view.HistoryExecutionRow, 0, len(report.Executions))
	for _, row := range report.Executions {
		executions = append(executions, view.HistoryExecutionRow{
			Executed:    displayHistoryTimestamp(row.ExecutedAt),
			Route:       displayHistoryRoute(row.BuyExchange, row.SellExchange),
			Market:      displayHistoryMarket(row.Market),
			Size:        displayDecimalAsBase(row.BaseSize),
			NetProfit:   displaySignedDecimalAsQuote(row.NetProfit),
			TermsSource: row.TermsSource,
		})
	}
	return view.HistoryView{
		Path:             report.Summary.Path,
		Status:           "persisted",
		OpportunityCount: strconv.FormatInt(report.Summary.Opportunities, 10),
		ExecutionCount:   strconv.FormatInt(report.Summary.Executions, 10),
		TotalPnl:         displaySignedDecimalAsQuote(report.Summary.TotalPNL),
		OpportunityRows:  opportunities,
		ExecutionRows:    executions,
	}
}

func countFeedStatuses(feeds []market.FeedProjection) (live int, stale int, errors int) {
	for _, row := range feeds {
		switch row.Status {
		case exchange.StatusLive:
			live++
		case exchange.StatusStale:
			stale++
		case exchange.StatusError:
			errors++
		}
	}
	return live, stale, errors
}

func healthStatus(total int, live int, stale int, errors int) string {
	if total == 0 {
		return "starting"
	}
	if errors > 0 || stale > 0 || live < total {
		return "degraded"
	}
	return "live"
}

func titleCase(value string) string {
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

func bestOpportunity(rows []view.OpportunityRow) (string, string) {
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
