package publicfeed

import (
	"testing"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

func TestNewExchangeWebSocketParsers(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tests := []struct {
		name     string
		parser   WSParser
		binding  exchange.Binding
		payload  string
		exchange exchange.ID
		bid      string
		ask      string
	}{
		{
			name:     "coinbase",
			parser:   newCoinbaseParser(),
			binding:  testBinding(exchange.Coinbase, "BTC/USD", "BTC-USD"),
			payload:  `{"channel":"l2_data","timestamp":"2026-05-31T12:00:00.100Z","events":[{"type":"snapshot","product_id":"BTC-USD","updates":[{"side":"bid","price_level":"100","new_quantity":"1","event_time":"2026-05-31T12:00:00Z"},{"side":"offer","price_level":"101","new_quantity":"2","event_time":"2026-05-31T12:00:00Z"}]}]}`,
			exchange: exchange.Coinbase,
			bid:      "100",
			ask:      "101",
		},
		{
			name:     "okx",
			parser:   okxParser{},
			binding:  testBinding(exchange.OKX, "BTC/USDT", "BTC-USDT"),
			payload:  `{"arg":{"channel":"books","instId":"BTC-USDT"},"data":[{"bids":[["100","1","0","1"]],"asks":[["101","2","0","1"]],"ts":"1800000000000","seqId":123}]}`,
			exchange: exchange.OKX,
			bid:      "100",
			ask:      "101",
		},
		{
			name:     "bybit",
			parser:   bybitParser{},
			binding:  testBinding(exchange.Bybit, "BTC/USDT", "BTCUSDT"),
			payload:  `{"topic":"orderbook.50.BTCUSDT","type":"snapshot","ts":1800000000000,"data":{"s":"BTCUSDT","b":[["100","1"]],"a":[["101","2"]],"u":10}}`,
			exchange: exchange.Bybit,
			bid:      "100",
			ask:      "101",
		},
		{
			name:     "bitfinex",
			parser:   newBitfinexParser(),
			binding:  testBinding(exchange.Bitfinex, "BTC/USD", "tBTCUSD"),
			payload:  `[1,[[100,1,1],[101,1,-2]]]`,
			exchange: exchange.Bitfinex,
			bid:      "100",
			ask:      "101",
		},
		{
			name:     "kucoin",
			parser:   kuCoinParser{},
			binding:  testBinding(exchange.KuCoin, "BTC/USDT", "BTC-USDT"),
			payload:  `{"type":"message","topic":"/spotMarket/level2Depth50:BTC-USDT","subject":"level2","data":{"bids":[["100","1"]],"asks":[["101","2"]],"timestamp":1800000000000,"sequence":"42"}}`,
			exchange: exchange.KuCoin,
			bid:      "100",
			ask:      "101",
		},
		{
			name:     "gate",
			parser:   gateParser{},
			binding:  testBinding(exchange.Gate, "BTC/USDT", "BTC_USDT"),
			payload:  `{"time":1800000000,"time_ms":1800000000000,"channel":"spot.order_book","event":"update","result":{"id":7,"bids":[{"p":"100","s":"1"}],"asks":[{"p":"101","s":"2"}]}}`,
			exchange: exchange.Gate,
			bid:      "100",
			ask:      "101",
		},
		{
			name:     "bitstamp",
			parser:   bitstampParser{},
			binding:  testBinding(exchange.Bitstamp, "BTC/USD", "btcusd"),
			payload:  `{"event":"data","channel":"order_book_btcusd","data":{"microtimestamp":"1800000000000000","bids":[["100","1"]],"asks":[["101","2"]]}}`,
			exchange: exchange.Bitstamp,
			bid:      "100",
			ask:      "101",
		},
		{
			name:     "gemini",
			parser:   newGeminiParser(),
			binding:  testBinding(exchange.Gemini, "BTC/USD", "btcusd"),
			payload:  `{"stream":"btcusd@depth10@100ms","data":{"E":1800000000000,"bids":[["100","1"]],"asks":[["101","2"]]}}`,
			exchange: exchange.Gemini,
			bid:      "100",
			ask:      "101",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot, ok, err := tt.parser.Parse([]byte(tt.payload), tt.binding, 10, now)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Fatal("expected parser to emit a snapshot")
			}
			if snapshot.Exchange != tt.exchange {
				t.Fatalf("expected exchange %s, got %s", tt.exchange, snapshot.Exchange)
			}
			bid, _ := snapshot.BestBid()
			ask, _ := snapshot.BestAsk()
			if bid.PriceText != tt.bid || ask.PriceText != tt.ask {
				t.Fatalf("expected bid/ask %s/%s, got %s/%s", tt.bid, tt.ask, bid.PriceText, ask.PriceText)
			}
			if snapshot.ExchangeTime.IsZero() && tt.name != "bitfinex" {
				t.Fatal("expected venue timestamp")
			}
		})
	}
}

func testBinding(exchangeID exchange.ID, market string, symbol string) exchange.Binding {
	parsed, err := exchange.NewMarket(market)
	if err != nil {
		panic(err)
	}
	return exchange.Binding{
		Exchange:        exchangeID,
		Market:          parsed,
		WebSocketSymbol: symbol,
		RESTSymbol:      symbol,
		Enabled:         true,
	}
}
