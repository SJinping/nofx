package api

import (
	"errors"
	"nofx/logger"
	"testing"
	"time"
)

func TestIndexedEquityCacheQueryIsolationAndClone(t *testing.T) {
	c := NewEquityHistoryCache(20000, time.Minute)
	calls := 0
	fetch := func(limit, from, to int, actions bool) ([]logger.ChartRecord, error) {
		calls++
		if limit != 2 || from != 4 || to != 8 {
			t.Fatalf("query lost before source filtering: %d %d %d", limit, from, to)
		}
		p := logger.ChartRecord{CycleNumber: 8, Success: true, AccountState: logger.AccountSnapshot{TotalBalance: 120, TotalUnrealizedProfit: 20}}
		if actions {
			p.Actions = []logger.ChartAction{{Action: "open_long", Success: true, Price: 42}}
		}
		return []logger.ChartRecord{p}, nil
	}
	q := EquityHistoryQuery{Limit: 2, FromCycle: 4, ToCycle: 8, IncludeActions: true}
	p, err := c.GetIndexed("a", fetch, 100, q)
	if err != nil || len(p) != 1 || p[0].TotalPnLPct != 20 || len(p[0].Actions) != 1 {
		t.Fatalf("wrong points: %+v %v", p, err)
	}
	p[0].Actions[0].Price = 0
	p, err = c.GetIndexed("a", fetch, 100, q)
	if err != nil || calls != 1 || p[0].Actions[0].Price != 42 {
		t.Fatalf("cache mutation: %+v %v calls=%d", p, err, calls)
	}
	q.IncludeActions = false
	p, err = c.GetIndexed("a", fetch, 100, q)
	if err != nil || calls != 2 || len(p[0].Actions) != 0 {
		t.Fatalf("actions leaked: %+v %v", p, err)
	}
	_, err = c.GetIndexed("a", fetch, 200, q)
	if err != nil || calls != 3 {
		t.Fatal("initial balance must be part of key")
	}
}

func TestIndexedEquityPendingNotCachedAndRefreshDoesNotMissGaps(t *testing.T) {
	c := NewEquityHistoryCache(20000, time.Nanosecond)
	calls := 0
	fetch := func(limit, from, to int, actions bool) ([]logger.ChartRecord, error) {
		calls++
		if calls == 1 {
			return nil, logger.ErrChartIndexPending
		}
		if limit != 20000 {
			t.Fatalf("refresh window can miss gaps: %d", limit)
		}
		return []logger.ChartRecord{{Success: true, CycleNumber: 1, AccountState: logger.AccountSnapshot{TotalBalance: 100}}, {Success: true, CycleNumber: 502, AccountState: logger.AccountSnapshot{TotalBalance: 120}}}, nil
	}
	q := EquityHistoryQuery{Limit: 0}
	if _, err := c.GetIndexed("a", fetch, 100, q); !errors.Is(err, logger.ErrChartIndexPending) {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		p, err := c.GetIndexed("a", fetch, 0, q)
		if err != nil || len(p) != 2 {
			t.Fatalf("%+v %v", p, err)
		}
	}
	if calls != 3 {
		t.Fatalf("expected retry/refresh got %d", calls)
	}
}

func TestApplyEquityHistoryQueryLimitsNewestPointsAndCycleRange(t *testing.T) {
	p := applyEquityHistoryQuery([]EquityPoint{{CycleNumber: 1}, {CycleNumber: 3}, {CycleNumber: 5}}, EquityHistoryQuery{Limit: 1, ToCycle: 3})
	if len(p) != 1 || p[0].CycleNumber != 3 {
		t.Fatal(p)
	}
}
