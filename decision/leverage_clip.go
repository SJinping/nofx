package decision

import (
	"fmt"
	"strings"
)

// applyLeverageClip 在 validation 之前将 AI 给出的过高杠杆裁剪到系统配置上限。
// 关闭配置时保持原行为：超限杠杆会被 validation 拒绝。
func applyLeverageClip(decisions []Decision, ctx *Context) {
	if ctx == nil || !ctx.LeverageClip.Enabled || !ctx.LeverageClip.ClipToMax {
		return
	}

	for i := range decisions {
		d := &decisions[i]
		if d.Action != ActionOpenLong && d.Action != ActionOpenShort {
			continue
		}
		if d.Leverage <= 0 {
			continue
		}

		maxLev := ctx.AltcoinLeverage
		if isMajorSymbol(d.Symbol) {
			maxLev = ctx.BTCETHLeverage
		}
		if maxLev <= 0 || d.Leverage <= maxLev {
			continue
		}

		d.OriginalLeverage = d.Leverage
		d.Leverage = maxLev
		d.Reasoning = appendSystemReason(d.Reasoning,
			fmt.Sprintf("系统杠杆裁剪：AI建议%d倍，配置上限%d倍，最终按%d倍验证并执行。", d.OriginalLeverage, maxLev, d.Leverage))
	}
}

func isMajorSymbol(symbol string) bool {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	return sym == "BTCUSDT" || sym == "ETHUSDT"
}

func appendSystemReason(reasoning, note string) string {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return note
	}
	return reasoning + " | " + note
}
