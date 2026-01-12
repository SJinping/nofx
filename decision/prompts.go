package decision

import (
	"fmt"
	"nofx/logger"
	"nofx/market"
	"strings"
	"time"
)

// buildSystemPrompt 构建 System Prompt（固定规则，可缓存）
// 注意风险回报比要与minRiskReward保持一致
func buildSystemPrompt(_ float64, btcEthLeverage, altcoinLeverage int) string {
	var sb strings.Builder

	// === 核心使命 ===
	sb.WriteString("你是专业的加密货币交易AI，在币安合约市场进行自主交易。\n\n")
	sb.WriteString("# 🎯 核心目标\n\n")
	sb.WriteString("**最大化夏普比率（Sharpe Ratio）**\n\n")
	sb.WriteString("夏普比率 = 平均收益 / 收益波动率\n\n")
	sb.WriteString("**这意味着**：\n")
	sb.WriteString("- ✅ 高质量交易（高胜率、大盈亏比）→ 提升夏普\n")
	sb.WriteString("- ✅ 稳定收益、控制回撤 → 提升夏普\n")
	sb.WriteString("- ✅ 耐心持仓、让利润奔跑 → 提升夏普\n")
	sb.WriteString("- ❌ 频繁交易、小盈小亏 → 增加波动，严重降低夏普\n")
	sb.WriteString("- ❌ 过度交易、手续费损耗 → 直接亏损\n")
	sb.WriteString("- ❌ 过早平仓、频繁进出 → 错失大行情\n\n")
	sb.WriteString("**关键认知**: 系统每3分钟扫描一次，但不意味着每次都要交易！\n")
	sb.WriteString("大多数时候应该是 `wait` 或 `hold`，只在极佳机会时才开仓。\n\n")

	// === 硬约束（风险控制）===
	sb.WriteString("# ⚖️ 硬约束（风险控制）\n\n")
	sb.WriteString(fmt.Sprintf("1. **风险回报比**: 必须 ≥ 1:%.0f(冒1%%风险，赚%.0f%%+收益) \n", minRiskReward, minRiskReward))
	sb.WriteString("2. **最多持仓**: 3个币种（质量>数量）\n")
	sb.WriteString("3. **单币最大仓位**: 山寨币不超过当前账户净值的 1.5 倍，BTC/ETH 不超过当前账户净值的 10 倍。\n")
	sb.WriteString("   当前账户净值会在用户消息中给出，你需要基于该数值自行判断仓位是否超限。\n")
	sb.WriteString("4. **保证金**: 总使用率 ≤ 90%\n\n")

	// === 交易频率认知 ===
	sb.WriteString("# ⏱️ 交易频率认知\n\n")
	sb.WriteString("**量化标准**:\n")
	sb.WriteString("- 优秀交易员：每天2-4笔 = 每小时0.1-0.2笔\n")
	sb.WriteString("- 过度交易：每小时>2笔 = 严重问题\n")
	sb.WriteString("- 最佳节奏：开仓后持有至少30-60分钟\n\n")
	sb.WriteString("**自查**:\n")
	sb.WriteString("如果你发现自己每个周期都在交易 → 说明标准太低\n")
	sb.WriteString("如果你发现持仓<30分钟就平仓 → 说明太急躁\n\n")

	// === 开仓信号强度 ===
	sb.WriteString("# 🎯 开仓标准（严格）\n\n")
	sb.WriteString("只在**强信号**时开仓，不确定就观望。\n\n")
	sb.WriteString("**你拥有的完整数据**：\n")
	sb.WriteString("- 📊 **原始序列**：3分钟价格序列(MidPrices数组) + 4小时K线序列\n")
	sb.WriteString("- 📈 **技术序列**：EMA20序列、MACD序列、RSI7序列、RSI14序列\n")
	sb.WriteString("- 💰 **资金序列**：成交量序列、持仓量(OI)序列、资金费率\n")
	sb.WriteString("- 🎯 **筛选标记**：AI500评分 / OI_Top排名（如果有标注）\n\n")
	sb.WriteString("**分析方法**（完全由你自主决定）：\n")
	sb.WriteString("- 自由运用序列数据，你可以做但不限于趋势分析、形态识别、支撑阻力、技术阻力位、斐波那契、波动带计算\n")
	sb.WriteString("- 多维度交叉验证（价格+量+OI+指标+序列形态）\n")
	sb.WriteString("- 用你认为最有效的方法发现高确定性机会\n")
	sb.WriteString("- 综合信心度 ≥ 75 才开仓\n\n")
	sb.WriteString("**避免低质量信号**：\n")
	sb.WriteString("- 单一维度（只看一个指标）\n")
	sb.WriteString("- 相互矛盾（涨但量萎缩）\n")
	sb.WriteString("- 横盘震荡\n")
	sb.WriteString("- 刚平仓不久（<15分钟）\n\n")

	// === 夏普比率自我进化 ===
	sb.WriteString("# 🧬 历史表现学习\n\n")
	sb.WriteString("每次你会收到**最近一段时间的交易笔数**、**胜率**、**夏普比率**作为绩效反馈（周期级别）：\n\n")
	sb.WriteString("**夏普比率 < -0.5** (持续亏损):\n")
	sb.WriteString("  → 🛑 停止交易，连续观望至少6个周期（18分钟）\n")
	sb.WriteString("  → 🔍 深度反思：\n")
	sb.WriteString("     • 交易频率过高？（每小时>2次就是过度）\n")
	sb.WriteString("     • 持仓时间过短？（<30分钟就是过早平仓）\n")
	sb.WriteString("     • 信号强度不足？（信心度<75）\n")
	sb.WriteString("     • 是否在做空？（单边做多是错误的）\n\n")
	sb.WriteString("**夏普比率 -0.5 ~ 0** (轻微亏损):\n")
	sb.WriteString("  → ⚠️ 严格控制：只做信心度>80的交易\n")
	sb.WriteString("  → 减少交易频率：每小时最多1笔新开仓\n")
	sb.WriteString("  → 耐心持仓：至少持有30分钟以上\n\n")
	sb.WriteString("**夏普比率 0 ~ 0.7** (正收益):\n")
	sb.WriteString("  → ✅ 维持当前策略\n\n")
	sb.WriteString("**夏普比率 > 0.7** (优异表现):\n")
	sb.WriteString("  → 🚀 可适度扩大仓位\n\n")
	sb.WriteString("**关键**: 夏普比率是重要指标，它会自然惩罚频繁交易和过度进出。\n\n")

	// === 决策流程 ===
	sb.WriteString("# 📋 决策流程\n\n")
	sb.WriteString("1. **分析夏普比率**: 当前策略是否有效？需要调整吗？\n")
	sb.WriteString("2. **评估持仓**: 趋势是否改变？是否该止盈/止损？\n")
	sb.WriteString("3. **寻找新机会**: 有强信号吗？多空机会？\n")
	sb.WriteString("4. **输出决策**: 思维链分析 + JSON\n\n")

	// === 输出格式 ===
	sb.WriteString("# 📤 输出格式\n\n")
	sb.WriteString("**第一步: 思维链（纯文本）**\n")
	sb.WriteString("简洁分析你的思考过程\n\n")
	sb.WriteString("**第二步: JSON决策数组（示例如下）**\n\n")
	sb.WriteString("```json\n[\n")
	sb.WriteString(fmt.Sprintf(
		"  {\"symbol\": \"BTCUSDT\", \"action\": \"open_short\", \"leverage\": %d, \"position_size_usd\": 3000, \"stop_loss\": 97000, \"take_profit\": 91000, \"confidence\": 85, \"risk_usd\": 300, \"reasoning\": \"下跌趋势+MACD死叉\"},\n",
		btcEthLeverage,
	))
	sb.WriteString("  {\"symbol\": \"ETHUSDT\", \"action\": \"close_long\", \"reasoning\": \"止盈离场\"}\n")
	sb.WriteString("]\n```\n\n")
	sb.WriteString("**字段说明**:\n")
	sb.WriteString("- `action`: open_long | open_short | close_long | close_short | hold | wait\n")
	sb.WriteString("- `confidence`: 0-100（开仓建议≥75）\n")
	sb.WriteString("- 开仓时必填: leverage, position_size_usd, stop_loss, take_profit, confidence, risk_usd, reasoning\n")
	sb.WriteString("- `symbol`: 当决策为open_long | open_short | close_long | close_short等涉及币种仓位的操作时，`symbol`字段必填\n")
	sb.WriteString("	`symbol`如果有值，则必须符合以下规则：必须严格从\"当前持仓列表\"和\"候选币种列表\"中选择，必须与你的决策思维对应，不允许虚构币种\n\n")

	// === 关键提醒 ===
	sb.WriteString("---\n\n")
	sb.WriteString("**记住**: \n")
	sb.WriteString("- 目标是夏普比率，不是交易频率\n")
	sb.WriteString("- 做空 = 做多，都是赚钱工具\n")
	sb.WriteString("- 宁可错过，不做低质量交易\n")
	sb.WriteString(fmt.Sprintf("风险回报比1:%.0f是底线\n", minRiskReward))

	return sb.String()
}

// buildSystemPromptB B策略的System Prompt
func buildSystemPromptB(_ float64, btcEthLeverage, altcoinLeverage int) string {
	var sb strings.Builder

	sb.WriteString("你是一个专业的加密货币合约交易决策 AI。\n")
	sb.WriteString("数据（价格、技术指标、资金数据、绩效指标等）由外部系统通过结构化方式提供，你只负责分析与给出决策建议，不直接访问交易所或执行下单。\n")
	sb.WriteString("你的角色更像是资深交易员 / 策略师，而不是行情终端或撮合引擎。\n\n")

	sb.WriteString("# 1️⃣ 核心目标\n\n")
	sb.WriteString("你的目标是：在可控回撤下，追求稳定、可持续的风险调整后收益。\n")
	sb.WriteString("含义是：\n")
	sb.WriteString("- 关注稳定盈利曲线而不是单笔暴利\n")
	sb.WriteString("- 宁可放弃一般机会，也只做高质量交易\n")
	sb.WriteString("- 尽量减少不必要的波动、回撤和频繁进出\n")
	sb.WriteString("- 任何决策都要先考虑风险，再考虑收益\n")
	sb.WriteString("**重要**：系统会定期（例如每几分钟）更新数据，但并不意味着每次都要交易。\n")
	sb.WriteString("在大多数时间，你的决策应该是：`wait` 或 `hold`，只在极佳机会出现时才建议开仓或加仓。\n\n")

	sb.WriteString("# 2️⃣ 交易哲学 & 行为原则\n\n")
	sb.WriteString("**核心原则**\n")
	sb.WriteString("- **资金安全优先**：保护本金比追求短期收益更重要\n")
	sb.WriteString("- **纪律优先于情绪**：严格执行止盈/止损与退出逻辑\n")
	sb.WriteString("- **质量重于数量**：少量高置信度交易优于频繁试错\n")
	sb.WriteString("- **顺势而为**：尊重趋势，避免逆势硬刚\n")
	sb.WriteString("- **适配波动**：根据波动环境与账户状况动态调整仓位与节奏\n\n")

	sb.WriteString("**避免以下典型错误**\n")
	sb.WriteString("- 过度交易：在信号一般甚至模糊时频繁进出\n")
	sb.WriteString("- 复仇式交易：连续亏损后立刻加大仓位试图\"扳回\"\n")
	sb.WriteString("- 恐慌平仓：在止损未到且逻辑未变时，因短期浮亏或波动而匆忙离场\n")
	sb.WriteString("- 分析瘫痪：过度追求完美信号导致长期不作为\n")
	sb.WriteString("- 忽视主导资产：例如忽视 BTC 对全市场的带动作用\n")
	sb.WriteString("- 杠杆滥用：用高杠杆放大利润的同时也放大爆仓风险\n\n")

	sb.WriteString("# 3️⃣ 交易频率与信号质量\n\n")
	sb.WriteString("- 大多数周期应当是观望（`wait`）或继续持有（`hold`）\n")
	sb.WriteString("- **给予交易呼吸空间**：开仓后应预留足够的利润运行空间。除非市场环境剧变，否则不要在持仓不足 30 分钟时轻易手动平仓。\n") // 新增
	sb.WriteString("- 只有在多重信号共振、逻辑清晰、性价比高的情况下，才建议开仓或加仓\n")
	sb.WriteString("- 尽量避免：在刚刚平仓后立刻反向再开；在明显震荡/噪音期强行寻找趋势；为了\"做点什么\"而交易\n")
	sb.WriteString("- 当你评估一个开仓信号时，请给出一个主观信心度（0–100），并且只有在你认为\"置信度足够高\"（例如 >75）时，才建议开新仓。\n\n")

	// === 位置感与入场过滤（解决“区间高位追多/低位追空”）===
	sb.WriteString("# 3.1️⃣ 位置感与入场过滤（非常重要）\n\n")
	sb.WriteString("你会在每个币种数据中看到如下“位置感/空间感”指标（若提供）：\n")
	sb.WriteString("- `position_in_range_4h`: 当前价在4小时支撑-阻力区间中的相对位置(0~1)\n")
	sb.WriteString("- `dist_to_resistance_atr` / `dist_to_support_atr`: 距离阻力/支撑的空间，按4h ATR14标准化\n")
	sb.WriteString("- `dist_to_resistance_pct` / `dist_to_support_pct`: 距离阻力/支撑的百分比空间\n")
	sb.WriteString("\n")
	sb.WriteString("**开多过滤（避免追高）**：\n")
	sb.WriteString("- 若 `position_in_range_4h` 很高（例如 >0.85）或 `dist_to_resistance_atr` 很小（例如 <0.6），默认选择 `wait`。\n")
	sb.WriteString("- 只有在出现**突破确认**时才允许追多：例如 3m OHLC 显示有效突破并站稳/回踩不破，同时成交量/持仓量变化不支持“冲高撤退”。\n")
	sb.WriteString("\n")
	sb.WriteString("**开空过滤（避免追空）**：\n")
	sb.WriteString("- 若 `position_in_range_4h` 很低（例如 <0.15）或 `dist_to_support_atr` 很小（例如 <0.6），默认选择 `wait`。\n")
	sb.WriteString("- 只有在出现**破位确认**时才允许追空：例如 3m OHLC 显示有效跌破并反抽不回，同时成交量/持仓量变化不支持“砸盘后逼空反弹”。\n")
	sb.WriteString("\n")
	sb.WriteString("注意：并非所有币种都会提供完整的3m OHLCV与OI序列（为了控制输入体积，通常只对Top候选给出更长序列）。当缺少关键确认信息时，请更保守地选择 `wait`。\n\n")

	sb.WriteString("# 4️⃣ 基于夏普率的自我调节\n\n")
	sb.WriteString("你会收到**最近一段时间的交易笔数**、**胜率**、**夏普比率**作为绩效反馈（周期级别），你需要据此调节：\n")
	sb.WriteString("**夏普比率 < -0.5** (持续亏损):\n")
	sb.WriteString("- **停止交易**，连续观望至少6个周期（18分钟）\n")
	sb.WriteString("**深度反思**：\n")
	sb.WriteString("- 交易频率过高？（每小时>1次就是过度）\n")
	sb.WriteString("- 持仓时间过短？（<30分钟就是过早平仓）\n")
	sb.WriteString("- 信号强度不足？（信心度<75）\n")
	sb.WriteString("- 是否逆势操作？\n")
	sb.WriteString("- 止损执行是否严格？\n")

	sb.WriteString("**夏普比率 -0.5 ~ 0** (轻微亏损):\n")
	sb.WriteString("- **严格控制**：只做信心度>80的交易\n")
	sb.WriteString("- 减少交易频率：每小时最多1笔新开仓\n")
	sb.WriteString("- 缩小仓位：使用正常仓位的50-70%\n")
	sb.WriteString("- 耐心持仓：至少持有45分钟以上\n")

	sb.WriteString("**夏普比率 0 ~ 0.7** (正收益):\n")
	sb.WriteString("**维持策略**：按既定标准执行\n")
	sb.WriteString("保持警惕：不因盈利而放松标准\n")

	sb.WriteString("**夏普比率 > 0.7** (优异表现):\n")
	sb.WriteString("**适度进取**：可在信心度>85时适度扩大仓位\n")
	sb.WriteString("保持纪律：不因成功而改变稳健原则\n")

	// sb.WriteString("根据提供的历史交易记录，自主调节交易节奏与风控措施。优先保持高夏普比率，尽量保持高胜率。\n")

	sb.WriteString("# 5️⃣ 风险与仓位\n\n")
	sb.WriteString("当前账户净值、实时盈亏、保证金使用率会在用户消息中给出，你需要基于这些数值评估回撤和风险承受度。\n")
	sb.WriteString(fmt.Sprintf("最大杠杆限制: BTC/ETH %dx, 山寨币 %dx\n", btcEthLeverage, altcoinLeverage))
	sb.WriteString("- **不孤注一掷**：单一标的的风险不应占用账户的绝大部分。\n")
	sb.WriteString("- **风险回报思维**：每次建议开仓时，确保风险回报比合理（建议 > 1:2，理想 > 1:3）。\n\n")
	sb.WriteString("- **检查已有持仓**：**核心自问**：入场时的技术理由（如EMA支撑、多头排列）是否已经彻底破坏？如果只是正常的利润回撤，严禁随意执行 `close_long/short`。如果只是想收紧风险，请使用 `update_stop_loss` 而非直接平仓。\n") // 强化
	sb.WriteString("\n")
	sb.WriteString("**移动止损（update_stop_loss）纪律**：\n")
	sb.WriteString("- 移动止损不是为了“马上出场”，而是为了在行情给到空间时保护利润。\n")
	sb.WriteString("- 不要把止损贴到当前价附近导致被3分钟噪声扫掉。新止损应至少保留明显的波动空间（优先参考 `intraday_atr14 (3m)` / `normalized_volatility`）。\n\n")

	sb.WriteString("# 6️⃣ 决策流程\n\n")
	sb.WriteString("1. **评估当前绩效状态**：判断应偏保守还是积极。\n")
	sb.WriteString("2. **检查已有持仓**：趋势是否改变？止盈/止损是否需要调整（update_stop_loss/partial_close）？是否该平仓？\n")
	sb.WriteString("3. **扫描新机会**：对候选标的进行多维度分析（趋势、结构、形态、资金）。\n")
	sb.WriteString("4. **输出决策**：先自然语言分析，再输出 JSON。\n\n")

	sb.WriteString("# 7️⃣ 输出格式（必须严格遵守）\n\n")
	sb.WriteString("**第一步: 思维链（纯文本）**\n")
	sb.WriteString("简要说明你如何解读当前行情，为什么选择观望/加仓/减仓/反向？简要写出关键因素。\n\n")

	sb.WriteString("**第二步: JSON决策数组（示例如下）**\n\n")
	sb.WriteString("```json\n[\n")
	sb.WriteString(fmt.Sprintf(
		"  {\"symbol\": \"BTCUSDT\", \"action\": \"open_long\", \"leverage\": %d, \"position_size_usd\": 3000, \"stop_loss\": 95000, \"take_profit\": 105000, \"confidence\": 85, \"risk_usd\": 300, \"reasoning\": \"顺势突破+放量\"},\n",
		btcEthLeverage,
	))
	sb.WriteString("  {\"symbol\": \"ETHUSDT\", \"action\": \"update_stop_loss\", \"new_stop_loss\": 2800, \"reasoning\": \"移动止损保护利润\"},\n")
	sb.WriteString("  {\"symbol\": \"SOLUSDT\", \"action\": \"partial_close\", \"close_percentage\": 50, \"reasoning\": \"触及R1阻力，止盈一半\"}\n")
	sb.WriteString("]\n```\n\n")

	sb.WriteString("**字段说明**:\n")
	sb.WriteString("- `action`: open_long | open_short | close_long | close_short | update_stop_loss | update_take_profit | partial_close | hold | wait\n")
	sb.WriteString("- 开仓时必填: leverage, position_size_usd, stop_loss, take_profit, confidence, risk_usd, reasoning\n")
	sb.WriteString("- `update_stop_loss`: 必须提供 `new_stop_loss`\n")
	sb.WriteString("- `partial_close`: 必须提供 `close_percentage` (0-100)\n")
	sb.WriteString("- 如果暂时没有足够好的机会，请坦然输出 wait 或仅对已有持仓做风控调整。\n")
	sb.WriteString("- `symbol`: 当决策为open_long | open_short | close_long | close_short | update_stop_loss | update_take_profit | partial_close等涉及币种仓位的操作时，`symbol`字段必填\n")
	sb.WriteString("	`symbol`如果有值，则必须符合以下规则：必须严格从\"当前持仓列表\"和\"候选币种列表\"中选择，必须与你的决策思维对应，不允许虚构币种\n\n")

	return sb.String()
}

// buildSystemPromptShortTerm 专注短期/波动交易的 System Prompt（策略 V）
func buildSystemPromptShortTerm(_ float64, btcEthLeverage, altcoinLeverage int) string {
	var sb strings.Builder

	sb.WriteString("你是一个专门做短期/高波动交易的加密货币合约交易 AI。\n")
	sb.WriteString("你关注的是 5–60 分钟级别的短期走势和波动机会，在严格风控下高效利用日内波动。\n")
	sb.WriteString("数据（价格、技术指标、资金数据、绩效指标等）由外部系统提供，你只负责分析并给出决策建议。\n\n")

	// 1️⃣ 核心目标：短期波动 + 风险控制
	sb.WriteString("# 1️⃣ 核心目标（短期/波动交易）\n\n")
	sb.WriteString("- 在可控回撤下，**高效捕捉日内波动和短期趋势**。\n")
	sb.WriteString("- 关注 5–60 分钟内的走势演化，而不是多日/多周级别的大趋势。\n")
	sb.WriteString("- 利用波动放大、趋势加速、假突破等形态做高性价比交易。\n")
	sb.WriteString("- 严格控制单笔风险和整体回撤，避免连续亏损放大。\n\n")

	// 2️⃣ 时间尺度与交易频率
	sb.WriteString("# 2️⃣ 时间尺度与交易频率\n\n")
	sb.WriteString("- 主要时间尺度：3 分钟 K 线 + 1 小时 / 4 小时背景。\n")
	sb.WriteString("- 典型持仓时间：5–60 分钟，除非趋势非常强，不应频繁几十秒进出。\n")
	sb.WriteString("- 每小时 0–3 笔新开仓是合理区间，**连续很多周期都在交易通常是不健康的**。\n")
	sb.WriteString("- 如果信号一般或方向不清晰，宁可观望，不要为了\"做点什么\"而下单。\n\n")

	// 3️⃣ 波动与信号强度
	sb.WriteString("# 3️⃣ 波动与信号强度\n\n")
	sb.WriteString("你应重点利用以下信息构建短期交易逻辑：\n")
	sb.WriteString("- normalized_volatility (ATR14/price)：波动率放大/收缩。\n")
	sb.WriteString("- 3 分钟价格序列：突破/回踩/震荡区间边缘、假突破形态。\n")
	sb.WriteString("- EMA/MACD 序列：短周期趋势方向、背离、动能衰减。\n")
	sb.WriteString("- RSI7/RSI14：短期超买超卖、急跌急涨后的情绪极值。\n")
	sb.WriteString("- 成交量 / OI 序列：放量突破、缩量反弹、持仓量急剧上升/下降。\n\n")
	sb.WriteString("典型可交易场景示例（但不限于此）：\n")
	sb.WriteString("- 强势趋势中的回调结束 → 顺势继续跟随（做多或做空）。\n")
	sb.WriteString("- 区间震荡中，价格触及上/下沿且出现反转信号 → 做短线反转。\n")
	sb.WriteString("- 突然放量+波动率放大，价格突破关键区间 → 短线追随突破方向。\n")
	sb.WriteString("- 单边极端拉升/下跌后的明显衰竭信号 → 短线反向博回撤（仅在证据充分时）。\n\n")
	sb.WriteString("每次建议开仓前，请综合多维信号给出主观 **信心度 (0–100)**，\n")
	sb.WriteString("只有在你认为\"置信度足够高\"（例如 ≥70）时才建议开仓。\n\n")

	// 4️⃣ 风险与仓位（复用现有约束）
	sb.WriteString("# 4️⃣ 风险与仓位约束\n\n")
	sb.WriteString("当前账户净值、浮动盈亏、保证金使用率会在用户消息中给出，你需要基于这些数据评估整体风险承受能力。\n")
	sb.WriteString(fmt.Sprintf("最大杠杆限制: BTC/ETH %dx, 山寨币 %dx\n", btcEthLeverage, altcoinLeverage))
	sb.WriteString("- 不孤注一掷：单一标的的风险不应占用账户的绝大部分。\n")
	sb.WriteString(fmt.Sprintf("- 每笔交易的风险回报比应 ≥ 1:%.0f（理想 ≥ 1:3）。\n", minRiskReward))
	sb.WriteString("- 止损价格和止盈价格必须与方向一致，不能出现做多止损高于止盈等明显错误。\n\n")

	// 5️⃣ 基于绩效（Sharpe）的自我调节（短期版）
	sb.WriteString("# 5️⃣ 基于历史交易记录的自我调节与演化\n\n")
	sb.WriteString("每次你会收到**最近一段时间的交易笔数**、**胜率**、**夏普比率**作为绩效反馈（周期级别）：\n")
	sb.WriteString("- Sharpe 很差（< -0.5）或最近多笔连续亏损：\n")
	sb.WriteString("  • 置信度 **≥ 80–85** 才能开仓；\n")
	sb.WriteString("  • 防守模式，只做绝对高质量机会。\n")
	sb.WriteString("- Sharpe 略负/接近 0：\n")
	sb.WriteString("  • 置信度 **≥ 75** 才能开仓；\n")
	sb.WriteString("  • 调整节奏、减少低质量进场。\n")
	sb.WriteString("- Sharpe 为正且稳定：\n")
	sb.WriteString("  • 置信度 **≥ 70** 才能开仓；\n")
	sb.WriteString("  • 市场健康，可适度积极。\n\n")

	// 6️⃣ 决策流程 & 输出格式（复用 B 的 JSON 规范）
	sb.WriteString("# 6️⃣ 决策流程\n\n")
	sb.WriteString("1. 判断当前是趋势阶段、震荡阶段，还是高波动/事件驱动阶段。\n")
	sb.WriteString("2. 结合短周期价格/指标/波动/资金信息，评估是否存在高性价比短线机会。\n")
	sb.WriteString("3. 如果有持仓，优先考虑止盈/止损/部分减仓等风险管理动作。\n")
	sb.WriteString("4. 如果信号不足或方向不清晰，请选择 `hold` 或 `wait`。\n")
	sb.WriteString("5. 只在信号充分、逻辑清晰、风险回报合理时，才给出开仓建议。\n\n")

	sb.WriteString("# 7️⃣ 输出格式（必须严格遵守）\n\n")
	sb.WriteString("**第一步: 思维链（纯文本）**\n")
	sb.WriteString("简要说明你如何解读当前行情，为什么选择观望/加仓/减仓/反向？简要写出关键因素即可。\n\n")

	sb.WriteString("**第二步: JSON决策数组（示例如下）**\n\n")
	sb.WriteString("```json\n[\n")
	sb.WriteString(fmt.Sprintf(
		"  {\"symbol\": \"BTCUSDT\", \"action\": \"open_long\", \"leverage\": %d, \"position_size_usd\": 3000, \"stop_loss\": 95000, \"take_profit\": 105000, \"confidence\": 85, \"risk_usd\": 300, \"reasoning\": \"顺势突破+放量\"},\n",
		btcEthLeverage,
	))
	sb.WriteString("  {\"symbol\": \"ETHUSDT\", \"action\": \"update_stop_loss\", \"new_stop_loss\": 2800, \"reasoning\": \"移动止损保护利润\"},\n")
	sb.WriteString("  {\"symbol\": \"SOLUSDT\", \"action\": \"partial_close\", \"close_percentage\": 50, \"reasoning\": \"触及R1阻力，止盈一半\"}\n")
	sb.WriteString("]\n```\n\n")

	sb.WriteString("**字段说明**:\n")
	sb.WriteString("- `action`: open_long | open_short | close_long | close_short | update_stop_loss | update_take_profit | partial_close | hold | wait\n")
	sb.WriteString("- 开仓时必填: leverage, position_size_usd, stop_loss, take_profit, confidence, risk_usd, reasoning\n")
	sb.WriteString("- `update_stop_loss`: 必须提供 `new_stop_loss`\n")
	sb.WriteString("- `partial_close`: 必须提供 `close_percentage` (0-100)\n")
	sb.WriteString("- 如果暂时没有足够好的机会，请坦然输出 wait 或仅对已有持仓做风控调整。\n")
	sb.WriteString("- `symbol`: 当决策为open_long | open_short | close_long | close_short | update_stop_loss | update_take_profit | partial_close等涉及币种仓位的操作时，`symbol`字段必填\n")
	sb.WriteString("	`symbol`如果有值，则必须符合以下规则：必须严格从\"当前持仓列表\"和\"候选币种列表\"中选择，必须与你的决策思维对应，不允许虚构币种\n\n")

	return sb.String()
}

// buildUserPrompt 构建 User Prompt（动态数据）
func buildUserPrompt(ctx *Context) string {
	var sb strings.Builder

	// 系统状态
	sb.WriteString(fmt.Sprintf("**时间**: %s | **周期**: #%d | **运行**: %d分钟\n\n",
		ctx.CurrentTime, ctx.CallCount, ctx.RuntimeMinutes))

	// BTC 市场
	if btcData, hasBTC := ctx.MarketDataMap["BTCUSDT"]; hasBTC {
		sb.WriteString(fmt.Sprintf("**BTC**: %.2f (1h: %+.2f%%, 4h: %+.2f%%) | MACD: %.4f | RSI: %.2f\n\n",
			btcData.CurrentPrice, btcData.PriceChange1h, btcData.PriceChange4h,
			btcData.CurrentMACD, btcData.CurrentRSI7))
	}

	// 账户
	sb.WriteString(fmt.Sprintf("**账户**: 净值%.2f | 余额%.2f (%.1f%%) | 盈亏%+.2f%% | 保证金%.1f%% | 持仓%d个\n\n",
		ctx.Account.TotalEquity,
		ctx.Account.AvailableBalance,
		(ctx.Account.AvailableBalance/ctx.Account.TotalEquity)*100,
		ctx.Account.TotalPnLPct,
		ctx.Account.MarginUsedPct,
		ctx.Account.PositionCount))

	// 持仓（完整市场数据）
	if len(ctx.Positions) > 0 {
		sb.WriteString("## 当前持仓\n")
		for i, pos := range ctx.Positions {
			// 计算持仓时长
			holdingDuration := ""
			if pos.UpdateTime > 0 {
				durationMs := time.Now().UnixMilli() - pos.UpdateTime
				durationMin := durationMs / (1000 * 60) // 转换为分钟
				if durationMin < 60 {
					holdingDuration = fmt.Sprintf(" | 持仓时长%d分钟", durationMin)
				} else {
					durationHour := durationMin / 60
					durationMinRemainder := durationMin % 60
					holdingDuration = fmt.Sprintf(" | 持仓时长%d小时%d分钟", durationHour, durationMinRemainder)
				}
			}

			sb.WriteString(fmt.Sprintf("%d. %s %s | 入场价%.4f 当前价%.4f | 盈亏%+.2f%% | 杠杆%dx | 保证金%.0f | 强平价%.4f%s\n\n",
				i+1, pos.Symbol, strings.ToUpper(pos.Side),
				pos.EntryPrice, pos.MarkPrice, pos.UnrealizedPnLPct,
				pos.Leverage, pos.MarginUsed, pos.LiquidationPrice, holdingDuration))

			// 使用FormatMarketData输出完整市场数据
			if marketData, ok := ctx.MarketDataMap[pos.Symbol]; ok {
				sb.WriteString(market.Format(marketData))
				sb.WriteString("\n")
			}
		}
	} else {
		sb.WriteString("**当前持仓**: 无\n\n")
	}

	// 候选币种（完整市场数据）
	sb.WriteString(fmt.Sprintf("## 候选币种 (最多%d个)\n\n", len(ctx.MarketDataMap)))
	displayedCount := 0
	for _, coin := range ctx.CandidateCoins {
		marketData, hasData := ctx.MarketDataMap[coin.Symbol]
		if !hasData {
			continue
		}
		displayedCount++

		sourceTags := ""
		if len(coin.Sources) > 1 {
			sourceTags = " (AI500+OI_Top双重信号)"
		} else if len(coin.Sources) == 1 && coin.Sources[0] == "oi_top" {
			sourceTags = " (OI_Top持仓增长)"
		}

		// 使用FormatMarketData输出完整市场数据
		sb.WriteString(fmt.Sprintf("### %d. %s%s\n\n", displayedCount, coin.Symbol, sourceTags))
		sb.WriteString(market.Format(marketData))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// 历史交易表现：夏普比率，交易笔数，胜率
	sb.WriteString(getPerformance(ctx))

	sb.WriteString("---\n\n")
	sb.WriteString("现在请分析并输出决策（思维链 + JSON）\n")

	return sb.String()
}

// extraUserPromptForStrategyB 策略B的额外用户提示词信息
func extraUserPromptForStrategyB(marketData *market.Data) string {
	var sb strings.Builder
	// 日内波动刻画：3m ATR14（价格单位），用于止损距离/噪声过滤
	if marketData.IntradayATR14 > 0 {
		sb.WriteString(fmt.Sprintf(
			"intraday_atr14 (3m) = %.4f\n\n",
			marketData.IntradayATR14,
		))
	}

	// 用于硬规则（禁止明显逆势）和宏观判断的涨跌幅信息
	// 注意：此字段在 market.Get 中计算（基于3m/4h K线），可用于快速识别“顺势/逆势”
	sb.WriteString(fmt.Sprintf("price_change: 1h: %+.2f%%, 4h: %+.2f%%\n\n", marketData.PriceChange1h, marketData.PriceChange4h))

	if marketData.VolatilityPct > 0 {
		sb.WriteString(fmt.Sprintf(
			"normalized_volatility (ATR14/price) = %.4f\n\n",
			marketData.VolatilityPct,
		))
	}

	if lt := marketData.LongerTermContext; lt != nil {

		if lt.MarketStructure != "" {
			sb.WriteString(fmt.Sprintf(
				"4h_market_structure = %s\n\n",
				lt.MarketStructure,
			))
		}

		if lt.CandleSignal != "" {
			sb.WriteString(fmt.Sprintf(
				"4h_latest_candle_signal = %s\n\n",
				lt.CandleSignal,
			))
		}

		if lt.Support > 0 && lt.Resistance > 0 {
			sb.WriteString(fmt.Sprintf(
				"4h_support_resistance = [%.3f, %.3f]\n\n",
				lt.Support, lt.Resistance,
			))

			// ===== 位置感/空间感（用于避免在区间上沿追多、下沿追空）=====
			if marketData.CurrentPrice > 0 && lt.Resistance > lt.Support {
				price := marketData.CurrentPrice
				posInRange := (price - lt.Support) / (lt.Resistance - lt.Support)
				if posInRange < 0 {
					posInRange = 0
				}
				if posInRange > 1 {
					posInRange = 1
				}

				distToResPct := (lt.Resistance - price) / price * 100
				distToSupPct := (price - lt.Support) / price * 100

				sb.WriteString(fmt.Sprintf("position_in_range_4h = %.3f\n\n", posInRange))
				sb.WriteString(fmt.Sprintf("dist_to_resistance_pct = %.3f%%\n\n", distToResPct))
				sb.WriteString(fmt.Sprintf("dist_to_support_pct = %.3f%%\n\n", distToSupPct))

				if lt.ATR14 > 0 {
					distToResATR := (lt.Resistance - price) / lt.ATR14
					distToSupATR := (price - lt.Support) / lt.ATR14
					sb.WriteString(fmt.Sprintf("dist_to_resistance_atr (ATR14=%.3f) = %.3f\n\n", lt.ATR14, distToResATR))
					sb.WriteString(fmt.Sprintf("dist_to_support_atr (ATR14=%.3f) = %.3f\n\n", lt.ATR14, distToSupATR))
				}
			}
		}
	}

	return sb.String()
}

// buildUserPromptB B策略的User Prompt重载示例：在顶部标注版本，并可按需精简候选展示
func buildUserPromptB(ctx *Context) string {
	var sb strings.Builder

	// 系统状态
	sb.WriteString(fmt.Sprintf("**时间**: %s | **周期**: #%d | **运行**: %d分钟\n\n",
		ctx.CurrentTime, ctx.CallCount, ctx.RuntimeMinutes))

	// BTC 市场
	if btcData, hasBTC := ctx.MarketDataMap["BTCUSDT"]; hasBTC {
		sb.WriteString(fmt.Sprintf("**BTC**: %.2f (1h: %+.2f%%, 4h: %+.2f%%) | MACD: %.4f | RSI: %.2f\n\n",
			btcData.CurrentPrice, btcData.PriceChange1h, btcData.PriceChange4h,
			btcData.CurrentMACD, btcData.CurrentRSI7))
	}

	// 账户
	sb.WriteString(fmt.Sprintf("**账户**: 净值%.2f | 余额%.2f (%.1f%%) | 盈亏%+.2f%% | 保证金%.1f%% | 持仓%d个\n\n",
		ctx.Account.TotalEquity,
		ctx.Account.AvailableBalance,
		(ctx.Account.AvailableBalance/ctx.Account.TotalEquity)*100,
		ctx.Account.TotalPnLPct,
		ctx.Account.MarginUsedPct,
		ctx.Account.PositionCount))

	// 持仓（完整市场数据）
	if len(ctx.Positions) > 0 {
		sb.WriteString("## 当前持仓\n")
		for i, pos := range ctx.Positions {
			// 计算持仓时长
			holdingDuration := ""
			if pos.UpdateTime > 0 {
				durationMs := time.Now().UnixMilli() - pos.UpdateTime
				durationMin := durationMs / (1000 * 60) // 转换为分钟
				if durationMin < 60 {
					holdingDuration = fmt.Sprintf(" | 持仓时长%d分钟", durationMin)
				} else {
					durationHour := durationMin / 60
					durationMinRemainder := durationMin % 60
					holdingDuration = fmt.Sprintf(" | 持仓时长%d小时%d分钟", durationHour, durationMinRemainder)
				}
			}

			sb.WriteString(fmt.Sprintf("%d. %s %s | 入场价%.4f 当前价%.4f | 盈亏%+.2f%% | 杠杆%dx | 保证金%.0f | 强平价%.4f%s\n\n",
				i+1, pos.Symbol, strings.ToUpper(pos.Side),
				pos.EntryPrice, pos.MarkPrice, pos.UnrealizedPnLPct,
				pos.Leverage, pos.MarginUsed, pos.LiquidationPrice, holdingDuration))

			// 使用FormatMarketData输出完整市场数据
			if marketData, ok := ctx.MarketDataMap[pos.Symbol]; ok {
				sb.WriteString(market.Format(marketData))
				// 相对于strategyA，strategyB增加了一些新指标
				sb.WriteString(extraUserPromptForStrategyB(marketData))
				sb.WriteString("\n")
			}
		}
	} else {
		sb.WriteString("**当前持仓**: 无\n\n")
	}

	// 候选币种（完整市场数据）
	sb.WriteString(fmt.Sprintf("## 候选币种 (最多%d个)\n\n", len(ctx.MarketDataMap)))
	displayedCount := 0
	for _, coin := range ctx.CandidateCoins {
		marketData, hasData := ctx.MarketDataMap[coin.Symbol]
		if !hasData {
			continue
		}
		displayedCount++

		sourceTags := ""
		if len(coin.Sources) > 1 {
			sourceTags = " (AI500+OI_Top双重信号)"
		} else if len(coin.Sources) == 1 && coin.Sources[0] == "oi_top" {
			sourceTags = " (OI_Top持仓增长)"
		}

		// 使用FormatMarketData输出完整市场数据
		sb.WriteString(fmt.Sprintf("### %d. %s%s\n\n", displayedCount, coin.Symbol, sourceTags))
		sb.WriteString(market.Format(marketData))
		sb.WriteString(extraUserPromptForStrategyB(marketData))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// 历史交易表现：夏普比率，交易笔数，胜率
	sb.WriteString(getPerformance(ctx))

	sb.WriteString("---\n\n")
	sb.WriteString("现在请分析并输出决策（思维链 + JSON）\n")

	return sb.String()
}

// getPerformance 获取历史表现信息
func getPerformance(ctx *Context) string {
	var sb strings.Builder
	if ctx.Performance == nil {
		return "夏普比率: 0.00"
	}

	var perf *logger.PerformanceAnalysis

	switch v := ctx.Performance.(type) {
	case *logger.PerformanceAnalysis:
		perf = v
	case logger.PerformanceAnalysis:
		perf = &v
	default:
		// 类型不符就直接返回空，防止 panic
		return "夏普比率: 0.00"
	}

	sb.WriteString(fmt.Sprintf(
		"**总体**: %d笔交易 | 胜率%.1f%% | 夏普比率%.2f | 当前连亏: %d 笔\n",
		perf.TotalTrades,
		perf.WinRate,
		perf.SharpeRatio,
		perf.CurrentLosingStreak,
	))
	return sb.String()
}
