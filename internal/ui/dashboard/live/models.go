package live

type Model struct {
	FeedRows        []FeedRow
	OpportunityRows []OpportunityRow
	TermsRows       []TermsSourceRow
	BalanceRows     []BalanceRow
	BalanceGroups   []BalanceGroup
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
	AgeP50      string
	AgeP95      string
	UpdateRate  string
	Latency     string
	Sequence    string
	Message     string
}

type OpportunityRow struct {
	Route         string
	Market        string
	Size          string
	GrossPnl      string
	GrossBPS      string
	Fees          string
	Slippage      string
	Latency       string
	Rebalance     string
	CostStack     string
	ExpectedNet   string
	NetBPS        string
	Decision      string
	DecisionClass string
	ReasonLabel   string
	Reason        string
}

type HistoryOpportunityRow struct {
	Observed     string
	ObservedFull string
	Route        string
	Market       string
	Size         string
	ExpectedNet  string
	Decision     string
	Reason       string
}

type HistoryExecutionRow struct {
	Executed     string
	ExecutedFull string
	Route        string
	Market       string
	Size         string
	NetProfit    string
	TermsSource  string
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

type BalanceGroup struct {
	Exchange string
	Assets   []BalanceAssetRow
}

type BalanceAssetRow struct {
	Asset  string
	Amount string
}
