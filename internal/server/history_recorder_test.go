package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/arbitrage"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

func TestHistoryRecorderPersistsExecutionsImmediately(t *testing.T) {
	store := newFakeHistoryStore()
	recorder := newHistoryRecorder(store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go recorder.run(ctx)

	recorder.observe(arbitrage.Snapshot{
		Opportunities: []arbitrage.Opportunity{testRecorderOpportunity("execution", 1, arbitrage.DecisionExecute, arbitrage.ReasonExecuted)},
		LastUpdated:   time.Now(),
	})

	eventually(t, func() bool {
		return store.executionCount() == 1
	})
	if store.opportunityCount() != 0 {
		t.Fatalf("expected execution path to avoid sampled opportunity writes, got %d", store.opportunityCount())
	}
}

func TestHistoryRecorderSamplesNoMoreThanOncePerSecond(t *testing.T) {
	recorder := newHistoryRecorder(newFakeHistoryStore())
	start := time.Unix(1_800_000_000, 0)

	recorder.observe(arbitrage.Snapshot{
		Opportunities: []arbitrage.Opportunity{testRecorderOpportunity("first", 1, arbitrage.DecisionSkip, arbitrage.ReasonNegativeNet)},
		LastUpdated:   start,
	})
	recorder.observe(arbitrage.Snapshot{
		Opportunities: []arbitrage.Opportunity{testRecorderOpportunity("second", 1, arbitrage.DecisionSkip, arbitrage.ReasonNegativeNet)},
		LastUpdated:   start.Add(500 * time.Millisecond),
	})

	_, sample := recorder.drain()
	if sample == nil {
		t.Fatal("expected sampled opportunities")
	}
	if got := sample.opportunities[0].ID; got != "first" {
		t.Fatalf("expected first sample to survive interval gate, got %s", got)
	}
}

func TestHistoryRecorderPersistsOnlyTopTenSampledOpportunities(t *testing.T) {
	recorder := newHistoryRecorder(newFakeHistoryStore())
	opportunities := make([]arbitrage.Opportunity, 0, 12)
	for i := 12; i >= 1; i-- {
		opportunities = append(opportunities, testRecorderOpportunity(string(rune('a'+i)), i, arbitrage.DecisionSkip, arbitrage.ReasonNegativeNet))
	}

	recorder.observe(arbitrage.Snapshot{
		Opportunities: opportunities,
		LastUpdated:   time.Unix(1_800_000_000, 0),
	})

	_, sample := recorder.drain()
	if sample == nil {
		t.Fatal("expected sampled opportunities")
	}
	if len(sample.opportunities) != 10 {
		t.Fatalf("expected 10 sampled opportunities, got %d", len(sample.opportunities))
	}
	for i, opportunity := range sample.opportunities {
		wantRank := i + 1
		if opportunity.Rank != wantRank {
			t.Fatalf("expected rank %d at index %d, got %d", wantRank, i, opportunity.Rank)
		}
	}
}

func TestHistoryRecorderDropsOldestNonExecutionSampleUnderBackpressure(t *testing.T) {
	recorder := newHistoryRecorder(newFakeHistoryStore())
	start := time.Unix(1_800_000_000, 0)

	recorder.observe(arbitrage.Snapshot{
		Opportunities: []arbitrage.Opportunity{testRecorderOpportunity("old-sample", 1, arbitrage.DecisionSkip, arbitrage.ReasonNegativeNet)},
		LastUpdated:   start,
	})
	recorder.observe(arbitrage.Snapshot{
		Opportunities: []arbitrage.Opportunity{testRecorderOpportunity("new-sample", 1, arbitrage.DecisionSkip, arbitrage.ReasonNegativeNet)},
		LastUpdated:   start.Add(time.Second),
	})

	_, sample := recorder.drain()
	if sample == nil {
		t.Fatal("expected sampled opportunities")
	}
	if got := sample.opportunities[0].ID; got != "new-sample" {
		t.Fatalf("expected newest sample after backpressure drop, got %s", got)
	}
}

func TestHistoryRecorderNeverDropsExecutionsUnderBackpressure(t *testing.T) {
	recorder := newHistoryRecorder(newFakeHistoryStore())
	start := time.Unix(1_800_000_000, 0)

	for i := range 25 {
		recorder.observe(arbitrage.Snapshot{
			Opportunities: []arbitrage.Opportunity{
				testRecorderOpportunity("execution-"+string(rune('a'+i)), i+1, arbitrage.DecisionExecute, arbitrage.ReasonExecuted),
				testRecorderOpportunity("sample-"+string(rune('a'+i)), i+1, arbitrage.DecisionSkip, arbitrage.ReasonNegativeNet),
			},
			LastUpdated: start.Add(time.Duration(i) * time.Second),
		})
	}

	executions, sample := recorder.drain()
	if len(executions) != 25 {
		t.Fatalf("expected 25 execution batches, got %d", len(executions))
	}
	if sample == nil || sample.opportunities[0].ID != "sample-y" {
		t.Fatalf("expected latest non-execution sample to be retained, got %#v", sample)
	}
}

type fakeHistoryStore struct {
	mu            sync.Mutex
	opportunities []arbitrage.Opportunity
	executions    []arbitrage.Opportunity
}

func newFakeHistoryStore() *fakeHistoryStore {
	return &fakeHistoryStore{}
}

func (s *fakeHistoryStore) RecordOpportunities(_ context.Context, opportunities []arbitrage.Opportunity, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opportunities = append(s.opportunities, opportunities...)
	return nil
}

func (s *fakeHistoryStore) RecordExecutions(_ context.Context, opportunities []arbitrage.Opportunity, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executions = append(s.executions, opportunities...)
	return nil
}

func (s *fakeHistoryStore) opportunityCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.opportunities)
}

func (s *fakeHistoryStore) executionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.executions)
}

func testRecorderOpportunity(id string, rank int, decision arbitrage.Decision, reason string) arbitrage.Opportunity {
	return arbitrage.Opportunity{
		ID:                id,
		Rank:              rank,
		BuyExchange:       exchange.Binance,
		SellExchange:      exchange.Kraken,
		ExpectedNetProfit: decimal.MustNew(int64(100-rank), 0),
		GrossBPS:          decimal.MustNew(int64(100-rank), 0),
		Decision:          decision,
		ReasonCode:        reason,
	}
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}
