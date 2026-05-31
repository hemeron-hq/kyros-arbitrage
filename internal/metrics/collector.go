package metrics

import (
	"sort"
	"sync"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
	"github.com/hemeron-hq/kyros-arbitrage/internal/market"
)

const metricsWindow = time.Minute

type Collector struct {
	mu              sync.RWMutex
	feeds           map[exchange.Key]*feedMetrics
	lastDecision    DecisionMetrics
	decisionSamples []int64
}

type feedMetrics struct {
	Exchange    exchange.ID
	Market      string
	agesMS      []int64
	updateTimes []time.Time
	lastSeen    time.Time
	lastLatency *int64
}

type Snapshot struct {
	LastDecisionLatencyMS int64             `json:"lastDecisionLatencyMs"`
	EvaluationDurationMS  int64             `json:"evaluationDurationMs"`
	DecisionP95MS         int64             `json:"decisionP95Ms"`
	Feeds                 []FeedMetrics     `json:"feeds"`
	Markets               []MarketStatusRow `json:"markets"`
}

type DecisionMetrics struct {
	LatencyMS       int64 `json:"latencyMs"`
	EvaluationMS    int64 `json:"evaluationMs"`
	NewestBookAgeMS int64 `json:"newestBookAgeMs"`
}

type FeedMetrics struct {
	Exchange     exchange.ID `json:"exchange"`
	Market       string      `json:"market"`
	FeedAgeP50MS int64       `json:"feedAgeP50Ms"`
	FeedAgeP95MS int64       `json:"feedAgeP95Ms"`
	UpdateRate   float64     `json:"updateRatePerSecond"`
	LastLatency  *int64      `json:"lastLatencyMs,omitempty"`
	LastSeen     time.Time   `json:"lastSeen"`
}

type MarketStatusRow struct {
	Market string `json:"market"`
	Live   int    `json:"live"`
	Stale  int    `json:"stale"`
	Errors int    `json:"errors"`
}

func NewCollector() *Collector {
	return &Collector{
		feeds: make(map[exchange.Key]*feedMetrics),
	}
}

func (c *Collector) ObserveMarket(snapshot exchange.OrderBookSnapshot, now time.Time) {
	if c == nil || !snapshot.HasBook() {
		return
	}
	age := feedAgeMS(snapshot, now)
	var latency *int64
	if snapshot.Latency > 0 {
		value := snapshot.Latency.Milliseconds()
		latency = &value
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	key := snapshot.Key()
	row := c.feeds[key]
	if row == nil {
		row = &feedMetrics{Exchange: snapshot.Exchange, Market: snapshot.Market.ID()}
		c.feeds[key] = row
	}
	row.agesMS = appendLimited(row.agesMS, age, 512)
	row.updateTimes = appendTimeWindow(row.updateTimes, now, now.Add(-metricsWindow))
	row.lastSeen = now
	row.lastLatency = latency
}

func (c *Collector) ObserveDecision(newestBook time.Time, startedAt time.Time, finishedAt time.Time) {
	if c == nil {
		return
	}
	latency := int64(0)
	if !newestBook.IsZero() && finishedAt.After(newestBook) {
		latency = finishedAt.Sub(newestBook).Milliseconds()
	}
	duration := int64(0)
	if finishedAt.After(startedAt) {
		duration = finishedAt.Sub(startedAt).Milliseconds()
	}
	newestAge := int64(0)
	if !newestBook.IsZero() && startedAt.After(newestBook) {
		newestAge = startedAt.Sub(newestBook).Milliseconds()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastDecision = DecisionMetrics{
		LatencyMS:       latency,
		EvaluationMS:    duration,
		NewestBookAgeMS: newestAge,
	}
	c.decisionSamples = appendLimited(c.decisionSamples, latency, 512)
}

func (c *Collector) Snapshot(now time.Time, feeds []market.FeedProjection) Snapshot {
	if c == nil {
		return Snapshot{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	rows := make([]FeedMetrics, 0, len(c.feeds))
	for _, feed := range c.feeds {
		updateTimes := appendTimeWindow(append([]time.Time(nil), feed.updateTimes...), now, now.Add(-metricsWindow))
		rows = append(rows, FeedMetrics{
			Exchange:     feed.Exchange,
			Market:       feed.Market,
			FeedAgeP50MS: percentile(feed.agesMS, 50),
			FeedAgeP95MS: percentile(feed.agesMS, 95),
			UpdateRate:   float64(len(updateTimes)) / metricsWindow.Seconds(),
			LastLatency:  cloneInt64(feed.lastLatency),
			LastSeen:     feed.lastSeen,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Market == rows[j].Market {
			return rows[i].Exchange < rows[j].Exchange
		}
		return rows[i].Market < rows[j].Market
	})

	return Snapshot{
		LastDecisionLatencyMS: c.lastDecision.LatencyMS,
		EvaluationDurationMS:  c.lastDecision.EvaluationMS,
		DecisionP95MS:         percentile(c.decisionSamples, 95),
		Feeds:                 rows,
		Markets:               projectMarketStatuses(feeds),
	}
}

func feedAgeMS(snapshot exchange.OrderBookSnapshot, now time.Time) int64 {
	if !snapshot.ExchangeTime.IsZero() && now.After(snapshot.ExchangeTime) {
		return now.Sub(snapshot.ExchangeTime).Milliseconds()
	}
	if snapshot.Latency > 0 {
		return snapshot.Latency.Milliseconds()
	}
	if !snapshot.ReceivedAt.IsZero() && now.After(snapshot.ReceivedAt) {
		return now.Sub(snapshot.ReceivedAt).Milliseconds()
	}
	return 0
}

func appendLimited(values []int64, value int64, limit int) []int64 {
	values = append(values, value)
	if len(values) > limit {
		return values[len(values)-limit:]
	}
	return values
}

func appendTimeWindow(values []time.Time, value time.Time, cutoff time.Time) []time.Time {
	if !value.IsZero() {
		values = append(values, value)
	}
	first := 0
	for first < len(values) && values[first].Before(cutoff) {
		first++
	}
	return values[first:]
}

func percentile(values []int64, pct int) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := (len(sorted)*pct + 99) / 100
	if index <= 0 {
		index = 1
	}
	if index > len(sorted) {
		index = len(sorted)
	}
	return sorted[index-1]
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func projectMarketStatuses(feeds []market.FeedProjection) []MarketStatusRow {
	byMarket := make(map[string]*MarketStatusRow)
	for _, feed := range feeds {
		row := byMarket[feed.Market]
		if row == nil {
			row = &MarketStatusRow{Market: feed.Market}
			byMarket[feed.Market] = row
		}
		switch feed.Status {
		case exchange.StatusLive:
			row.Live++
		case exchange.StatusStale:
			row.Stale++
		case exchange.StatusError:
			row.Errors++
		}
	}
	rows := make([]MarketStatusRow, 0, len(byMarket))
	for _, row := range byMarket {
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Market < rows[j].Market })
	return rows
}
