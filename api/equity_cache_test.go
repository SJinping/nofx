package api

import (
	"testing"
	"time"

	"nofx/logger"
)

func TestBuildEquityPointsFiltersInvalidRecordsAndAppliesInitialBalance(t *testing.T) {
	records := []*logger.DecisionRecord{
		{
			Timestamp:   time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
			CycleNumber: 1,
			Success:     true,
			AccountState: logger.AccountSnapshot{
				TotalBalance:          100,
				AvailableBalance:      90,
				TotalUnrealizedProfit: 0,
				PositionCount:         1,
				MarginUsedPct:         10,
			},
		},
		{
			Timestamp:   time.Date(2026, 7, 1, 10, 3, 0, 0, time.UTC),
			CycleNumber: 2,
			Success:     false,
			AccountState: logger.AccountSnapshot{
				TotalBalance: 110,
			},
		},
		{
			Timestamp:   time.Date(2026, 7, 1, 10, 6, 0, 0, time.UTC),
			CycleNumber: 3,
			Success:     true,
			AccountState: logger.AccountSnapshot{
				TotalBalance:          120,
				AvailableBalance:      115,
				TotalUnrealizedProfit: 20,
				PositionCount:         2,
				MarginUsedPct:         20,
			},
		},
	}

	points, err := buildEquityPoints(records, 100)
	if err != nil {
		t.Fatalf("buildEquityPoints returned error: %v", err)
	}

	if len(points) != 2 {
		t.Fatalf("expected 2 valid points, got %d", len(points))
	}
	if points[0].CycleNumber != 1 || points[1].CycleNumber != 3 {
		t.Fatalf("unexpected cycles: %+v", points)
	}
	if points[1].TotalPnLPct != 20 {
		t.Fatalf("expected 20%% pnl, got %.2f", points[1].TotalPnLPct)
	}
}

func TestApplyEquityHistoryQueryLimitsNewestPointsAndCycleRange(t *testing.T) {
	points := []EquityPoint{
		{CycleNumber: 1},
		{CycleNumber: 2},
		{CycleNumber: 3},
		{CycleNumber: 4},
		{CycleNumber: 5},
	}

	filtered := applyEquityHistoryQuery(points, EquityHistoryQuery{Limit: 2, FromCycle: 2, ToCycle: 5})
	if len(filtered) != 2 {
		t.Fatalf("expected 2 points, got %d", len(filtered))
	}
	if filtered[0].CycleNumber != 4 || filtered[1].CycleNumber != 5 {
		t.Fatalf("expected newest cycles 4,5; got %+v", filtered)
	}
}

func TestEquityHistoryCacheReusesFreshData(t *testing.T) {
	calls := 0
	cache := NewEquityHistoryCache(10, time.Minute)
	fetch := func(limit int) ([]*logger.DecisionRecord, error) {
		calls++
		return []*logger.DecisionRecord{
			{
				Timestamp:   time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
				CycleNumber: calls,
				Success:     true,
				AccountState: logger.AccountSnapshot{
					TotalBalance: 100,
				},
			},
		}, nil
	}

	if _, err := cache.Get("trader-a", fetch, 100, 10); err != nil {
		t.Fatalf("first get failed: %v", err)
	}
	if _, err := cache.Get("trader-a", fetch, 100, 10); err != nil {
		t.Fatalf("second get failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected fetch once for fresh cache, got %d", calls)
	}
}

func TestEquityHistoryCacheReusesFallbackInitialBalance(t *testing.T) {
	calls := 0
	cache := NewEquityHistoryCache(10, time.Minute)
	fetch := func(limit int) ([]*logger.DecisionRecord, error) {
		calls++
		return []*logger.DecisionRecord{
			{
				Timestamp:   time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
				CycleNumber: calls,
				Success:     true,
				AccountState: logger.AccountSnapshot{
					TotalBalance: 100,
				},
			},
		}, nil
	}

	if _, err := cache.Get("trader-a", fetch, 0, 10); err != nil {
		t.Fatalf("first get failed: %v", err)
	}
	if _, err := cache.Get("trader-a", fetch, 0, 10); err != nil {
		t.Fatalf("second get failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected fallback initial balance cache to fetch once, got %d", calls)
	}
}

func TestEquityHistoryCacheKeepsFallbackInitialBalanceOnRefresh(t *testing.T) {
	calls := 0
	cache := NewEquityHistoryCache(10, time.Nanosecond)
	fetch := func(limit int) ([]*logger.DecisionRecord, error) {
		calls++
		if calls == 1 {
			return []*logger.DecisionRecord{
				{
					Timestamp:   time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
					CycleNumber: 1,
					Success:     true,
					AccountState: logger.AccountSnapshot{
						TotalBalance: 100,
					},
				},
			}, nil
		}
		return []*logger.DecisionRecord{
			{
				Timestamp:   time.Date(2026, 7, 1, 10, 3, 0, 0, time.UTC),
				CycleNumber: 2,
				Success:     true,
				AccountState: logger.AccountSnapshot{
					TotalBalance:          120,
					TotalUnrealizedProfit: 20,
				},
			},
		}, nil
	}

	if _, err := cache.Get("trader-a", fetch, 0, 10); err != nil {
		t.Fatalf("first get failed: %v", err)
	}
	time.Sleep(time.Millisecond)
	points, err := cache.Get("trader-a", fetch, 0, 10)
	if err != nil {
		t.Fatalf("second get failed: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("expected 2 points after refresh, got %d", len(points))
	}
	if points[1].TotalPnLPct != 20 {
		t.Fatalf("expected cached initial balance to keep pnl pct at 20, got %.2f", points[1].TotalPnLPct)
	}
}

func TestEquityHistoryCacheFetchesOnlyRequestedLimitAndExpandsOnDemand(t *testing.T) {
	var requested []int
	cache := NewEquityHistoryCache(20, time.Minute)
	fetch := func(limit int) ([]*logger.DecisionRecord, error) {
		requested = append(requested, limit)
		records := make([]*logger.DecisionRecord, 0, limit)
		for i := 1; i <= limit; i++ {
			records = append(records, &logger.DecisionRecord{
				Timestamp:    time.Date(2026, 7, 1, 10, i, 0, 0, time.UTC),
				CycleNumber:  i,
				Success:      true,
				AccountState: logger.AccountSnapshot{TotalBalance: 100},
			})
		}
		return records, nil
	}

	if points, err := cache.Get("trader-a", fetch, 100, 2); err != nil || len(points) != 2 {
		t.Fatalf("first get points=%d err=%v", len(points), err)
	}
	if points, err := cache.Get("trader-a", fetch, 100, 10); err != nil || len(points) != 10 {
		t.Fatalf("expanded get points=%d err=%v", len(points), err)
	}
	if len(requested) != 2 || requested[0] != 2 || requested[1] != 10 {
		t.Fatalf("expected fetch limits [2 10], got %+v", requested)
	}
}
