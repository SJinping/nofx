package reconcile

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/adshao/go-binance/v2/futures"
)

func fill(id, ms int64, side futures.SideType, qty, price, pnl string) *futures.AccountTrade {
	return &futures.AccountTrade{ID: id, Time: ms, Symbol: "ETHUSDT", Side: side, PositionSide: futures.PositionSideTypeShort,
		Quantity: qty, Price: price, RealizedPnl: pnl, Commission: "0.05", CommissionAsset: "USDT"}
}

func TestRebuildAcrossMidnight(t *testing.T) {
	midnight := time.Date(2026, 11, 22, 0, 0, 0, 0, shanghai).UnixMilli()
	open := fill(10, midnight-10, futures.SideTypeSell, "2", "100", "0")
	partial := fill(11, midnight+10, futures.SideTypeBuy, "1", "98", "2")
	funding := &futures.IncomeHistory{TranID: 1, Symbol: "ETHUSDT", Time: midnight, IncomeType: "FUNDING_FEE", Asset: "USDT", Income: "0.01"}
	l := ledger{Fills: []*futures.AccountTrade{partial, open, open}, Income: []*futures.IncomeHistory{funding, funding}}
	if err := l.rebuild(); err != nil {
		t.Fatal(err)
	}
	if len(l.Open) != 1 || len(l.Closed) != 0 || l.Open[0].Remaining.String() != "1" || l.Open[0].Totals.Net["USDT"].String() != "1.91" {
		t.Fatalf("wrong partial close outcome: %+v", l.Open)
	}
	l.Fills = append(l.Fills, fill(12, midnight+20, futures.SideTypeBuy, "1", "99", "1"), partial)
	if err := l.rebuild(); err != nil {
		t.Fatal(err)
	}
	if len(l.Closed) != 1 || len(l.Open) != 0 {
		t.Fatalf("expected one completed trade, got closed=%d open=%d", len(l.Closed), len(l.Open))
	}
	p := l.Closed[0]
	if p.ExitPrice.String() != "98.5" || p.Totals.Net["USDT"].String() != "2.86" || l.CashFlows.Net["USDT"].String() != "2.86" {
		t.Fatalf("wrong settlement: %+v", p)
	}
	if got := l.Daily["2026-11-21"].Net["USDT"].String(); got != "-0.05" {
		t.Errorf("open day net %s", got)
	}
	if got := l.Daily["2026-11-22"].Net["USDT"].String(); got != "2.91" {
		t.Errorf("close day net %s", got)
	}
}

type fakeExchange struct {
	rows []*futures.AccountTrade
	err  error
}

func (f fakeExchange) income(_ context.Context, _, _ int64) ([]*futures.IncomeHistory, error) {
	return []*futures.IncomeHistory{{TranID: 1, IncomeType: "COMMISSION", Symbol: "ETHUSDT", Time: 110}}, nil
}

func (f fakeExchange) positions(context.Context) ([]observedPosition, error) { return nil, nil }
func (f fakeExchange) fills(_ context.Context, symbol string, start, end int64) ([]*futures.AccountTrade, error) {
	var rows []*futures.AccountTrade
	for _, row := range f.rows {
		if row.Symbol == symbol && start <= row.Time && row.Time <= end {
			rows = append(rows, row)
		}
	}
	return rows, f.err
}

func TestSyncCommitsOnlyCompleteBatches(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "V.json")
	ex := fakeExchange{rows: []*futures.AccountTrade{
		fill(1, 120, futures.SideTypeSell, "1", "100", "0"),
		fill(2, 130, futures.SideTypeBuy, "1", "98", "2"),
	}}
	a := account{id: "V", path: path, ex: ex}
	if _, err := syncAccount(ctx, a, 100, 150); err != nil {
		t.Fatal(err)
	}
	// Query the same fills through the overlap, plus an empty slice of new time.
	if _, err := syncAccount(ctx, a, 100, 160); err != nil {
		t.Fatal(err)
	}
	l, err := readLedger(path)
	if err != nil || l.Through != 160 || len(l.Fills) != 2 || len(l.Closed) != 1 {
		t.Fatalf("unexpected checkpoint, fills or trades: %+v %v", l, err)
	}
	ex.err = errors.New("Binance timeout")
	a.ex = ex
	if _, err := syncAccount(ctx, a, 100, 170); !errors.Is(err, ex.err) {
		t.Fatalf("expected exchange error, got %v", err)
	}
	l, err = readLedger(path)
	if err != nil || l.Through != 160 {
		t.Fatalf("failed run overwrote checkpoint: %+v %v", l, err)
	}

}

func TestFetchRangeSplitsAFullPage(t *testing.T) {
	rows, err := fetchRange(10, 12, func(start, end int64) ([]int64, error) {
		if start != end {
			return make([]int64, pageLimit), nil
		}
		return []int64{start}, nil
	})
	if err != nil || len(rows) != 2 || rows[0] != 10 || rows[1] != 11 {
		t.Fatalf("rows %v error %v", rows, err)
	}
}

func TestDisplayHistoryUsesExchangeNetAndKeepsLastSnapshot(t *testing.T) {
	s := &Service{views: map[string]snapshot{
		"V": {meta: Metadata{Source: "local", Status: "pending"}},
	}}
	if history, meta := s.History("V"); history != nil || meta.Source != "local" {
		t.Fatal("expected local stats before the first sync")
	}
	l := ledger{Trader: "V", From: 100, Through: 150, Fills: []*futures.AccountTrade{
		fill(10, 110, futures.SideTypeSell, "1", "100", "0"),
		fill(11, 120, futures.SideTypeBuy, "1", "99", "1"),
	}}
	if err := l.rebuild(); err != nil {
		t.Fatal(err)
	}
	s.publish("V", &l, "ready")
	s.markFailed("V")
	history, meta := s.History("V")
	if len(history) != 1 || history[0].PnL != 0.9 || meta.Source != "binance" ||
		meta.Status != "stale" || meta.Through.UnixMilli() != 150 {
		t.Fatalf("unexpected display history or source: %+v %+v", history, meta)
	}
	s.publish("V", &ledger{From: 100, Through: 150}, "ready")
	if history, _ := s.History("V"); history == nil || len(history) != 0 {
		t.Fatal("a successful empty Binance history must not select the local fallback")
	}
}
