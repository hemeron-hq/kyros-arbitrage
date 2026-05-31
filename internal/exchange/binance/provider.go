package binance

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	json "github.com/goccy/go-json"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

const (
	defaultRESTBaseURL = "https://api.binance.com"
	defaultWSBaseURL   = "wss://stream.binance.com:9443"
)

type Provider struct {
	HTTPClient  *http.Client
	RESTBaseURL string
	WSBaseURL   string
	Now         func() time.Time
}

var _ exchange.MarketDataProvider = (*Provider)(nil)

func New() *Provider {
	provider := new(Provider)
	provider.HTTPClient = http.DefaultClient
	provider.RESTBaseURL = defaultRESTBaseURL
	provider.WSBaseURL = defaultWSBaseURL
	provider.Now = time.Now
	return provider
}

func (p *Provider) Venue() exchange.Venue {
	return exchange.VenueBinance
}

func (p *Provider) Stream(ctx context.Context, binding exchange.Binding, depth int, out chan<- exchange.OrderBookSnapshot) error {
	streamURL := strings.TrimRight(p.wsBaseURL(), "/") + "/ws/" + strings.ToLower(binding.WebSocketSymbol) + "@depth" + strconv.Itoa(depth) + "@100ms"
	conn, _, err := websocket.Dial(ctx, streamURL, nil)
	if err != nil {
		return fmt.Errorf("dial binance websocket: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)

	var lastSequence int64
	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return err
		}

		snapshot, err := ParseDepth(payload, binding, depth, exchange.TransportWebSocket, p.now(), 0)
		if err != nil {
			return err
		}
		if snapshot.Sequence < lastSequence {
			return fmt.Errorf("binance sequence moved backwards: previous=%d current=%d", lastSequence, snapshot.Sequence)
		}
		if snapshot.Sequence == lastSequence {
			continue
		}
		lastSequence = snapshot.Sequence

		out <- snapshot
	}
}

func (p *Provider) Poll(ctx context.Context, binding exchange.Binding, depth int) (exchange.OrderBookSnapshot, error) {
	startedAt := p.now()
	endpoint, err := url.Parse(strings.TrimRight(p.restBaseURL(), "/") + "/api/v3/depth")
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}

	query := endpoint.Query()
	query.Set("symbol", binding.RESTSymbol)
	query.Set("limit", strconv.Itoa(depth))
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}

	resp, err := p.httpClient().Do(req)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return exchange.OrderBookSnapshot{}, fmt.Errorf("binance depth status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}

	return ParseDepth(payload, binding, depth, exchange.TransportPolling, p.now(), p.now().Sub(startedAt))
}

func ParseDepth(payload []byte, binding exchange.Binding, depth int, transport exchange.Transport, receivedAt time.Time, latency time.Duration) (exchange.OrderBookSnapshot, error) {
	var message depthMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("decode binance depth: %w", err)
	}

	bids, err := parseLevels(message.Bids, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("decode binance bids: %w", err)
	}
	asks, err := parseLevels(message.Asks, depth)
	if err != nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("decode binance asks: %w", err)
	}
	if len(bids) == 0 || len(asks) == 0 {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("binance depth has empty side")
	}

	return exchange.OrderBookSnapshot{
		Venue:      exchange.VenueBinance,
		Market:     binding.Market,
		Bids:       bids,
		Asks:       asks,
		ReceivedAt: receivedAt,
		Latency:    latency,
		Sequence:   message.LastUpdateID,
		Transport:  transport,
		Status:     exchange.StatusLive,
		Message:    "depth10 live",
	}, nil
}

type depthMessage struct {
	LastUpdateID int64      `json:"lastUpdateId"`
	Bids         [][]string `json:"bids"`
	Asks         [][]string `json:"asks"`
}

func parseLevels(rawLevels [][]string, depth int) ([]exchange.PriceLevel, error) {
	depth = min(max(depth, 0), len(rawLevels))

	levels := make([]exchange.PriceLevel, 0, depth)
	for i := 0; i < depth; i++ {
		if len(rawLevels[i]) < 2 {
			return nil, fmt.Errorf("level %d has %d fields", i, len(rawLevels[i]))
		}

		level, err := exchange.NewPriceLevel(rawLevels[i][0], rawLevels[i][1])
		if err != nil {
			return nil, err
		}
		if level.Quantity.IsZero() {
			continue
		}
		levels = append(levels, level)
	}

	return levels, nil
}

func (p *Provider) httpClient() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return http.DefaultClient
}

func (p *Provider) restBaseURL() string {
	if p.RESTBaseURL != "" {
		return p.RESTBaseURL
	}
	return defaultRESTBaseURL
}

func (p *Provider) wsBaseURL() string {
	if p.WSBaseURL != "" {
		return p.WSBaseURL
	}
	return defaultWSBaseURL
}

func (p *Provider) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}
