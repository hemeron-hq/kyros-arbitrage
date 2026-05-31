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
	OpportunityRows []OpportunityRow
	TermsRows       []TermsSourceRow
	BalanceRows     []BalanceRow
	History         HistoryView
	LiveFeeds       int
	StaleFeeds      int
	BestNetPnl      string
	BestNetState    string
	SessionPnl      string
	Executed        string
	Rejected        string
	LastUpdated     string
}

type HistoryView struct {
	Path             string
	Status           string
	OpportunityCount string
	ExecutionCount   string
	TotalPnl         string
	OpportunityRows  []HistoryOpportunityRow
	ExecutionRows    []HistoryExecutionRow
}

type FeedRow struct {
	Exchange    string
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

type OpportunityRow struct {
	Route       string
	Market      string
	Size        string
	GrossPnl    string
	Fees        string
	Slippage    string
	Latency     string
	Rebalance   string
	ExpectedNet string
	Decision    string
	Reason      string
}

type HistoryOpportunityRow struct {
	Observed    string
	Route       string
	Market      string
	Size        string
	ExpectedNet string
	Decision    string
	Reason      string
}

type HistoryExecutionRow struct {
	Executed    string
	Route       string
	Market      string
	Size        string
	NetProfit   string
	TermsSource string
}

type TermsSourceRow struct {
	Exchange string
	Market   string
	Source   string
	Status   string
	Message  string
	Updated  string
}

type BalanceRow struct {
	Exchange string
	Asset    string
	Amount   string
	Source   string
}
