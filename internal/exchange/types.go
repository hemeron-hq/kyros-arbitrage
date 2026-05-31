package exchange

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/govalues/decimal"
)

type ID string

const (
	Binance ID = "binance"
	Kraken  ID = "kraken"
)

type Provider interface {
	Exchange() ID
}

type MarketDataProvider interface {
	Provider
	Stream(ctx context.Context, binding Binding, depth int, out chan<- OrderBookSnapshot) error
	Poll(ctx context.Context, binding Binding, depth int) (OrderBookSnapshot, error)
}

type OrderPlacer interface {
	Provider
	PlaceOrder(ctx context.Context, request OrderRequest) (OrderResult, error)
}

type OrderSide string

const (
	OrderSideBuy  OrderSide = "buy"
	OrderSideSell OrderSide = "sell"
)

type OrderType string

const (
	OrderTypeLimit  OrderType = "limit"
	OrderTypeMarket OrderType = "market"
)

type OrderStatus string

const (
	OrderStatusAccepted OrderStatus = "accepted"
	OrderStatusRejected OrderStatus = "rejected"
)

type OrderRequest struct {
	Exchange      ID
	Market        Market
	Side          OrderSide
	Type          OrderType
	Price         decimal.Decimal
	Quantity      decimal.Decimal
	ClientOrderID string
}

type OrderResult struct {
	Exchange       ID
	Market         Market
	OrderID        string
	ClientOrderID  string
	Status         OrderStatus
	FilledQuantity decimal.Decimal
	AveragePrice   decimal.Decimal
	Message        string
}

type Transport string

const (
	TransportWebSocket Transport = "websocket"
	TransportPolling   Transport = "polling"
	TransportNone      Transport = "none"
)

type FeedStatus string

const (
	StatusStarting   FeedStatus = "starting"
	StatusConnecting FeedStatus = "connecting"
	StatusLive       FeedStatus = "live"
	StatusStale      FeedStatus = "stale"
	StatusError      FeedStatus = "error"
)

type Market struct {
	Base  string `json:"base"`
	Quote string `json:"quote"`
}

func NewMarket(symbol string) (Market, error) {
	parts := strings.Split(strings.ToUpper(strings.TrimSpace(symbol)), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Market{}, fmt.Errorf("market must be BASE/QUOTE, got %q", symbol)
	}

	return Market{Base: parts[0], Quote: parts[1]}, nil
}

func (m Market) ID() string {
	return m.Base + "/" + m.Quote
}

type Binding struct {
	Exchange        ID
	Market          Market
	WebSocketSymbol string
	RESTSymbol      string
	Enabled         bool
}

func (b Binding) Key() Key {
	return Key{Exchange: b.Exchange, MarketID: b.Market.ID()}
}

type Key struct {
	Exchange ID
	MarketID string
}

func (k Key) String() string {
	return string(k.Exchange) + ":" + k.MarketID
}

type PriceLevel struct {
	Price        decimal.Decimal
	Quantity     decimal.Decimal
	PriceText    string
	QuantityText string
}

func NewPriceLevel(priceText, quantityText string) (PriceLevel, error) {
	price, err := decimal.Parse(priceText)
	if err != nil {
		return PriceLevel{}, fmt.Errorf("parse price %q: %w", priceText, err)
	}

	quantity, err := decimal.Parse(quantityText)
	if err != nil {
		return PriceLevel{}, fmt.Errorf("parse quantity %q: %w", quantityText, err)
	}

	return PriceLevel{
		Price:        price,
		Quantity:     quantity,
		PriceText:    priceText,
		QuantityText: quantityText,
	}, nil
}

type OrderBookSnapshot struct {
	Exchange     ID
	Market       Market
	Bids         []PriceLevel
	Asks         []PriceLevel
	ReceivedAt   time.Time
	ExchangeTime time.Time
	Latency      time.Duration
	Sequence     int64
	Transport    Transport
	Status       FeedStatus
	Message      string
}

func (s OrderBookSnapshot) Key() Key {
	return Key{Exchange: s.Exchange, MarketID: s.Market.ID()}
}

func (s OrderBookSnapshot) Clone() OrderBookSnapshot {
	clone := s
	clone.Bids = append([]PriceLevel(nil), s.Bids...)
	clone.Asks = append([]PriceLevel(nil), s.Asks...)
	return clone
}

func (s OrderBookSnapshot) HasBook() bool {
	return len(s.Bids) > 0 && len(s.Asks) > 0
}

func (s OrderBookSnapshot) BestBid() (PriceLevel, bool) {
	if len(s.Bids) == 0 {
		return PriceLevel{}, false
	}
	return s.Bids[0], true
}

func (s OrderBookSnapshot) BestAsk() (PriceLevel, bool) {
	if len(s.Asks) == 0 {
		return PriceLevel{}, false
	}
	return s.Asks[0], true
}

func DefaultBindings() []Binding {
	btcUSDT := Market{Base: "BTC", Quote: "USDT"}
	return []Binding{
		{
			Exchange:        Binance,
			Market:          btcUSDT,
			WebSocketSymbol: "BTCUSDT",
			RESTSymbol:      "BTCUSDT",
			Enabled:         true,
		},
		{
			Exchange:        Kraken,
			Market:          btcUSDT,
			WebSocketSymbol: "BTC/USDT",
			RESTSymbol:      "XBTUSDT",
			Enabled:         true,
		},
	}
}
