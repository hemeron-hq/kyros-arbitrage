package terms

import (
	"testing"
	"time"

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
