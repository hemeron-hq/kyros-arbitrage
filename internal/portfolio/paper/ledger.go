package paper

import (
	"sort"
	"sync"

	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
	"github.com/hemeron-hq/kyros-arbitrage/internal/portfolio"
	"github.com/hemeron-hq/kyros-arbitrage/internal/terms"
)

type Ledger struct {
	mu         sync.RWMutex
	balances   map[exchange.ID]map[string]decimal.Decimal
	sources    map[exchange.ID]terms.Source
	executions []portfolio.Execution
	sessionPNL decimal.Decimal
}

func NewLedger() *Ledger {
	ledger := &Ledger{
		balances: make(map[exchange.ID]map[string]decimal.Decimal),
		sources:  make(map[exchange.ID]terms.Source),
	}
	seen := make(map[exchange.ID]struct{})
	for _, binding := range exchange.DefaultBindings() {
		if _, ok := seen[binding.Exchange]; ok {
			continue
		}
		seen[binding.Exchange] = struct{}{}
		ledger.Seed(binding.Exchange, terms.FallbackBalances(), terms.SourceFallback)
	}
	return ledger
}

func (l *Ledger) Seed(exchangeID exchange.ID, balances map[string]decimal.Decimal, source terms.Source) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seedLocked(exchangeID, balances, source, false)
}

func (l *Ledger) SeedAuthenticatedOnce(snapshot terms.Snapshot) {
	if snapshot.Source != terms.SourceAuthenticated || len(snapshot.Balances) == 0 {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.executions) > 0 {
		return
	}
	if l.sources[snapshot.Exchange] == terms.SourceAuthenticated {
		return
	}
	l.seedLocked(
		snapshot.Exchange,
		terms.MergeBalanceFloors(snapshot.Balances, terms.FallbackBalances()),
		snapshot.Source,
		true,
	)
}

func (l *Ledger) Balance(exchangeID exchange.ID, asset string) decimal.Decimal {
	l.mu.RLock()
	defer l.mu.RUnlock()
	balances := l.balances[exchangeID]
	if balances == nil {
		return terms.FallbackBalances()[asset]
	}
	return balances[asset]
}

func (l *Ledger) Balances() []portfolio.BalanceRow {
	l.mu.RLock()
	defer l.mu.RUnlock()

	rows := make([]portfolio.BalanceRow, 0)
	for exchangeID, balances := range l.balances {
		for asset, amount := range balances {
			rows = append(rows, portfolio.BalanceRow{
				Exchange: exchangeID,
				Asset:    asset,
				Amount:   amount,
				Source:   l.sources[exchangeID],
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Exchange == rows[j].Exchange {
			return rows[i].Asset < rows[j].Asset
		}
		return rows[i].Exchange < rows[j].Exchange
	})
	return rows
}

func (l *Ledger) Apply(execution portfolio.Execution) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	buyBalances := l.ensureExchangeLocked(execution.BuyExchange)
	sellBalances := l.ensureExchangeLocked(execution.SellExchange)

	buyCost, err := execution.BuyNotional.Add(execution.BuyFee)
	if err != nil {
		return false
	}
	sellProceeds, err := execution.SellNotional.Sub(execution.SellFee)
	if err != nil {
		return false
	}
	if buyBalances[execution.Market.Quote].Cmp(buyCost) < 0 {
		return false
	}
	if sellBalances[execution.Market.Base].Cmp(execution.BaseSize) < 0 {
		return false
	}

	buyBalances[execution.Market.Quote], _ = buyBalances[execution.Market.Quote].Sub(buyCost)
	buyBalances[execution.Market.Base], _ = buyBalances[execution.Market.Base].Add(execution.BaseSize)
	sellBalances[execution.Market.Base], _ = sellBalances[execution.Market.Base].Sub(execution.BaseSize)
	sellBalances[execution.Market.Quote], _ = sellBalances[execution.Market.Quote].Add(sellProceeds)

	l.sessionPNL, _ = l.sessionPNL.Add(execution.NetProfit)
	l.executions = append(l.executions, execution)
	return true
}

func (l *Ledger) Stats() portfolio.Stats {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return portfolio.Stats{
		SessionPNL: l.sessionPNL,
		Executed:   len(l.executions),
	}
}

func (l *Ledger) seedLocked(exchangeID exchange.ID, balances map[string]decimal.Decimal, source terms.Source, replace bool) {
	if _, exists := l.balances[exchangeID]; exists && !replace {
		return
	}
	l.balances[exchangeID] = cloneBalances(balances)
	l.sources[exchangeID] = source
}

func (l *Ledger) ensureExchangeLocked(exchangeID exchange.ID) map[string]decimal.Decimal {
	if l.balances[exchangeID] == nil {
		l.balances[exchangeID] = terms.FallbackBalances()
		l.sources[exchangeID] = terms.SourceFallback
	}
	return l.balances[exchangeID]
}

func cloneBalances(values map[string]decimal.Decimal) map[string]decimal.Decimal {
	clone := make(map[string]decimal.Decimal, len(values))
	for asset, value := range values {
		clone[asset] = value
	}
	return clone
}
