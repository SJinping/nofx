package decision

import (
	"strings"
	"testing"

	"nofx/market"
)

func TestApplyShortTermStopLossDistanceConfigIsStrategyVScoped(t *testing.T) {
	ctx := &Context{StopLossDistance: DefaultStopLossDistanceConfig()}
	applyShortTermStopLossDistanceConfig(ctx)

	if ctx.StopLossDistance.MajorVolMult != 0 || ctx.StopLossDistance.AltVolMult != 0 {
		t.Fatalf("StrategyV should not use 4h volatility as a stop-loss floor: %+v", ctx.StopLossDistance)
	}
	if ctx.StopLossDistance.MajorMinPct != shortTermMajorMinStopPct || ctx.StopLossDistance.AltMinPct != shortTermAltMinStopPct {
		t.Fatalf("unexpected StrategyV stop-loss distance config: %+v", ctx.StopLossDistance)
	}

	defaultCfg := DefaultStopLossDistanceConfig()
	if defaultCfg.MajorVolMult == 0 || defaultCfg.AltVolMult == 0 {
		t.Fatalf("default StrategyA/B config must remain unchanged: %+v", defaultCfg)
	}
}

func TestValidateShortTermStopGeometryRejectsLargeAutomaticWidening(t *testing.T) {
	d := &Decision{
		Symbol:   "SOLUSDT",
		Action:   ActionOpenLong,
		StopLoss: 99.6,
		Reasoning: "setup_type=trend_pullback; why_now=reclaim; time_stop_minutes=30；" +
			"止损距离自适应调整：SL 99.8000→99.6000，仓位 700.00→350.00 USDT，保持原始名义风险不变（距离 0.2000→0.4000）",
	}
	md := &market.Data{CurrentPrice: 100, IntradayATR14: 1}

	err := validateShortTermStopGeometry(d, md)
	if err == nil || !strings.Contains(err.Error(), "超过允许上限") {
		t.Fatalf("expected excessive widening rejection, got %v", err)
	}
}

func TestValidateShortTermStopGeometryAllowsSmallAdjustment(t *testing.T) {
	d := &Decision{
		Symbol:   "SOLUSDT",
		Action:   ActionOpenLong,
		StopLoss: 99.6,
		Reasoning: "setup_type=trend_pullback; why_now=reclaim; time_stop_minutes=30；" +
			"止损距离自适应调整：SL 99.6900→99.6000，仓位 700.00→542.50 USDT，保持原始名义风险不变（距离 0.3100→0.4000）",
	}
	md := &market.Data{CurrentPrice: 100, IntradayATR14: 1}

	if err := validateShortTermStopGeometry(d, md); err != nil {
		t.Fatalf("expected small StrategyV adjustment to pass, got %v", err)
	}
}

func TestValidateShortTermStopGeometryRejectsWideShortTermStop(t *testing.T) {
	d := &Decision{
		Symbol:   "SOLUSDT",
		Action:   ActionOpenLong,
		StopLoss: 97.5,
	}
	md := &market.Data{CurrentPrice: 100, IntradayATR14: 1}

	err := validateShortTermStopGeometry(d, md)
	if err == nil || !strings.Contains(err.Error(), "短线止损过宽") {
		t.Fatalf("expected max stop-distance rejection, got %v", err)
	}
}
