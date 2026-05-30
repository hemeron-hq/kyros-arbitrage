package view

type DashboardSignals struct {
	Connected  bool   `json:"connected"`
	ServerTime string `json:"serverTime"`
	Ticks      int    `json:"ticks"`
	Streaming  bool   `json:"streaming"`
}

type PageModel struct {
	Title         string
	StartedAt     string
	Heartbeat     HeartbeatView
	Venues        []VenuePlaceholder
	Opportunities []OpportunityRow
}

type HeartbeatView struct {
	Connected   bool
	StatusLabel string
	StatusClass string
	ServerTime  string
	Ticks       int
	TicksLabel  string
	Uptime      string
}

type VenuePlaceholder struct {
	Name    string
	Pair    string
	Status  string
	Bid     string
	Ask     string
	Latency string
}

type OpportunityRow struct {
	Route       string
	GrossSpread string
	NetEstimate string
	Size        string
	State       string
}
