package registry

import (
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange/binance"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange/bitfinex"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange/coinbase"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange/gemini"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange/kraken"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange/publicfeed"
	"github.com/hemeron-hq/kyros-arbitrage/internal/platform/config"
)

type Registry struct {
	Bindings            []exchange.Binding
	MarketDataProviders map[exchange.ID]exchange.MarketDataProvider
	TermsClients        map[exchange.ID]exchange.TermsClient
}

func New(cfg config.Config) Registry {
	binanceProvider := binance.New(binance.WithCredentials(cfg.BinanceAPIKey, cfg.BinanceAPISecret))
	krakenProvider := kraken.New(kraken.WithCredentials(cfg.KrakenAPIKey, cfg.KrakenAPISecret))

	marketProviders := map[exchange.ID]exchange.MarketDataProvider{
		exchange.Binance:  binanceProvider,
		exchange.Kraken:   krakenProvider,
		exchange.Coinbase: coinbase.New(),
		exchange.OKX:      publicfeed.NewOKX(),
		exchange.Bybit:    publicfeed.NewBybit(),
		exchange.Bitfinex: bitfinex.New(),
		exchange.KuCoin:   publicfeed.NewKuCoin(),
		exchange.Gate:     publicfeed.NewGate(),
		exchange.Bitstamp: publicfeed.NewBitstamp(),
		exchange.Gemini:   gemini.New(),
	}
	termsClients := map[exchange.ID]exchange.TermsClient{
		exchange.Binance: binanceProvider,
		exchange.Kraken:  krakenProvider,
	}

	return Registry{
		Bindings:            exchange.DefaultBindings(),
		MarketDataProviders: marketProviders,
		TermsClients:        termsClients,
	}
}
