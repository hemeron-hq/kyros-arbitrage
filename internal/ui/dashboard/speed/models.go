package speed

type Model struct {
	LastDecisionLatency string
	EvaluationDuration  string
	DecisionP95         string
	FeedRows            []FeedRow
	MarketRows          []MarketRow
}

type FeedRow struct {
	Exchange   string
	Market     string
	AgeP50     string
	AgeP95     string
	UpdateRate string
	Latency    string
	LastSeen   string
}

type MarketRow struct {
	Market string
	Live   string
	Stale  string
	Errors string
}
