package historic

type Model struct {
	Status           string
	Path             string
	OpportunityCount string
	ExecutionCount   string
	TotalPnl         string
	Page             PageView
	OpportunityRows  []OpportunityRow
	ExecutionRows    []ExecutionRow
}

type PageView struct {
	Label   string
	PrevURL string
	NextURL string
	HasPrev bool
	HasNext bool
}

type OpportunityRow struct {
	Observed          string
	ObservedFull      string
	Route             string
	Style             string
	Market            string
	Size              string
	BuyNotional       string
	SellNotional      string
	GrossProfit       string
	GrossBPS          string
	TradingFees       string
	SlippageCost      string
	LatencyPenalty    string
	RebalanceCost     string
	RebalanceExposure string
	FeeHurdleBPS      string
	EdgeAfterFeesBPS  string
	MissingBPS        string
	ExpectedNetProfit string
	ExpectedNetBPS    string
	Decision          string
	Reason            string
	Partial           string
}

type ExecutionRow struct {
	Executed          string
	ExecutedFull      string
	Route             string
	Style             string
	Market            string
	Size              string
	BuyNotional       string
	SellNotional      string
	BuyFee            string
	SellFee           string
	LatencyPenalty    string
	RebalanceCost     string
	RebalanceExposure string
	NetProfit         string
	TermsSource       string
}
