package dashboard

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/historic"
	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/live"
)

const (
	historyCacheTTL    = 3 * time.Second
	historyReadTimeout = 10 * time.Second

	historyStatusPersisted   = "persisted"
	historyStatusSyncDelayed = "sync delayed"
	historyStatusUnavailable = "history unavailable"
)

type historyPageKey struct {
	limit  int64
	offset int64
}

type cachedHistoricPage struct {
	model historic.Model
	at    time.Time
}

type historyCache struct {
	mu sync.Mutex

	reportView live.HistoryView
	reportAt   time.Time

	pages map[historyPageKey]cachedHistoricPage
}

func newHistoryCache() *historyCache {
	return &historyCache{
		pages: make(map[historyPageKey]cachedHistoricPage),
	}
}

func (h *Handler) cachedHistoryView(ctx context.Context, streaming bool) live.HistoryView {
	if h.historyStore == nil {
		return live.HistoryView{
			Path:   h.database.Path(),
			Status: historyStatusUnavailable,
		}
	}

	if streaming {
		if view, ok := h.historyCache.readReport(); ok {
			return view
		}
	}

	reportCtx, cancel := context.WithTimeout(ctx, historyReadTimeout)
	defer cancel()

	report, err := h.historyStore.Report(reportCtx, 8)
	if err != nil {
		return h.historyCache.reportOnError(h.database.Path(), err)
	}

	view := historyViewFromReport(report)
	h.historyCache.storeReport(view)
	return view
}

func (h *Handler) cachedHistoricView(ctx context.Context, limit int64, offset int64, streaming bool) historic.Model {
	if h.historyStore == nil {
		return historic.Model{
			Path:   h.database.Path(),
			Status: historyStatusUnavailable,
		}
	}

	key := historyPageKey{limit: limit, offset: offset}
	if streaming {
		if model, ok := h.historyCache.readPage(key); ok {
			return model
		}
	}

	reportCtx, cancel := context.WithTimeout(ctx, historyReadTimeout)
	defer cancel()

	report, err := h.historyStore.Page(reportCtx, limit, offset)
	if err != nil {
		return h.historyCache.pageOnError(h.database.Path(), key, err)
	}

	model := historicViewFromReport(report, limit, offset)
	h.historyCache.storePage(key, model)
	return model
}

func (c *historyCache) readReport() (live.HistoryView, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reportAt.IsZero() || time.Since(c.reportAt) >= historyCacheTTL {
		return live.HistoryView{}, false
	}
	return c.reportView, true
}

func (c *historyCache) storeReport(view live.HistoryView) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reportView = view
	c.reportAt = time.Now()
}

func (c *historyCache) reportOnError(path string, err error) live.HistoryView {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.reportAt.IsZero() {
		view := c.reportView
		view.Status = historyStatusSyncDelayed
		return view
	}
	return live.HistoryView{
		Path:   path,
		Status: historyStatusUnavailable,
	}
}

func (c *historyCache) readPage(key historyPageKey) (historic.Model, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.pages[key]
	if !ok || time.Since(entry.at) >= historyCacheTTL {
		return historic.Model{}, false
	}
	return entry.model, true
}

func (c *historyCache) storePage(key historyPageKey, model historic.Model) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pages[key] = cachedHistoricPage{
		model: model,
		at:    time.Now(),
	}
}

func (c *historyCache) pageOnError(path string, key historyPageKey, err error) historic.Model {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.pages[key]; ok {
		model := entry.model
		model.Status = historyStatusSyncDelayed
		return model
	}
	_ = err
	return historic.Model{
		Path:   path,
		Status: historyStatusUnavailable,
	}
}

func streamURL(options PageOptions) string {
	query := fmt.Sprintf("tab=%s", options.ActiveTab)
	if options.ActiveTab == "historic" {
		query += fmt.Sprintf("&history_limit=%d&history_offset=%d", options.HistoryLimit, options.HistoryOffset)
	}
	return "/stream?" + query
}
