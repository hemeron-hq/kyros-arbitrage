package terms

import (
	"testing"
	"time"

	"github.com/hemeron-hq/kyros-arbitrage/internal/exchange"
)

func TestFallbackSnapshotIncludesTransferFees(t *testing.T) {
	snapshot := FallbackSnapshot(exchange.Binance, exchange.Market{Base: "BTC", Quote: "USDT"}, time.Now(), "test")

	if !snapshot.TransferFee("BTC").IsPos() {
		t.Fatal("expected fallback BTC transfer fee")
	}
	if !snapshot.TransferFee("USDT").IsPos() {
		t.Fatal("expected fallback USDT transfer fee")
	}
}
