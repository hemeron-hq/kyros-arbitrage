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
	"github.com/hemeron-hq/kyros-arbitrage/internal/market"
	"github.com/hemeron-hq/kyros-arbitrage/internal/platform/config"
	"github.com/hemeron-hq/kyros-arbitrage/internal/portfolio"
	"github.com/hemeron-hq/kyros-arbitrage/internal/portfolio/paper"
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
	decisionInterval  = 500 * time.Millisecond
)

type Server struct {
	cfg                  config.Config
	startedAt            time.Time
	marketStore          *market.Store
	termsStore           *terms.Store
	ledger               portfolio.Store
	decisionEngine       *arbitrage.Engine
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

func New(cfg config.Config, opts ...Option) *http.Server {
	marketConfig := market.DefaultServiceConfig()
	now := time.Now()
	termsStore := terms.NewStore(now)
	ledger := paper.NewLedger()
	app := &Server{
		cfg:         cfg,
		startedAt:   now,
		marketStore: market.NewStore(marketConfig.StaleAfter),
		termsStore:  termsStore,
		ledger:      ledger,
	}
	app.decisionEngine = arbitrage.NewEngine(app.termsStore, app.ledger)
	for _, opt := range opts {
		opt(app)
	}

	ctx, cancel := context.WithCancel(context.Background())
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
	httpServer.RegisterOnShutdown(cancel)

	return httpServer
}

func (s *Server) runDecisionLoop(ctx context.Context) {
	events := s.marketStore.Subscribe(ctx.Done())
	ticker := time.NewTicker(decisionInterval)
	defer ticker.Stop()

	dirty := true
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				return
			}
			dirty = true
		case now := <-ticker.C:
			if !dirty {
				continue
			}
			s.decisionEngine.Evaluate(s.marketStore.Snapshot(), now)
			dirty = false
		}
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /stream", s.stream)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", s.assetHandler()))

	utils.SetupScriptRoutes(mux, s.cfg.Environment.IsDevelopment())

	return mux
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := view.Page(s.pageModel()).Render(r.Context(), w); err != nil {
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
	live := s.liveDashboardView(now)
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
	if err := sse.PatchElementTempl(view.LiveDashboard(live)); err != nil {
		return false
	}

	return true
}

func (s *Server) pageModel() view.PageModel {
	return view.PageModel{
		Title:     "Kyros Arbitrage",
		StartedAt: displayTimestamp(s.startedAt),
		Heartbeat: s.heartbeatView(0, time.Now()),
		Live:      s.liveDashboardView(time.Now()),
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

func (s *Server) liveDashboardView(now time.Time) view.LiveDashboardView {
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

	return view.LiveDashboardView{
		FeedRows:        rows,
		OpportunityRows: opportunities,
		TermsRows:       termsRows,
		BalanceRows:     balanceRows,
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

func displayAssetAmount(asset string, value decimal.Decimal) string {
	switch asset {
	case "BTC":
		return displayBase(value)
	default:
		return displayQuote(value)
	}
}

func displayBPS(value decimal.Decimal) string {
	return fmt.Sprintf("%.2f bps", value)
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

func displayMarket(market exchange.Market) string {
	if market.Base == "" || market.Quote == "" {
		return "BTC/USDT"
	}
	return market.ID()
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
