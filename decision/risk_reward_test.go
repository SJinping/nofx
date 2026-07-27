package decision

import (
	"math"
	"testing"

	"nofx/market"
)

func TestEstimateDecisionNetRiskRewardUsesMarketPriceAndCosts(t *testing.T) {
	ctx := &Context{
		MarketDataMap: map[string]*market.Data{
			"ETHUSDT": {CurrentPrice: 100},
		},
		AssumedTakerFeeRate: 0.0004,
		AssumedSlippageRate: 0.0005,
	}
	dec := &Decision{
		Symbol:     "ethusdt",
		Action:     ActionOpenLong,
		StopLoss:   98,
		TakeProfit: 106,
		Leverage:   5,
	}

	est := EstimateDecisionNetRiskReward(ctx, dec)
	if !est.Valid {
		t.Fatalf("expected valid estimate, got invalid reason %q", est.InvalidReason)
	}
	if est.EntryPriceSource != "market_data" || est.EntryPrice != 100 {
		t.Fatalf("unexpected entry: price=%v source=%s", est.EntryPrice, est.EntryPriceSource)
	}
	assertAlmostEqual(t, est.RawRiskPct, 2.0)
	assertAlmostEqual(t, est.RawRewardPct, 6.0)
	assertAlmostEqual(t, est.RoundTripCostROIPct, 0.9)
	assertAlmostEqual(t, est.NetRiskPct, 10.9)
	assertAlmostEqual(t, est.NetRewardPct, 29.1)
	assertAlmostEqual(t, est.NetRiskRewardRatio, 29.1/10.9)
}

func TestEstimateNetRiskRewardFallbackEntryForShort(t *testing.T) {
	est := EstimateNetRiskReward(nil, "BTCUSDT", ActionOpenShort, 110, 60, 2)
	if !est.Valid {
		t.Fatalf("expected valid estimate, got invalid reason %q", est.InvalidReason)
	}
	if est.EntryPriceSource != "fallback" || est.EntryPrice != 100 {
		t.Fatalf("unexpected fallback entry: price=%v source=%s", est.EntryPrice, est.EntryPriceSource)
	}
	assertAlmostEqual(t, est.RawRiskPct, 10.0)
	assertAlmostEqual(t, est.RawRewardPct, 40.0)
}

func TestEstimateNetRiskRewardRejectsInvalidGeometry(t *testing.T) {
	est := EstimateNetRiskReward(nil, "BTCUSDT", ActionOpenLong, 110, 90, 5)
	if est.Valid {
		t.Fatalf("expected invalid estimate")
	}
	if est.InvalidReason != "invalid_risk_or_reward" {
		t.Fatalf("unexpected invalid reason: %q", est.InvalidReason)
	}
}

func assertAlmostEqual(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}
