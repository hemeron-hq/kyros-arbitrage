package dashboard

import (
	"bytes"
	"context"
	"testing"

	"github.com/hemeron-hq/kyros-arbitrage/internal/ui/dashboard/historic"
)

func TestPageRendersHistoricTabWithPager(t *testing.T) {
	model := Model{
		Title:          "Kyros Arbitrage",
		HistoricActive: true,
		Historic: historic.Model{
			Status:           "persisted",
			Path:             "file:test.db",
			OpportunityCount: "1",
			ExecutionCount:   "1",
			TotalPnl:         "0.00",
			Page: historic.PageView{
				Label:   "1-1 / 2",
				PrevURL: "/?tab=historic&history_limit=25&history_offset=0",
				NextURL: "/?tab=historic&history_limit=25&history_offset=25",
				HasNext: true,
			},
		},
	}

	var buf bytes.Buffer
	if err := Page(model).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
}
