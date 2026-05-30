package server

import (
	"net/http"
	"strconv"
	"time"

	json "github.com/goccy/go-json"
	"github.com/hemeron-hq/kyros-arbitrage/internal/platform/config"
	"github.com/hemeron-hq/kyros-arbitrage/internal/view"
	"github.com/starfederation/datastar-go/datastar"
	"github.com/templui/templui/utils"
)

const (
	assetsDir         = "assets"
	readHeaderTimeout = 5 * time.Second
)

type Server struct {
	cfg       config.Config
	startedAt time.Time
}

func New(cfg config.Config) *http.Server {
	app := &Server{
		cfg:       cfg,
		startedAt: time.Now(),
	}

	return &http.Server{
		Addr:              cfg.Addr(),
		Handler:           app.routes(),
		ReadHeaderTimeout: readHeaderTimeout,
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
	_ = json.NewEncoder(w).Encode(struct {
		OK bool `json:"ok"`
	}{OK: true})
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	ticks := 0
	if !s.patchHeartbeat(sse, ticks, time.Now()) {
		return
	}

	for {
		select {
		case <-sse.Context().Done():
			return
		case now := <-ticker.C:
			if sse.IsClosed() {
				return
			}
			ticks++
			if !s.patchHeartbeat(sse, ticks, now) {
				return
			}
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

func (s *Server) patchHeartbeat(sse *datastar.ServerSentEventGenerator, ticks int, now time.Time) bool {
	heartbeat := s.heartbeatView(ticks, now)
	signals := view.DashboardSignals{
		Connected:  heartbeat.Connected,
		ServerTime: heartbeat.ServerTime,
		Ticks:      heartbeat.Ticks,
		Streaming:  false,
	}

	if err := sse.MarshalAndPatchSignals(signals); err != nil {
		return false
	}
	if err := sse.PatchElementTempl(view.HeartbeatPanel(heartbeat)); err != nil {
		return false
	}

	return true
}

func (s *Server) pageModel() view.PageModel {
	return view.PageModel{
		Title:     "Kyros Arbitrage",
		StartedAt: s.startedAt.Format(time.RFC3339),
		Heartbeat: s.heartbeatView(0, time.Now()),
		Venues: []view.VenuePlaceholder{
			{Name: "Binance", Pair: "BTC/USDT", Status: "planned", Bid: "-", Ask: "-", Latency: "-"},
			{Name: "Kraken", Pair: "BTC/USDT", Status: "planned", Bid: "-", Ask: "-", Latency: "-"},
			{Name: "Coinbase", Pair: "BTC/USD", Status: "later", Bid: "-", Ask: "-", Latency: "-"},
			{Name: "OKX", Pair: "BTC/USDT", Status: "later", Bid: "-", Ask: "-", Latency: "-"},
		},
		Opportunities: []view.OpportunityRow{
			{Route: "Kraken -> Binance", GrossSpread: "-", NetEstimate: "-", Size: "-", State: "waiting for feeds"},
			{Route: "Binance -> Kraken", GrossSpread: "-", NetEstimate: "-", Size: "-", State: "waiting for feeds"},
		},
	}
}

func (s *Server) heartbeatView(ticks int, now time.Time) view.HeartbeatView {
	return view.HeartbeatView{
		Connected:   true,
		StatusLabel: "Stream connected",
		StatusClass: "bg-emerald-500",
		ServerTime:  now.Format(time.RFC3339),
		Ticks:       ticks,
		TicksLabel:  strconv.Itoa(ticks),
		Uptime:      time.Since(s.startedAt).Round(time.Second).String(),
	}
}
