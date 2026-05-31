package history

import (
	"context"
	"fmt"
	"time"

	"github.com/govalues/decimal"
	db "github.com/hemeron-hq/kyros-arbitrage/gen/db"
	"github.com/hemeron-hq/kyros-arbitrage/internal/arbitrage"
	"github.com/hemeron-hq/kyros-arbitrage/internal/platform/database"
)

const timestampLayout = time.RFC3339Nano
const (
	DefaultPageLimit = int64(25)
	MaxPageLimit     = int64(100)
)

type Store struct {
	database *database.Database
	queries  *db.Queries
}

type Report struct {
	Summary       Summary       `json:"summary"`
	Opportunities []Opportunity `json:"opportunities"`
	Executions    []Execution   `json:"executions"`
	Pagination    Pagination    `json:"pagination"`
}

type Pagination struct {
	Opportunities Page `json:"opportunities"`
	Executions    Page `json:"executions"`
}

type Page struct {
	Total   int64 `json:"total"`
	Limit   int64 `json:"limit"`
	Offset  int64 `json:"offset"`
	HasPrev bool  `json:"hasPrev"`
	HasNext bool  `json:"hasNext"`
}

type Summary struct {
	Path          string `json:"path"`
	Opportunities int64  `json:"opportunities"`
	Executions    int64  `json:"executions"`
	TotalPNL      string `json:"totalPnl"`
}

type Opportunity struct {
	ID                string `json:"id"`
	ObservedAt        string `json:"observedAt"`
	Market            string `json:"market"`
	BuyExchange       string `json:"buyExchange"`
	SellExchange      string `json:"sellExchange"`
	BaseSize          string `json:"baseSize"`
	BuyNotional       string `json:"buyNotional"`
	SellNotional      string `json:"sellNotional"`
	GrossProfit       string `json:"grossProfit"`
	GrossBPS          string `json:"grossBps"`
	BuyFee            string `json:"buyFee"`
	SellFee           string `json:"sellFee"`
	TradingFees       string `json:"tradingFees"`
	TradingFeeBPS     string `json:"tradingFeeBps"`
	SlippageCost      string `json:"slippageCost"`
	SlippageBPS       string `json:"slippageBps"`
	LatencyPenalty    string `json:"latencyPenalty"`
	LatencyPenaltyBPS string `json:"latencyPenaltyBps"`
	RebalanceCost     string `json:"rebalanceCost"`
	ExpectedNetProfit string `json:"expectedNetProfit"`
	ExpectedNetBPS    string `json:"expectedNetBps"`
	Decision          string `json:"decision"`
	ReasonCode        string `json:"reasonCode"`
	TermsSource       string `json:"termsSource"`
	Partial           bool   `json:"partial"`
}

type Execution struct {
	ID             string `json:"id"`
	ExecutedAt     string `json:"executedAt"`
	Market         string `json:"market"`
	BuyExchange    string `json:"buyExchange"`
	SellExchange   string `json:"sellExchange"`
	BaseSize       string `json:"baseSize"`
	BuyNotional    string `json:"buyNotional"`
	SellNotional   string `json:"sellNotional"`
	BuyFee         string `json:"buyFee"`
	SellFee        string `json:"sellFee"`
	LatencyPenalty string `json:"latencyPenalty"`
	RebalanceCost  string `json:"rebalanceCost"`
	NetProfit      string `json:"netProfit"`
	TermsSource    string `json:"termsSource"`
}

func New(database *database.Database) *Store {
	if database == nil {
		return nil
	}
	return &Store{
		database: database,
		queries:  database.Queries(),
	}
}

func (s *Store) RecordSnapshot(ctx context.Context, snapshot arbitrage.Snapshot) error {
	if s == nil {
		return nil
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin history transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	queries := s.queries.WithTx(tx)
	for _, opportunity := range snapshot.Opportunities {
		if opportunity.ID == "" {
			continue
		}
		if err := queries.InsertOpportunity(ctx, insertOpportunityParams(opportunity, snapshot.LastUpdated)); err != nil {
			return fmt.Errorf("insert opportunity: %w", err)
		}
		if opportunity.Decision == arbitrage.DecisionExecute && opportunity.ReasonCode == arbitrage.ReasonExecuted {
			if err := queries.InsertExecution(ctx, insertExecutionParams(opportunity, snapshot.LastUpdated)); err != nil {
				return fmt.Errorf("insert execution: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit history transaction: %w", err)
	}
	committed = true
	return nil
}

func (s *Store) Report(ctx context.Context, limit int64) (Report, error) {
	if s == nil {
		return Report{}, nil
	}
	if limit <= 0 {
		limit = 12
	}

	summary, err := s.Summary(ctx)
	if err != nil {
		return Report{}, err
	}
	opportunities, err := s.RecentOpportunities(ctx, limit)
	if err != nil {
		return Report{}, err
	}
	executions, err := s.RecentExecutions(ctx, limit)
	if err != nil {
		return Report{}, err
	}
	return Report{
		Summary:       summary,
		Opportunities: opportunities,
		Executions:    executions,
		Pagination: Pagination{
			Opportunities: page(summary.Opportunities, limit, 0),
			Executions:    page(summary.Executions, limit, 0),
		},
	}, nil
}

func (s *Store) Page(ctx context.Context, limit int64, offset int64) (Report, error) {
	if s == nil {
		return Report{}, nil
	}
	limit, offset = NormalizePage(limit, offset)

	summary, err := s.Summary(ctx)
	if err != nil {
		return Report{}, err
	}
	opportunities, err := s.OpportunitiesPage(ctx, limit, offset)
	if err != nil {
		return Report{}, err
	}
	executions, err := s.ExecutionsPage(ctx, limit, offset)
	if err != nil {
		return Report{}, err
	}
	return Report{
		Summary:       summary,
		Opportunities: opportunities,
		Executions:    executions,
		Pagination: Pagination{
			Opportunities: page(summary.Opportunities, limit, offset),
			Executions:    page(summary.Executions, limit, offset),
		},
	}, nil
}

func (s *Store) Summary(ctx context.Context) (Summary, error) {
	opportunities, err := s.queries.CountOpportunities(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("count opportunities: %w", err)
	}
	executions, err := s.queries.CountExecutions(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("count executions: %w", err)
	}
	totalPNL, err := s.totalPNL(ctx)
	if err != nil {
		return Summary{}, err
	}
	return Summary{
		Path:          s.database.Path(),
		Opportunities: opportunities,
		Executions:    executions,
		TotalPNL:      totalPNL.String(),
	}, nil
}

func (s *Store) RecentOpportunities(ctx context.Context, limit int64) ([]Opportunity, error) {
	rows, err := s.queries.ListRecentOpportunities(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent opportunities: %w", err)
	}
	opportunities := make([]Opportunity, 0, len(rows))
	for _, row := range rows {
		opportunities = append(opportunities, opportunityFromRecentRow(row))
	}
	return opportunities, nil
}

func (s *Store) OpportunitiesPage(ctx context.Context, limit int64, offset int64) ([]Opportunity, error) {
	limit, offset = NormalizePage(limit, offset)
	rows, err := s.queries.ListOpportunitiesPage(ctx, db.ListOpportunitiesPageParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list opportunities page: %w", err)
	}
	opportunities := make([]Opportunity, 0, len(rows))
	for _, row := range rows {
		opportunities = append(opportunities, opportunityFromPageRow(row))
	}
	return opportunities, nil
}

func (s *Store) RecentExecutions(ctx context.Context, limit int64) ([]Execution, error) {
	rows, err := s.queries.ListRecentExecutions(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent executions: %w", err)
	}
	executions := make([]Execution, 0, len(rows))
	for _, row := range rows {
		executions = append(executions, Execution{
			ID:             row.OpportunityID,
			ExecutedAt:     row.ExecutedAt,
			Market:         row.Market,
			BuyExchange:    row.BuyExchange,
			SellExchange:   row.SellExchange,
			BaseSize:       row.BaseSize,
			BuyNotional:    row.BuyNotional,
			SellNotional:   row.SellNotional,
			BuyFee:         row.BuyFee,
			SellFee:        row.SellFee,
			LatencyPenalty: row.LatencyPenalty,
			RebalanceCost:  row.RebalanceCost,
			NetProfit:      row.NetProfit,
			TermsSource:    row.TermsSource,
		})
	}
	return executions, nil
}

func (s *Store) ExecutionsPage(ctx context.Context, limit int64, offset int64) ([]Execution, error) {
	limit, offset = NormalizePage(limit, offset)
	rows, err := s.queries.ListExecutionsPage(ctx, db.ListExecutionsPageParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list executions page: %w", err)
	}
	executions := make([]Execution, 0, len(rows))
	for _, row := range rows {
		executions = append(executions, executionFromPageRow(row))
	}
	return executions, nil
}

func opportunityFromRecentRow(row db.ListRecentOpportunitiesRow) Opportunity {
	return Opportunity{
		ID:                row.OpportunityID,
		ObservedAt:        row.ObservedAt,
		Market:            row.Market,
		BuyExchange:       row.BuyExchange,
		SellExchange:      row.SellExchange,
		BaseSize:          row.BaseSize,
		BuyNotional:       row.BuyNotional,
		SellNotional:      row.SellNotional,
		GrossProfit:       row.GrossProfit,
		GrossBPS:          row.GrossBps,
		BuyFee:            row.BuyFee,
		SellFee:           row.SellFee,
		TradingFees:       row.TradingFees,
		TradingFeeBPS:     row.TradingFeeBps,
		SlippageCost:      row.SlippageCost,
		SlippageBPS:       row.SlippageBps,
		LatencyPenalty:    row.LatencyPenalty,
		LatencyPenaltyBPS: row.LatencyPenaltyBps,
		RebalanceCost:     row.RebalanceCost,
		ExpectedNetProfit: row.ExpectedNetProfit,
		ExpectedNetBPS:    row.ExpectedNetBps,
		Decision:          row.Decision,
		ReasonCode:        row.ReasonCode,
		TermsSource:       row.TermsSource,
		Partial:           row.Partial != 0,
	}
}

func opportunityFromPageRow(row db.ListOpportunitiesPageRow) Opportunity {
	return Opportunity{
		ID:                row.OpportunityID,
		ObservedAt:        row.ObservedAt,
		Market:            row.Market,
		BuyExchange:       row.BuyExchange,
		SellExchange:      row.SellExchange,
		BaseSize:          row.BaseSize,
		BuyNotional:       row.BuyNotional,
		SellNotional:      row.SellNotional,
		GrossProfit:       row.GrossProfit,
		GrossBPS:          row.GrossBps,
		BuyFee:            row.BuyFee,
		SellFee:           row.SellFee,
		TradingFees:       row.TradingFees,
		TradingFeeBPS:     row.TradingFeeBps,
		SlippageCost:      row.SlippageCost,
		SlippageBPS:       row.SlippageBps,
		LatencyPenalty:    row.LatencyPenalty,
		LatencyPenaltyBPS: row.LatencyPenaltyBps,
		RebalanceCost:     row.RebalanceCost,
		ExpectedNetProfit: row.ExpectedNetProfit,
		ExpectedNetBPS:    row.ExpectedNetBps,
		Decision:          row.Decision,
		ReasonCode:        row.ReasonCode,
		TermsSource:       row.TermsSource,
		Partial:           row.Partial != 0,
	}
}

func executionFromPageRow(row db.ListExecutionsPageRow) Execution {
	return Execution{
		ID:             row.OpportunityID,
		ExecutedAt:     row.ExecutedAt,
		Market:         row.Market,
		BuyExchange:    row.BuyExchange,
		SellExchange:   row.SellExchange,
		BaseSize:       row.BaseSize,
		BuyNotional:    row.BuyNotional,
		SellNotional:   row.SellNotional,
		BuyFee:         row.BuyFee,
		SellFee:        row.SellFee,
		LatencyPenalty: row.LatencyPenalty,
		RebalanceCost:  row.RebalanceCost,
		NetProfit:      row.NetProfit,
		TermsSource:    row.TermsSource,
	}
}

func NormalizePage(limit int64, offset int64) (int64, int64) {
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	if limit > MaxPageLimit {
		limit = MaxPageLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func page(total int64, limit int64, offset int64) Page {
	limit, offset = NormalizePage(limit, offset)
	return Page{
		Total:   total,
		Limit:   limit,
		Offset:  offset,
		HasPrev: offset > 0,
		HasNext: offset+limit < total,
	}
}

func (s *Store) totalPNL(ctx context.Context) (decimal.Decimal, error) {
	values, err := s.queries.ListExecutionNetProfits(ctx)
	if err != nil {
		return decimal.Zero, fmt.Errorf("list execution net profits: %w", err)
	}
	total := decimal.Zero
	for _, value := range values {
		parsed, err := decimal.Parse(value)
		if err != nil {
			return decimal.Zero, fmt.Errorf("parse execution net profit %q: %w", value, err)
		}
		total, err = total.Add(parsed)
		if err != nil {
			return decimal.Zero, fmt.Errorf("sum execution net profits: %w", err)
		}
	}
	return total, nil
}

func insertOpportunityParams(opportunity arbitrage.Opportunity, fallbackTime time.Time) db.InsertOpportunityParams {
	observedAt := opportunity.CreatedAt
	if observedAt.IsZero() {
		observedAt = fallbackTime
	}
	return db.InsertOpportunityParams{
		OpportunityID:     opportunity.ID,
		ObservedAt:        formatTime(observedAt),
		Market:            marketID(opportunity),
		BuyExchange:       string(opportunity.BuyExchange),
		SellExchange:      string(opportunity.SellExchange),
		BaseSize:          opportunity.BaseSize.String(),
		BuyNotional:       opportunity.BuyNotional.String(),
		SellNotional:      opportunity.SellNotional.String(),
		GrossProfit:       opportunity.GrossProfit.String(),
		GrossBps:          opportunity.GrossBPS.String(),
		BuyFee:            opportunity.BuyFee.String(),
		SellFee:           opportunity.SellFee.String(),
		TradingFees:       opportunity.TradingFees.String(),
		TradingFeeBps:     opportunity.TradingFeeBPS.String(),
		SlippageCost:      opportunity.SlippageCost.String(),
		SlippageBps:       opportunity.SlippageBPS.String(),
		LatencyPenalty:    opportunity.LatencyPenalty.String(),
		LatencyPenaltyBps: opportunity.LatencyPenaltyBPS.String(),
		RebalanceCost:     opportunity.RebalanceCost.String(),
		ExpectedNetProfit: opportunity.ExpectedNetProfit.String(),
		ExpectedNetBps:    opportunity.ExpectedNetBPS.String(),
		Decision:          string(opportunity.Decision),
		ReasonCode:        opportunity.ReasonCode,
		TermsSource:       string(opportunity.TermsSource),
		Partial:           boolInt(opportunity.Partial),
	}
}

func insertExecutionParams(opportunity arbitrage.Opportunity, executedAt time.Time) db.InsertExecutionParams {
	return db.InsertExecutionParams{
		OpportunityID:  opportunity.ID,
		ExecutedAt:     formatTime(executedAt),
		Market:         marketID(opportunity),
		BuyExchange:    string(opportunity.BuyExchange),
		SellExchange:   string(opportunity.SellExchange),
		BaseSize:       opportunity.BaseSize.String(),
		BuyNotional:    opportunity.BuyNotional.String(),
		SellNotional:   opportunity.SellNotional.String(),
		BuyFee:         opportunity.BuyFee.String(),
		SellFee:        opportunity.SellFee.String(),
		LatencyPenalty: opportunity.LatencyPenalty.String(),
		RebalanceCost:  opportunity.RebalanceCost.String(),
		NetProfit:      opportunity.ExpectedNetProfit.String(),
		TermsSource:    string(opportunity.TermsSource),
	}
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(timestampLayout)
}

func marketID(opportunity arbitrage.Opportunity) string {
	if opportunity.Market.Base == "" || opportunity.Market.Quote == "" {
		return ""
	}
	return opportunity.Market.ID()
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
