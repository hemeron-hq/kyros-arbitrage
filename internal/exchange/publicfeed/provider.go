package publicfeed

import (
	"bytes"
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

type Provider struct {
	spec       Spec
	HTTPClient *http.Client
	Now        func() time.Time
}

type Spec struct {
	Exchange  exchange.ID
	RESTURL   func(binding exchange.Binding, depth int) string
	ConnectWS func(ctx context.Context, provider *Provider, binding exchange.Binding, depth int) (WSConnection, error)
	ParseREST func(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time, latency time.Duration) (exchange.OrderBookSnapshot, error)
}

type WSConnection struct {
	URL      string
	Payloads [][]byte
	Parser   WSParser
}

type WSParser interface {
	Parse(payload []byte, binding exchange.Binding, depth int, receivedAt time.Time) (exchange.OrderBookSnapshot, bool, error)
}

var _ exchange.MarketDataProvider = (*Provider)(nil)

func New(spec Spec) *Provider {
	return &Provider{
		spec:       spec,
		HTTPClient: http.DefaultClient,
		Now:        time.Now,
	}
}

func (p *Provider) Exchange() exchange.ID {
	return p.spec.Exchange
}

func (p *Provider) Stream(ctx context.Context, binding exchange.Binding, depth int, out chan<- exchange.OrderBookSnapshot) error {
	connInfo, err := p.spec.ConnectWS(ctx, p, binding, depth)
	if err != nil {
		return err
	}
	conn, _, err := websocket.Dial(ctx, connInfo.URL, nil)
	if err != nil {
		return fmt.Errorf("dial %s websocket: %w", p.Exchange(), err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 20)

	for _, payload := range connInfo.Payloads {
		if len(payload) == 0 {
			continue
		}
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			return fmt.Errorf("subscribe %s websocket: %w", p.Exchange(), err)
		}
	}

	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		snapshot, ok, err := connInfo.Parser.Parse(payload, binding, depth, p.now())
		if err != nil {
			return err
		}
		if ok {
			out <- snapshot
		}
	}
}

func (p *Provider) Poll(ctx context.Context, binding exchange.Binding, depth int) (exchange.OrderBookSnapshot, error) {
	if p.spec.RESTURL == nil || p.spec.ParseREST == nil {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("%s REST depth unavailable", p.Exchange())
	}
	startedAt := p.now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.spec.RESTURL(binding, depth), nil)
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
		return exchange.OrderBookSnapshot{}, fmt.Errorf("%s depth status %d: %s", p.Exchange(), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return exchange.OrderBookSnapshot{}, err
	}
	now := p.now()
	return p.spec.ParseREST(payload, binding, depth, now, now.Sub(startedAt))
}

func (p *Provider) httpClient() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return http.DefaultClient
}

func (p *Provider) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func NewOKX() *Provider {
	return New(Spec{
		Exchange: exchange.OKX,
		RESTURL: func(binding exchange.Binding, depth int) string {
			return fmt.Sprintf("https://www.okx.com/api/v5/market/books?instId=%s&sz=%d", url.QueryEscape(binding.RESTSymbol), depth)
		},
		ConnectWS: func(_ context.Context, _ *Provider, binding exchange.Binding, _ int) (WSConnection, error) {
			payload, _ := json.Marshal(map[string]any{
				"op": "subscribe",
				"args": []map[string]string{{
					"channel": "books5",
					"instId":  binding.WebSocketSymbol,
				}},
			})
			return WSConnection{URL: "wss://ws.okx.com:8443/ws/v5/public", Payloads: [][]byte{payload}, Parser: okxParser{}}, nil
		},
		ParseREST: parseOKXREST,
	})
}

func NewBybit() *Provider {
	return New(Spec{
		Exchange: exchange.Bybit,
		RESTURL: func(binding exchange.Binding, depth int) string {
			return fmt.Sprintf("https://api.bybit.com/v5/market/orderbook?category=spot&symbol=%s&limit=%d", url.QueryEscape(binding.RESTSymbol), depth)
		},
		ConnectWS: func(_ context.Context, _ *Provider, binding exchange.Binding, _ int) (WSConnection, error) {
			payload, _ := json.Marshal(map[string]any{
				"op":   "subscribe",
				"args": []string{"orderbook.50." + binding.WebSocketSymbol},
			})
			return WSConnection{URL: "wss://stream.bybit.com/v5/public/spot", Payloads: [][]byte{payload}, Parser: bybitParser{}}, nil
		},
		ParseREST: parseBybitREST,
	})
}

func NewKuCoin() *Provider {
	return New(Spec{
		Exchange: exchange.KuCoin,
		RESTURL: func(binding exchange.Binding, depth int) string {
			return fmt.Sprintf("https://api.kucoin.com/api/v1/market/orderbook/level2_%d?symbol=%s", depth, url.QueryEscape(binding.RESTSymbol))
		},
		ConnectWS: connectKuCoinWS,
		ParseREST: parseKuCoinREST,
	})
}

func NewGate() *Provider {
	return New(Spec{
		Exchange: exchange.Gate,
		RESTURL: func(binding exchange.Binding, depth int) string {
			return fmt.Sprintf("https://api.gateio.ws/api/v4/spot/order_book?currency_pair=%s&limit=%d", url.QueryEscape(binding.RESTSymbol), depth)
		},
		ConnectWS: func(_ context.Context, _ *Provider, binding exchange.Binding, depth int) (WSConnection, error) {
			payload, _ := json.Marshal(map[string]any{
				"time":    time.Now().Unix(),
				"channel": "spot.order_book",
				"event":   "subscribe",
				"payload": []string{binding.WebSocketSymbol, strconv.Itoa(depth), "100ms"},
			})
			return WSConnection{URL: "wss://api.gateio.ws/ws/v4/", Payloads: [][]byte{payload}, Parser: gateParser{}}, nil
		},
		ParseREST: parseGateREST,
	})
}

func NewBitstamp() *Provider {
	return New(Spec{
		Exchange: exchange.Bitstamp,
		RESTURL: func(binding exchange.Binding, _ int) string {
			return "https://www.bitstamp.net/api/v2/order_book/" + url.PathEscape(binding.RESTSymbol) + "/"
		},
		ConnectWS: func(_ context.Context, _ *Provider, binding exchange.Binding, _ int) (WSConnection, error) {
			payload, _ := json.Marshal(map[string]any{
				"event": "bts:subscribe",
				"data":  map[string]string{"channel": "order_book_" + binding.WebSocketSymbol},
			})
			return WSConnection{URL: "wss://ws.bitstamp.net", Payloads: [][]byte{payload}, Parser: bitstampParser{}}, nil
		},
		ParseREST: parseBitstampREST,
	})
}

func connectKuCoinWS(ctx context.Context, provider *Provider, binding exchange.Binding, _ int) (WSConnection, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.kucoin.com/api/v1/bullet-public", bytes.NewReader(nil))
	if err != nil {
		return WSConnection{}, err
	}
	resp, err := provider.httpClient().Do(req)
	if err != nil {
		return WSConnection{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return WSConnection{}, fmt.Errorf("kucoin bullet status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var response kuCoinBulletResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return WSConnection{}, err
	}
	if response.Data.Token == "" || len(response.Data.InstanceServers) == 0 {
		return WSConnection{}, fmt.Errorf("kucoin bullet response missing token or endpoint")
	}
	endpoint := strings.TrimRight(response.Data.InstanceServers[0].Endpoint, "/")
	connectID := strconv.FormatInt(provider.now().UnixNano(), 10)
	payload, _ := json.Marshal(map[string]any{
		"id":             connectID,
		"type":           "subscribe",
		"topic":          "/spotMarket/level2Depth50:" + binding.WebSocketSymbol,
		"privateChannel": false,
		"response":       true,
	})
	return WSConnection{
		URL:      endpoint + "?token=" + url.QueryEscape(response.Data.Token) + "&connectId=" + url.QueryEscape(connectID),
		Payloads: [][]byte{payload},
		Parser:   kuCoinParser{},
	}, nil
}

type kuCoinBulletResponse struct {
	Data struct {
		Token           string `json:"token"`
		InstanceServers []struct {
			Endpoint string `json:"endpoint"`
		} `json:"instanceServers"`
	} `json:"data"`
}

func snapshot(exchangeID exchange.ID, binding exchange.Binding, bids []exchange.PriceLevel, asks []exchange.PriceLevel, receivedAt time.Time, exchangeTime time.Time, latency time.Duration, sequence int64, transport exchange.Transport, message string) (exchange.OrderBookSnapshot, error) {
	if len(bids) == 0 || len(asks) == 0 {
		return exchange.OrderBookSnapshot{}, fmt.Errorf("%s depth has empty side", exchangeID)
	}
	if !exchangeTime.IsZero() && latency == 0 && receivedAt.After(exchangeTime) {
		latency = receivedAt.Sub(exchangeTime)
	}
	return exchange.OrderBookSnapshot{
		Exchange:     exchangeID,
		Market:       binding.Market,
		Bids:         bids,
		Asks:         asks,
		ReceivedAt:   receivedAt,
		ExchangeTime: exchangeTime,
		Latency:      latency,
		Sequence:     sequence,
		Transport:    transport,
		Status:       exchange.StatusLive,
		Message:      message,
	}, nil
}

func parseStringLevels(raw [][]string, depth int) ([]exchange.PriceLevel, error) {
	depth = min(max(depth, 0), len(raw))
	levels := make([]exchange.PriceLevel, 0, depth)
	for i := 0; i < depth; i++ {
		if len(raw[i]) < 2 {
			return nil, fmt.Errorf("level %d has %d fields", i, len(raw[i]))
		}
		level, err := exchange.NewPriceLevel(raw[i][0], raw[i][1])
		if err != nil {
			return nil, err
		}
		if !level.Quantity.IsZero() {
			levels = append(levels, level)
		}
	}
	return levels, nil
}

func parseObjectLevels(raw []objectLevel, depth int) ([]exchange.PriceLevel, error) {
	depth = min(max(depth, 0), len(raw))
	levels := make([]exchange.PriceLevel, 0, depth)
	for i := 0; i < depth; i++ {
		level, err := exchange.NewPriceLevel(firstNonEmpty(raw[i].Price, raw[i].P), firstNonEmpty(raw[i].Quantity, raw[i].Size, raw[i].S))
		if err != nil {
			return nil, err
		}
		if !level.Quantity.IsZero() {
			levels = append(levels, level)
		}
	}
	return levels, nil
}

type objectLevel struct {
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
	Size     string `json:"size"`
	P        string `json:"p"`
	S        string `json:"s"`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func unixMillis(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value)
}

func unixMillisString(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return unixMillis(parsed)
}

func unixMicroString(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMicro(parsed)
}
