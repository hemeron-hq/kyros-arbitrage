package dashboard

import (
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/exchanges"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/historic"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/live"
	riskui "github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/risk"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/speed"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/shared"
)

type Signals struct {
	Connected  bool   `json:"connected"`
	ServerTime string `json:"serverTime"`
	Ticks      int    `json:"ticks"`
	Uptime     string `json:"uptime"`
	Streaming  bool   `json:"streaming"`
	LiveFeeds  int    `json:"liveFeeds"`
	StaleFeeds int    `json:"staleFeeds"`
}

type Model struct {
	Title           string
	StartedAt       string
	Heartbeat       HeartbeatView
	Metrics         shared.Metrics
	Live            live.Model
	Speed           speed.Model
	Risk            riskui.Model
	Exchanges       exchanges.Model
	Historic        historic.Model
	OverviewActive  bool
	ExchangesActive bool
	HistoricActive  bool
}

type HeartbeatView struct {
	Connected   bool
	StatusLabel string
	StatusClass string
	ServerTime  string
	ServerTitle string
	Ticks       int
	TicksLabel  string
	Uptime      string
}
