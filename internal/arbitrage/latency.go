package arbitrage

import (
	"sync"
	"time"

	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

const minLatencySamples = 2

type LatencyModel struct {
	mu      sync.Mutex
	window  time.Duration
	samples map[exchange.Key][]midSample
}

type midSample struct {
	at  time.Time
	mid decimal.Decimal
}

func NewLatencyModel(window time.Duration) *LatencyModel {
	if window <= 0 {
		window = 5 * time.Minute
	}
	return &LatencyModel{
		window:  window,
		samples: make(map[exchange.Key][]midSample),
	}
}

func (m *LatencyModel) Observe(snapshots []exchange.OrderBookSnapshot, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := now.Add(-m.window)
	for _, snapshot := range snapshots {
		if snapshot.Status != exchange.StatusLive || !snapshot.HasBook() {
			continue
		}
		bid, bidOK := snapshot.BestBid()
		ask, askOK := snapshot.BestAsk()
		if !bidOK || !askOK {
			continue
		}
		sum, err := bid.Price.Add(ask.Price)
		if err != nil {
			continue
		}
		mid, err := sum.Quo(decimal.MustNew(2, 0))
		if err != nil || !mid.IsPos() {
			continue
		}
		key := snapshot.Key()
		samples := append(m.samples[key], midSample{at: now, mid: mid})
		first := 0
		for first < len(samples) && samples[first].at.Before(cutoff) {
			first++
		}
		samples = samples[first:]
		if len(samples) > 256 {
			samples = samples[len(samples)-256:]
		}
		m.samples[key] = samples
	}
}

func (m *LatencyModel) PenaltyBPS(left exchange.Key, right exchange.Key) (decimal.Decimal, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	leftBPS, leftOK := movementBPS(m.samples[left])
	rightBPS, rightOK := movementBPS(m.samples[right])
	if !leftOK || !rightOK {
		return decimal.Zero, false
	}
	if leftBPS.Cmp(rightBPS) > 0 {
		return leftBPS, true
	}
	return rightBPS, true
}

func movementBPS(samples []midSample) (decimal.Decimal, bool) {
	if len(samples) < minLatencySamples {
		return decimal.Zero, false
	}
	latest := samples[len(samples)-1].mid
	previous := samples[len(samples)-2].mid
	if !previous.IsPos() {
		return decimal.Zero, false
	}
	diff, err := latest.Sub(previous)
	if err != nil {
		return decimal.Zero, false
	}
	ratio, err := diff.Abs().Quo(previous)
	if err != nil {
		return decimal.Zero, false
	}
	bps, err := ratio.Mul(decimal.MustNew(10_000, 0))
	if err != nil {
		return decimal.Zero, false
	}
	return bps, true
}
