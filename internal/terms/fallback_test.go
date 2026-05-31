package terms

import (
	"testing"
	"time"

	"github.com/govalues/decimal"
	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

func TestFallbackSnapshotIncludesTransferFees(t *testing.T) {
	for _, binding := range exchange.DefaultBindings() {
		snapshot := FallbackSnapshot(binding.Exchange, binding.Market, time.Now(), "test")

		if !snapshot.TransferFee("BTC").IsPos() {
			t.Fatalf("expected fallback BTC transfer fee for %s", binding.Exchange)
		}
		if !snapshot.TransferFee(binding.Market.Quote).IsPos() {
			t.Fatalf("expected fallback %s transfer reserve for %s", binding.Market.Quote, binding.Exchange)
		}
		if snapshot.Fees.TakerRate.IsNeg() || snapshot.Constraints.MinBase.IsZero() {
			t.Fatalf("expected usable fallback profile for %s", binding.Exchange)
		}
	}
}

func TestMergeBalanceFloorsKeepsDemoMinimums(t *testing.T) {
	floor := FallbackBalances()
	merged := MergeBalanceFloors(map[string]decimal.Decimal{
		"BTC":  decimal.MustNew(1, 1),
		"USDT": decimal.MustNew(500, 0),
	}, floor)

	if !merged["BTC"].Equal(floor["BTC"]) {
		t.Fatalf("expected floor BTC %s, got %s", floor["BTC"], merged["BTC"])
	}
	if !merged["USDT"].Equal(floor["USDT"]) {
		t.Fatalf("expected floor USDT %s, got %s", floor["USDT"], merged["USDT"])
	}
	if !merged["USD"].Equal(floor["USD"]) {
		t.Fatalf("expected floor USD %s, got %s", floor["USD"], merged["USD"])
	}
}

func TestMergeBalanceFloorsUsesHigherAuthenticatedAmounts(t *testing.T) {
	floor := FallbackBalances()
	merged := MergeBalanceFloors(map[string]decimal.Decimal{
		"BTC":  decimal.MustNew(5, 0),
		"USDT": decimal.MustNew(500000, 0),
	}, floor)

	if !merged["BTC"].Equal(decimal.MustNew(5, 0)) {
		t.Fatalf("expected authenticated BTC to win, got %s", merged["BTC"])
	}
	if !merged["USDT"].Equal(decimal.MustNew(500000, 0)) {
		t.Fatalf("expected authenticated USDT to win, got %s", merged["USDT"])
	}
}
