package server

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/arbitrage"
)

const (
	defaultHistorySampleInterval = time.Second
	defaultHistorySampleLimit    = 10
)

type historyPersistence interface {
	RecordOpportunities(context.Context, []arbitrage.Opportunity, time.Time) error
	RecordExecutions(context.Context, []arbitrage.Opportunity, time.Time) error
}

type historyRecorder struct {
	store          historyPersistence
	sampleInterval time.Duration
	sampleLimit    int
	signal         chan struct{}

	mu            sync.Mutex
	executions    []historyBatch
	sample        *historyBatch
	lastSampledAt time.Time
}

type historyBatch struct {
	opportunities []arbitrage.Opportunity
	observedAt    time.Time
}

func newHistoryRecorder(store historyPersistence) *historyRecorder {
	return &historyRecorder{
		store:          store,
		sampleInterval: defaultHistorySampleInterval,
		sampleLimit:    defaultHistorySampleLimit,
		signal:         make(chan struct{}, 1),
	}
}

func (r *historyRecorder) run(ctx context.Context) {
	if r == nil || r.store == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.signal:
			r.flush(ctx)
		}
	}
}

func (r *historyRecorder) observe(snapshot arbitrage.Snapshot) {
	if r == nil {
		return
	}
	observedAt := snapshot.LastUpdated
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	executions := executedOpportunities(snapshot.Opportunities)
	sample, ok := r.sampleOpportunities(snapshot.Opportunities, observedAt)

	r.mu.Lock()
	if len(executions) > 0 {
		r.executions = append(r.executions, historyBatch{
			opportunities: executions,
			observedAt:    observedAt,
		})
	}
	if ok {
		r.sample = &historyBatch{
			opportunities: sample,
			observedAt:    observedAt,
		}
		r.lastSampledAt = observedAt
	}
	shouldSignal := len(executions) > 0 || ok
	r.mu.Unlock()

	if shouldSignal {
		r.notify()
	}
}

func (r *historyRecorder) sampleOpportunities(opportunities []arbitrage.Opportunity, observedAt time.Time) ([]arbitrage.Opportunity, bool) {
	if r.sampleLimit <= 0 || len(opportunities) == 0 {
		return nil, false
	}
	r.mu.Lock()
	lastSampledAt := r.lastSampledAt
	r.mu.Unlock()
	if !lastSampledAt.IsZero() && observedAt.Sub(lastSampledAt) < r.sampleInterval {
		return nil, false
	}
	sample := make([]arbitrage.Opportunity, 0, min(r.sampleLimit, len(opportunities)))
	for _, opportunity := range opportunities {
		if opportunity.ID == "" || opportunity.BuyExchange == "" || opportunity.SellExchange == "" || isExecution(opportunity) {
			continue
		}
		sample = append(sample, opportunity)
	}
	if len(sample) == 0 {
		return nil, false
	}
	sort.Slice(sample, func(i, j int) bool {
		left := sample[i]
		right := sample[j]
		if left.Rank != 0 && right.Rank != 0 && left.Rank != right.Rank {
			return left.Rank < right.Rank
		}
		if !left.ExpectedNetProfit.Equal(right.ExpectedNetProfit) {
			return left.ExpectedNetProfit.Cmp(right.ExpectedNetProfit) > 0
		}
		return left.GrossBPS.Cmp(right.GrossBPS) > 0
	})
	if len(sample) > r.sampleLimit {
		sample = sample[:r.sampleLimit]
	}
	return sample, true
}

func (r *historyRecorder) flush(ctx context.Context) {
	for {
		executions, sample := r.drain()
		if len(executions) == 0 && sample == nil {
			return
		}
		for _, batch := range executions {
			_ = r.store.RecordExecutions(ctx, batch.opportunities, batch.observedAt)
		}
		if sample != nil {
			_ = r.store.RecordOpportunities(ctx, sample.opportunities, sample.observedAt)
		}
	}
}

func (r *historyRecorder) drain() ([]historyBatch, *historyBatch) {
	r.mu.Lock()
	defer r.mu.Unlock()
	executions := r.executions
	sample := r.sample
	r.executions = nil
	r.sample = nil
	return executions, sample
}

func (r *historyRecorder) notify() {
	select {
	case r.signal <- struct{}{}:
	default:
	}
}

func executedOpportunities(opportunities []arbitrage.Opportunity) []arbitrage.Opportunity {
	executions := make([]arbitrage.Opportunity, 0, 1)
	for _, opportunity := range opportunities {
		if isExecution(opportunity) {
			executions = append(executions, opportunity)
		}
	}
	return executions
}

func isExecution(opportunity arbitrage.Opportunity) bool {
	return opportunity.Decision == arbitrage.DecisionExecute && opportunity.ReasonCode == arbitrage.ReasonExecuted
}
