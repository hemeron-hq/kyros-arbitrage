package terms

import (
	"time"

	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

const DefaultTTL = 30 * time.Minute

var (
	defaultBTCUSDT = exchange.Market{Base: "BTC", Quote: "USDT"}

	fallbackBinanceFees = exchange.FeeSchedule{
		MakerRate: decimal.MustNew(10, 4), // 0.0010
		TakerRate: decimal.MustNew(10, 4),
	}
	fallbackKrakenFees = exchange.FeeSchedule{
		MakerRate: decimal.MustNew(25, 4), // 0.0025
		TakerRate: decimal.MustNew(40, 4), // 0.0040
	}
	fallbackConstraints = exchange.MarketConstraints{
		MinBase:     decimal.MustNew(1, 5),
		MinNotional: decimal.MustNew(5, 0),
		StepSize:    decimal.MustNew(1, 5),
		TickSize:    decimal.MustNew(1, 2),
	}
	fallbackBinanceTransferFees = exchange.TransferFees{
		"BTC":  decimal.MustNew(1, 4),
		"USDT": decimal.MustNew(1, 0),
	}
	fallbackKrakenTransferFees = exchange.TransferFees{
		"BTC":  decimal.MustNew(2, 4),
		"USDT": decimal.MustNew(25, 1),
	}
)

func FallbackSnapshot(exchangeID exchange.ID, market exchange.Market, now time.Time, message string) Snapshot {
	if market.ID() == "/" {
		market = defaultBTCUSDT
	}
	return Snapshot{
		Exchange:     exchangeID,
		Market:       market,
		Source:       SourceFallback,
		Fees:         FallbackFees(exchangeID),
		Constraints:  fallbackConstraints,
		Balances:     FallbackBalances(),
		TransferFees: FallbackTransferFees(exchangeID),
		UpdatedAt:    now,
		ExpiresAt:    now.Add(365 * 24 * time.Hour),
		Message:      message,
	}
}

func FallbackFees(exchangeID exchange.ID) exchange.FeeSchedule {
	if exchangeID == exchange.Kraken {
		return fallbackKrakenFees
	}
	return fallbackBinanceFees
}

func FallbackConstraints() exchange.MarketConstraints {
	return fallbackConstraints
}

func FallbackTransferFees(exchangeID exchange.ID) exchange.TransferFees {
	if exchangeID == exchange.Kraken {
		return cloneTransferFees(fallbackKrakenTransferFees)
	}
	return cloneTransferFees(fallbackBinanceTransferFees)
}

func FallbackBalances() map[string]decimal.Decimal {
	return map[string]decimal.Decimal{
		"BTC":  decimal.MustNew(25, 2),
		"USDT": decimal.MustNew(25000, 0),
	}
}

func cloneTransferFees(values exchange.TransferFees) exchange.TransferFees {
	clone := make(exchange.TransferFees, len(values))
	for asset, value := range values {
		clone[asset] = value
	}
	return clone
}
