package terms

import (
	"time"

	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

const DefaultTTL = 30 * time.Minute

var (
	defaultBTCUSDT = exchange.Market{Base: "BTC", Quote: "USDT"}
	defaultBTCUSD  = exchange.Market{Base: "BTC", Quote: "USD"}

	fallbackProfiles = map[exchange.ID]fallbackProfile{
		// Public fallback defaults reviewed against official fee/API references on 2026-05-31.
		// Authenticated exchange terms still override these demo profiles when credentials are configured.
		exchange.Binance: {
			Fees: exchange.FeeSchedule{MakerRate: decimal.MustNew(10, 4), TakerRate: decimal.MustNew(10, 4)},
			Constraints: exchange.MarketConstraints{
				MinBase:     decimal.MustNew(1, 5),
				MinNotional: decimal.MustNew(5, 0),
				StepSize:    decimal.MustNew(1, 5),
				TickSize:    decimal.MustNew(1, 2),
			},
			Transfers: exchange.TransferFees{"BTC": decimal.MustNew(1, 4), "USDT": decimal.MustNew(1, 0)},
		},
		exchange.Kraken: {
			Fees: exchange.FeeSchedule{MakerRate: decimal.MustNew(25, 4), TakerRate: decimal.MustNew(40, 4)},
			Constraints: exchange.MarketConstraints{
				MinBase:     decimal.MustNew(1, 5),
				MinNotional: decimal.MustNew(10, 0),
				StepSize:    decimal.MustNew(1, 5),
				TickSize:    decimal.MustNew(1, 1),
			},
			Transfers: exchange.TransferFees{"BTC": decimal.MustNew(2, 5), "USDT": decimal.MustNew(8, 0)},
		},
		exchange.Coinbase: {
			Fees: exchange.FeeSchedule{MakerRate: decimal.MustNew(4, 3), TakerRate: decimal.MustNew(6, 3)},
			Constraints: exchange.MarketConstraints{
				MinBase:     decimal.MustNew(1, 6),
				MinNotional: decimal.MustNew(1, 0),
				StepSize:    decimal.MustNew(1, 8),
				TickSize:    decimal.MustNew(1, 2),
			},
			Transfers: exchange.TransferFees{"BTC": decimal.MustNew(5, 5), "USD": decimal.MustNew(25, 0)},
		},
		exchange.OKX: {
			Fees: exchange.FeeSchedule{MakerRate: decimal.MustNew(8, 4), TakerRate: decimal.MustNew(10, 4)},
			Constraints: exchange.MarketConstraints{
				MinBase:     decimal.MustNew(1, 5),
				MinNotional: decimal.MustNew(10, 0),
				StepSize:    decimal.MustNew(1, 8),
				TickSize:    decimal.MustNew(1, 1),
			},
			Transfers: exchange.TransferFees{"BTC": decimal.MustNew(5, 5), "USDT": decimal.MustNew(1, 0)},
		},
		exchange.Bybit: {
			Fees: exchange.FeeSchedule{MakerRate: decimal.MustNew(10, 4), TakerRate: decimal.MustNew(10, 4)},
			Constraints: exchange.MarketConstraints{
				MinBase:     decimal.MustNew(1, 6),
				MinNotional: decimal.MustNew(5, 0),
				StepSize:    decimal.MustNew(1, 6),
				TickSize:    decimal.MustNew(1, 2),
			},
			Transfers: exchange.TransferFees{"BTC": decimal.MustNew(2, 4), "USDT": decimal.MustNew(1, 0)},
		},
		exchange.Bitfinex: {
			Fees: exchange.FeeSchedule{MakerRate: decimal.Zero, TakerRate: decimal.Zero},
			Constraints: exchange.MarketConstraints{
				MinBase:     decimal.MustNew(4, 5),
				MinNotional: decimal.MustNew(10, 0),
				StepSize:    decimal.MustNew(1, 8),
				TickSize:    decimal.MustNew(1, 0),
			},
			Transfers: exchange.TransferFees{"BTC": decimal.MustNew(4, 4), "USD": decimal.MustNew(60, 0)},
		},
		exchange.KuCoin: {
			Fees: exchange.FeeSchedule{MakerRate: decimal.MustNew(10, 4), TakerRate: decimal.MustNew(10, 4)},
			Constraints: exchange.MarketConstraints{
				MinBase:     decimal.MustNew(1, 6),
				MinNotional: decimal.MustNew(1, 0),
				StepSize:    decimal.MustNew(1, 8),
				TickSize:    decimal.MustNew(1, 1),
			},
			Transfers: exchange.TransferFees{"BTC": decimal.MustNew(4, 4), "USDT": decimal.MustNew(1, 0)},
		},
		exchange.Gate: {
			Fees: exchange.FeeSchedule{MakerRate: decimal.MustNew(10, 4), TakerRate: decimal.MustNew(10, 4)},
			Constraints: exchange.MarketConstraints{
				MinBase:     decimal.MustNew(1, 6),
				MinNotional: decimal.MustNew(1, 0),
				StepSize:    decimal.MustNew(1, 8),
				TickSize:    decimal.MustNew(1, 1),
			},
			Transfers: exchange.TransferFees{"BTC": decimal.MustNew(2, 4), "USDT": decimal.MustNew(1, 0)},
		},
		exchange.Bitstamp: {
			Fees: exchange.FeeSchedule{MakerRate: decimal.MustNew(30, 4), TakerRate: decimal.MustNew(40, 4)},
			Constraints: exchange.MarketConstraints{
				MinBase:     decimal.MustNew(1, 5),
				MinNotional: decimal.MustNew(10, 0),
				StepSize:    decimal.MustNew(1, 8),
				TickSize:    decimal.MustNew(1, 0),
			},
			Transfers: exchange.TransferFees{"BTC": decimal.MustNew(5, 4), "USD": decimal.MustNew(25, 0)},
		},
		exchange.Gemini: {
			Fees: exchange.FeeSchedule{MakerRate: decimal.MustNew(20, 4), TakerRate: decimal.MustNew(40, 4)},
			Constraints: exchange.MarketConstraints{
				MinBase:     decimal.MustNew(1, 5),
				MinNotional: decimal.MustNew(1, 0),
				StepSize:    decimal.MustNew(1, 8),
				TickSize:    decimal.MustNew(1, 2),
			},
			Transfers: exchange.TransferFees{"BTC": decimal.MustNew(1, 4), "USD": decimal.MustNew(25, 0)},
		},
	}
)

type fallbackProfile struct {
	Fees        exchange.FeeSchedule
	Constraints exchange.MarketConstraints
	Transfers   exchange.TransferFees
}

func FallbackSnapshot(exchangeID exchange.ID, market exchange.Market, now time.Time, message string) Snapshot {
	if market.ID() == "/" {
		market = defaultMarket(exchangeID)
	}
	profile := FallbackProfile(exchangeID)
	return Snapshot{
		Exchange:     exchangeID,
		Market:       market,
		Source:       SourceFallback,
		Fees:         profile.Fees,
		Constraints:  profile.Constraints,
		Balances:     FallbackBalances(),
		TransferFees: cloneTransferFees(profile.Transfers),
		UpdatedAt:    now,
		ExpiresAt:    now.Add(365 * 24 * time.Hour),
		Message:      message,
	}
}

func FallbackProfile(exchangeID exchange.ID) fallbackProfile {
	profile, ok := fallbackProfiles[exchangeID]
	if !ok {
		return fallbackProfiles[exchange.Binance]
	}
	return profile
}

func FallbackFees(exchangeID exchange.ID) exchange.FeeSchedule {
	return FallbackProfile(exchangeID).Fees
}

func FallbackConstraints() exchange.MarketConstraints {
	return fallbackProfiles[exchange.Binance].Constraints
}

func FallbackTransferFees(exchangeID exchange.ID) exchange.TransferFees {
	return cloneTransferFees(FallbackProfile(exchangeID).Transfers)
}

func FallbackBalances() map[string]decimal.Decimal {
	return map[string]decimal.Decimal{
		"BTC":  decimal.MustNew(25, 2),
		"USDT": decimal.MustNew(25000, 0),
		"USD":  decimal.MustNew(25000, 0),
	}
}

func defaultMarket(exchangeID exchange.ID) exchange.Market {
	switch exchangeID {
	case exchange.Coinbase, exchange.Bitfinex, exchange.Bitstamp, exchange.Gemini:
		return defaultBTCUSD
	default:
		return defaultBTCUSDT
	}
}

func cloneTransferFees(values exchange.TransferFees) exchange.TransferFees {
	clone := make(exchange.TransferFees, len(values))
	for asset, value := range values {
		clone[asset] = value
	}
	return clone
}
