package decision

import (
	"testing"

	"nofx/market"
)

func TestValidateDecisionsKeepsValidActionsWhenAnotherActionIsInvalid(t *testing.T) {
	decisions := []Decision{
		{Symbol: "BNBUSDT", Action: ActionUpdateStopLoss, NewStopLoss: 99.9, Reasoning: "too close"},
		{Symbol: "ETHUSDT", Action: ActionHold, Reasoning: "valid independent action"},
	}
	ctx := &Context{
		Positions: []PositionInfo{{Symbol: "BNBUSDT", Side: "long"}},
		MarketDataMap: map[string]*market.Data{
			"BNBUSDT": {Symbol: "BNBUSDT", CurrentPrice: 100, IntradayATR14: 1},
		},
		StopLossDistance: StopLossDistanceConfig{AltMinPct: 0.004, AltATRMult: 0.7, AltVolMult: 0.5},
	}
	validateDecisions(decisions, ctx)
	if decisions[0].ValidationError == "" {
		t.Fatal("expected BNB validation error")
	}
	if decisions[1].ValidationError != "" {
		t.Fatalf("valid ETH action rejected: %s", decisions[1].ValidationError)
	}
}
