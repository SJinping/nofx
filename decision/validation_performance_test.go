package decision

import (
	"nofx/logger"
	"testing"
)

func TestExtraValidateAllowsModeratelyPoorPerformanceOpenForCautiousEvaluation(t *testing.T) {
	err := ExtraValidate(&Decision{Action: ActionOpenLong}, &Context{
		Performance: &logger.PerformanceAnalysis{
			SharpeRatio:         -0.62,
			CurrentLosingStreak: 5,
		},
	})
	if err != nil {
		t.Fatalf("moderate poor performance should be handled by cautious confidence rules, not a hard opening ban: %v", err)
	}
}
