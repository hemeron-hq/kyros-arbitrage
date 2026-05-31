package view

type DashboardSignals struct {
	Connected  bool   `json:"connected"`
	ServerTime string `json:"serverTime"`
	Ticks      int    `json:"ticks"`
	Streaming  bool   `json:"streaming"`
	LiveFeeds  int    `json:"liveFeeds"`
	StaleFeeds int    `json:"staleFeeds"`
}

type PageModel struct {
	Title     string
	StartedAt string
	Heartbeat HeartbeatView
	Live      LiveDashboardView
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

type LiveDashboardView struct {
	FeedRows        []FeedRow
	SpreadRows      []SpreadRow
	LiveFeeds       int
	StaleFeeds      int
	BestSpread      string
	BestSpreadState string
	LastUpdated     string
}

type FeedRow struct {
	Venue       string
	Market      string
	Status      string
	StatusClass string
	Transport   string
	Bid         string
	BidSize     string
	Ask         string
	AskSize     string
	Levels      string
	Age         string
	Latency     string
	Sequence    string
	Message     string
}

type SpreadRow struct {
	Route          string
	Market         string
	GrossSpread    string
	GrossSpreadBPS string
	MaxBaseSize    string
	State          string
}
