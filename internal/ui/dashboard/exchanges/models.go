package exchanges

type Model struct {
	ExchangeCards []ExchangeCardView
	LastUpdated   string
}

type ExchangeCardView struct {
	Exchange    string
	Market      string
	Source      string
	Status      string
	StatusClass string
	Updated     string
	Expires     string
	Message     string
	Rules       []RuleRow
	Balances    []BalanceRow
	Transfers   []TransferRow
}

type RuleRow struct {
	Exchange    string
	Market      string
	MakerFee    string
	TakerFee    string
	MinBase     string
	MinNotional string
	StepSize    string
	TickSize    string
	Source      string
}

type BalanceRow struct {
	Exchange string
	Market   string
	Asset    string
	Amount   string
	Source   string
}

type TransferRow struct {
	Exchange string
	Market   string
	Asset    string
	Fee      string
	Source   string
}
