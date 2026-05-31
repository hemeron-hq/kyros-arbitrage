package strategy

import (
	"sort"

	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

var basisPointMultiplier = decimal.MustNew(10_000, 0)

type Route struct {
	Market         string
	BuyVenue       exchange.Venue
	SellVenue      exchange.Venue
	GrossSpread    decimal.Decimal
	GrossSpreadBPS decimal.Decimal
	GrossProfit    decimal.Decimal
	MaxBaseSize    decimal.Decimal
	AvgBuyPrice    decimal.Decimal
	AvgSellPrice   decimal.Decimal
	Executable     bool
}

func FindRoutes(snapshots []exchange.OrderBookSnapshot) []Route {
	byMarket := make(map[string][]exchange.OrderBookSnapshot)
	for _, snapshot := range snapshots {
		if snapshot.Status != exchange.StatusLive || !snapshot.HasBook() {
			continue
		}
		byMarket[snapshot.Market.ID()] = append(byMarket[snapshot.Market.ID()], snapshot)
	}

	routes := make([]Route, 0)
	for marketID, marketSnapshots := range byMarket {
		for _, buy := range marketSnapshots {
			for _, sell := range marketSnapshots {
				if sell.Venue == buy.Venue {
					continue
				}
				route, ok := evaluateRoute(marketID, buy, sell)
				if ok {
					routes = append(routes, route)
				}
			}
		}
	}

	sort.Slice(routes, func(i, j int) bool {
		left := routes[i]
		right := routes[j]
		if left.Executable != right.Executable {
			return left.Executable
		}
		if left.Executable {
			if !left.GrossProfit.Equal(right.GrossProfit) {
				return left.GrossProfit.Cmp(right.GrossProfit) > 0
			}
			return left.GrossSpreadBPS.Cmp(right.GrossSpreadBPS) > 0
		}
		return left.GrossSpread.Cmp(right.GrossSpread) > 0
	})

	return routes
}

func evaluateRoute(marketID string, buy exchange.OrderBookSnapshot, sell exchange.OrderBookSnapshot) (Route, bool) {
	route := Route{
		Market:    marketID,
		BuyVenue:  buy.Venue,
		SellVenue: sell.Venue,
	}

	topAsk, askOK := buy.BestAsk()
	topBid, bidOK := sell.BestBid()
	if !askOK || !bidOK {
		return Route{}, false
	}

	grossSpread, err := topBid.Price.Sub(topAsk.Price)
	if err != nil {
		return Route{}, false
	}
	route.GrossSpread = grossSpread
	if bps, ok := spreadBPS(grossSpread, topAsk.Price); ok {
		route.GrossSpreadBPS = bps
	}

	base, buyNotional, sellNotional, ok := walkExecutableDepth(buy.Asks, sell.Bids)
	if !ok {
		return Route{}, false
	}
	if base.IsZero() {
		return route, true
	}

	grossProfit, err := sellNotional.Sub(buyNotional)
	if err != nil {
		return Route{}, false
	}
	avgBuyPrice, err := buyNotional.Quo(base)
	if err != nil {
		return Route{}, false
	}
	avgSellPrice, err := sellNotional.Quo(base)
	if err != nil {
		return Route{}, false
	}
	grossSpread, err = avgSellPrice.Sub(avgBuyPrice)
	if err != nil {
		return Route{}, false
	}

	route.Executable = true
	route.MaxBaseSize = base
	route.GrossProfit = grossProfit
	route.AvgBuyPrice = avgBuyPrice
	route.AvgSellPrice = avgSellPrice
	route.GrossSpread = grossSpread
	if bps, ok := spreadBPS(grossSpread, avgBuyPrice); ok {
		route.GrossSpreadBPS = bps
	}

	return route, true
}

func walkExecutableDepth(asks []exchange.PriceLevel, bids []exchange.PriceLevel) (decimal.Decimal, decimal.Decimal, decimal.Decimal, bool) {
	var base decimal.Decimal
	var buyNotional decimal.Decimal
	var sellNotional decimal.Decimal
	askIndex := 0
	bidIndex := 0
	askRemaining := decimal.Zero
	bidRemaining := decimal.Zero

	for askIndex < len(asks) && bidIndex < len(bids) {
		ask := asks[askIndex]
		bid := bids[bidIndex]
		if bid.Price.Cmp(ask.Price) <= 0 {
			break
		}

		if askRemaining.IsZero() {
			askRemaining = ask.Quantity
		}
		if bidRemaining.IsZero() {
			bidRemaining = bid.Quantity
		}

		size := askRemaining.Min(bidRemaining)
		if size.IsZero() {
			break
		}

		var err error
		base, err = base.Add(size)
		if err != nil {
			return decimal.Zero, decimal.Zero, decimal.Zero, false
		}
		buyNotional, err = addMul(buyNotional, size, ask.Price)
		if err != nil {
			return decimal.Zero, decimal.Zero, decimal.Zero, false
		}
		sellNotional, err = addMul(sellNotional, size, bid.Price)
		if err != nil {
			return decimal.Zero, decimal.Zero, decimal.Zero, false
		}

		askRemaining, err = askRemaining.Sub(size)
		if err != nil {
			return decimal.Zero, decimal.Zero, decimal.Zero, false
		}
		bidRemaining, err = bidRemaining.Sub(size)
		if err != nil {
			return decimal.Zero, decimal.Zero, decimal.Zero, false
		}
		if askRemaining.IsZero() {
			askIndex++
		}
		if bidRemaining.IsZero() {
			bidIndex++
		}
	}

	return base, buyNotional, sellNotional, true
}

func spreadBPS(spread decimal.Decimal, price decimal.Decimal) (decimal.Decimal, bool) {
	if !price.IsPos() {
		return decimal.Zero, false
	}

	ratio, err := spread.Quo(price)
	if err != nil {
		return decimal.Zero, false
	}
	bps, err := ratio.Mul(basisPointMultiplier)
	if err != nil {
		return decimal.Zero, false
	}
	return bps, true
}

func addMul(total decimal.Decimal, left decimal.Decimal, right decimal.Decimal) (decimal.Decimal, error) {
	product, err := left.Mul(right)
	if err != nil {
		return decimal.Zero, err
	}
	return total.Add(product)
}
