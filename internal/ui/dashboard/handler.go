package dashboard

import (
	"net/http"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/arbitrage"
	"github.com/hemeron-hq/kyros-arbitrage/internal/history"
	"github.com/hemeron-hq/kyros-arbitrage/internal/market"
	appmetrics "github.com/hemeron-hq/kyros-arbitrage/internal/metrics"
	"github.com/hemeron-hq/kyros-arbitrage/internal/platform/database"
	"github.com/hemeron-hq/kyros-arbitrage/internal/risk"
	"github.com/hemeron-hq/kyros-arbitrage/internal/terms"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/exchanges"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/live"
	riskui "github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/risk"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/speed"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/shared"
	"github.com/starfederation/datastar-go/datastar"
)

const uiPatchInterval = 250 * time.Millisecond

type Dependencies struct {
	StartedAt      time.Time
	MarketStore    *market.Store
	TermsStore     *terms.Store
	DecisionEngine *arbitrage.Engine
	RiskController *risk.Controller
	Metrics        *appmetrics.Collector
	Database       *database.Database
	HistoryStore   *history.Store
}

type Handler struct {
	startedAt      time.Time
	marketStore    *market.Store
	termsStore     *terms.Store
	decisionEngine *arbitrage.Engine
	riskController *risk.Controller
	metrics        *appmetrics.Collector
	database       *database.Database
	historyStore   *history.Store
}

func New(deps Dependencies) *Handler {
	return &Handler{
		startedAt:      deps.StartedAt,
		marketStore:    deps.MarketStore,
		termsStore:     deps.TermsStore,
		decisionEngine: deps.DecisionEngine,
		riskController: deps.RiskController,
		metrics:        deps.Metrics,
		database:       deps.Database,
		historyStore:   deps.HistoryStore,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("GET /stream", h.stream)
	riskui.NewHandler(h.riskController).RegisterRoutes(mux)
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := Page(h.model(r.Context(), 0, time.Now())).Render(r.Context(), w); err != nil {
		http.Error(w, "render page", http.StatusInternalServerError)
	}
}

func (h *Handler) stream(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	ticker := time.NewTicker(uiPatchInterval)
	defer ticker.Stop()

	ticks := 0
	if !h.patchDashboard(sse, ticks, time.Now()) {
		return
	}

	events := h.marketStore.Subscribe(sse.Context().Done())
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
			if !h.patchDashboard(sse, ticks, now) {
				return
			}
			dirty = false
		}
	}
}

func (h *Handler) patchDashboard(sse *datastar.ServerSentEventGenerator, ticks int, now time.Time) bool {
	model := h.model(sse.Context(), ticks, now)
	signals := Signals{
		Connected:  model.Heartbeat.Connected,
		ServerTime: model.Heartbeat.ServerTime,
		Ticks:      model.Heartbeat.Ticks,
		Streaming:  false,
		LiveFeeds:  model.Live.LiveFeeds,
		StaleFeeds: model.Live.StaleFeeds,
	}

	if err := sse.MarshalAndPatchSignals(signals); err != nil {
		return false
	}
	if err := sse.PatchElementTempl(shared.MetricStrip(model.Metrics)); err != nil {
		return false
	}
	if err := sse.PatchElementTempl(HeartbeatPanel(model.Heartbeat)); err != nil {
		return false
	}
	if err := sse.PatchElementTempl(speed.Panel(model.Speed)); err != nil {
		return false
	}
	if err := sse.PatchElementTempl(riskui.Panel(model.Risk)); err != nil {
		return false
	}
	if err := sse.PatchElementTempl(live.Dashboard(model.Live)); err != nil {
		return false
	}
	if err := sse.PatchElementTempl(exchanges.Dashboard(model.Exchanges)); err != nil {
		return false
	}

	return true
}
