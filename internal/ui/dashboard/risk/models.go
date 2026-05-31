package riskui

type Model struct {
	Mode               string
	Status             string
	StatusClass        string
	Reasons            []string
	CircuitState       string
	CircuitReason      string
	HaltedAt           string
	ResetVisible       bool
	MaxSpread          string
	MaxLatencyPenalty  string
	MaxDrawdown        string
	Reserve            string
	ConservativeActive bool
	BalancedActive     bool
	AggressiveActive   bool
}
