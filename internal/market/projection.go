package market

import (
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

type Projection struct {
	Feeds []FeedProjection `json:"feeds"`
}

type FeedProjection struct {
	Venue     exchange.Venue      `json:"venue"`
	Market    string              `json:"market"`
	Status    exchange.FeedStatus `json:"status"`
	Transport exchange.Transport  `json:"transport"`
	Bid       string              `json:"bid"`
	BidSize   string              `json:"bidSize"`
	Ask       string              `json:"ask"`
	AskSize   string              `json:"askSize"`
	Levels    int                 `json:"levels"`
	AgeMS     *int64              `json:"ageMs"`
	LatencyMS *int64              `json:"latencyMs"`
	Sequence  int64               `json:"sequence"`
	Message   string              `json:"message"`
}

func Project(snapshots []exchange.OrderBookSnapshot, now time.Time) Projection {
	feeds := make([]FeedProjection, 0, len(snapshots))
	for _, snapshot := range snapshots {
		feeds = append(feeds, projectFeed(snapshot, now))
	}

	return Projection{
		Feeds: feeds,
	}
}

func projectFeed(snapshot exchange.OrderBookSnapshot, now time.Time) FeedProjection {
	projection := FeedProjection{
		Venue:     snapshot.Venue,
		Market:    snapshot.Market.ID(),
		Status:    snapshot.Status,
		Transport: snapshot.Transport,
		Levels:    min(len(snapshot.Bids), len(snapshot.Asks)),
		Sequence:  snapshot.Sequence,
		Message:   snapshot.Message,
	}

	if bid, ok := snapshot.BestBid(); ok {
		projection.Bid = bid.Price.String()
		projection.BidSize = bid.Quantity.String()
	}
	if ask, ok := snapshot.BestAsk(); ok {
		projection.Ask = ask.Price.String()
		projection.AskSize = ask.Quantity.String()
	}
	if !snapshot.ReceivedAt.IsZero() {
		age := max(now.Sub(snapshot.ReceivedAt).Milliseconds(), int64(0))
		projection.AgeMS = &age
	}
	if snapshot.Latency > 0 {
		latency := snapshot.Latency.Milliseconds()
		projection.LatencyMS = &latency
	}

	return projection
}
