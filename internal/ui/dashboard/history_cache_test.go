package dashboard

import (
	"testing"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/live"
)

func TestHistoryCacheReadReportUsesTTL(t *testing.T) {
	cache := newHistoryCache()
	view := live.HistoryView{
		Path:           "file:test.db",
		Status:         historyStatusPersisted,
		ExecutionCount: "3",
		TotalPnl:       "+1.00",
	}
	cache.storeReport(view)

	if got, ok := cache.readReport(); !ok || got.ExecutionCount != "3" {
		t.Fatalf("expected cached report, got ok=%v view=%+v", ok, got)
	}

	cache.mu.Lock()
	cache.reportAt = time.Now().Add(-historyCacheTTL)
	cache.mu.Unlock()

	if _, ok := cache.readReport(); ok {
		t.Fatal("expected expired cache entry")
	}
}

func TestHistoryCacheReportOnErrorReturnsStaleData(t *testing.T) {
	cache := newHistoryCache()
	cache.storeReport(live.HistoryView{
		Path:           "file:test.db",
		Status:         historyStatusPersisted,
		ExecutionCount: "5",
		TotalPnl:       "+2.00",
	})

	view := cache.reportOnError("file:test.db", errTestHistoryRead)
	if view.Status != historyStatusSyncDelayed {
		t.Fatalf("expected sync delayed status, got %q", view.Status)
	}
	if view.ExecutionCount != "5" {
		t.Fatalf("expected stale execution count, got %q", view.ExecutionCount)
	}
}

var errTestHistoryRead = &historyReadError{}

type historyReadError struct{}

func (e *historyReadError) Error() string { return "history read failed" }

func TestStreamURLIncludesHistoricPagination(t *testing.T) {
	url := streamURL(PageOptions{
		ActiveTab:     "historic",
		HistoryLimit:  25,
		HistoryOffset: 50,
	})
	want := "/stream?tab=historic&history_limit=25&history_offset=50"
	if url != want {
		t.Fatalf("got %q want %q", url, want)
	}
}
