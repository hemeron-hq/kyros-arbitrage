package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	json "github.com/goccy/go-json"
	"github.com/hemeron-hq/kyros-arbitrage/internal/arbitrage"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange/registry"
	"github.com/hemeron-hq/kyros-arbitrage/internal/history"
	"github.com/hemeron-hq/kyros-arbitrage/internal/market"
	appmetrics "github.com/hemeron-hq/kyros-arbitrage/internal/metrics"
	"github.com/hemeron-hq/kyros-arbitrage/internal/platform/config"
	"github.com/hemeron-hq/kyros-arbitrage/internal/platform/database"
	"github.com/hemeron-hq/kyros-arbitrage/internal/portfolio"
	"github.com/hemeron-hq/kyros-arbitrage/internal/portfolio/paper"
	"github.com/hemeron-hq/kyros-arbitrage/internal/risk"
	"github.com/hemeron-hq/kyros-arbitrage/internal/terms"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard"
	"github.com/templui/templui/utils"
)

const (
	assetsDir         = "assets"
	readHeaderTimeout = 5 * time.Second
)

type Server struct {
	cfg                  config.Config
	startedAt            time.Time
	marketStore          *market.Store
	termsStore           *terms.Store
	ledger               portfolio.Store
	decisionEngine       *arbitrage.Engine
	riskController       *risk.Controller
	metricsCollector     *appmetrics.Collector
	database             *database.Database
	historyStore         *history.Store
	historyRecorder      *historyRecorder
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
	metricsCollector := appmetrics.NewCollector()
	app := &Server{
		cfg:              cfg,
		startedAt:        now,
		marketStore:      market.NewStore(marketConfig.StaleAfter),
		termsStore:       termsStore,
		ledger:           ledger,
		riskController:   riskController,
		metricsCollector: metricsCollector,
		database:         appDatabase,
		historyStore:     historyStore,
		historyRecorder:  newHistoryRecorder(historyStore),
	}
	app.decisionEngine = arbitrage.NewEngine(app.termsStore, app.ledger, app.riskController)
	for _, opt := range opts {
		opt(app)
	}
	app.marketStore.SetObserver(app.metricsCollector)

	exchanges := registry.New(cfg)
	termsService := terms.NewService(
		app.termsStore,
		exchanges.TermsClients,
		exchanges.Bindings,
		terms.DefaultServiceConfig(),
	)
	termsService.Start(ctx)
	if !app.disableMarketService {
		service := market.NewService(
			app.marketStore,
			exchanges.MarketDataProviders,
			exchanges.Bindings,
			marketConfig,
		)
		service.Start(ctx)
	}
	go app.historyRecorder.run(ctx)
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
		snapshots := s.marketStore.Snapshot()
		startedAt := time.Now()
		snapshot := s.decisionEngine.Evaluate(snapshots, now)
		s.historyRecorder.observe(snapshot)
		s.metricsCollector.ObserveDecision(newestBookTime(snapshots), startedAt, time.Now())
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

	dashboard.New(dashboard.Dependencies{
		StartedAt:      s.startedAt,
		MarketStore:    s.marketStore,
		TermsStore:     s.termsStore,
		DecisionEngine: s.decisionEngine,
		RiskController: s.riskController,
		Metrics:        s.metricsCollector,
		Database:       s.database,
		HistoryStore:   s.historyStore,
	}).RegisterRoutes(mux)

	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/history", s.historyAPI)
	mux.HandleFunc("GET /api/metrics", s.metricsAPI)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", s.assetHandler()))

	utils.SetupScriptRoutes(mux, s.cfg.Environment.IsDevelopment())

	return mux
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
	limit, offset := historyPageParams(r)
	report, err := s.historyStore.Page(r.Context(), limit, offset)
	if err != nil {
		http.Error(w, "load history", http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(report)
}

func historyPageParams(r *http.Request) (int64, int64) {
	query := r.URL.Query()
	limit := parseInt64(query.Get("limit"), history.DefaultPageLimit)
	offset := parseInt64(query.Get("offset"), 0)
	return history.NormalizePage(limit, offset)
}

func parseInt64(value string, fallback int64) int64 {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func (s *Server) metricsAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	now := time.Now()
	projection := market.Project(s.marketStore.Snapshot(), now)
	_ = json.NewEncoder(w).Encode(s.metricsCollector.Snapshot(now, projection.Feeds))
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

func newestBookTime(snapshots []exchange.OrderBookSnapshot) time.Time {
	var newest time.Time
	for _, snapshot := range snapshots {
		if snapshot.ReceivedAt.After(newest) {
			newest = snapshot.ReceivedAt
		}
	}
	return newest
}
