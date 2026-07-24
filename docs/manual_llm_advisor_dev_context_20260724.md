# Manual LLM Advisor 开发上下文

生成时间：2026-07-24 11:42:30 CST
仓库：/home/admin/nofx-src
分支：feat/manual-llm-advisor
当前提交：87ceafe feat: scaffold manual LLM advisor

## 1. 功能目标

在 nofx Web 界面中新增一个人工触发的 LLM 交易顾问功能。

用户可以在界面中指定：

- symbol，例如 ETHUSDT / SOLUSDT
- 问题，例如“我想做空 ETH，现在是否合适？”
- intent：让 LLM 自主判断方向、评估做多、评估做空、校验已有计划
- 可选人工计划：entry / stop loss / take profit / leverage / size

系统需要拉取 nofx 内部已有的实时上下文：

- trader/account 状态
- 当前持仓
- 单 symbol 的 StrategyV short-term heavy market context
- 后续可加入 BTC/ETH 大盘锚、OI/funding、AI300/heatmap 等

LLM 输出结构化建议，但第一版不允许 LLM 直接下单。用户必须手动确认开仓。

## 2. 已确定产品边界

这是 Manual LLM Advisor，不是新的自动交易策略。

第一版明确不做：

- 不新增 ManualAdvisorStrategy
- 不自动扫描市场
- 不改变自动交易 loop
- 不改变 watchlist / candidate pool
- 不直接下单
- 不让 LLM 绕过程序侧风控校验

第一版要做：

- 在 UI 中提供 Ask LLM 面板
- 后端提供 advisor analyze API
- API 返回上下文和 prompt preview 框架
- 后续接入 LLM 严格 JSON 输出
- 后续支持 Fill Manual Order
- 人工确认开仓后，将仓位绑定给已有 trader 管理

## 3. 仓位管理归属决策

人工 advisor 开仓后的仓位不交给一个新 strategy，而是交给已有 trader instance。

字段使用：

```json
{
  "source": "manual_llm_advisor",
  "advisor_session_id": "advisor_...",
  "management_trader_id": "binance_ds_strategyV_paper_local",
  "entry_thesis": {
    "setup_type": "range_reversal",
    "reasoning_summary": "...",
    "invalidation_condition": "...",
    "suggested_stop_loss": 1882,
    "suggested_take_profit": 1835,
    "time_stop_minutes": 60
  }
}
```

为什么用 management_trader_id，而不是 strategy name：

- 同一个 strategy name 可能对应不同账号、不同模型、不同 paper/live 环境
- 后续仓位管理需要准确知道由哪个 trader/account/risk config 负责
- trader instance 维度可以避免管理错账号或错环境

当前 v1 选择规则：

- 0 个 running candidate：无默认；提示没有 LLM 自动管理
- 1 个 running candidate：自动默认这个 trader
- 多个 running candidate：requires_choice=true，用户必须选择 management_trader_id

后续可以进一步过滤：

- same exchange
- same account / trader account binding
- same paper/live mode
- supports position management
- active 或 explicitly management-enabled

## 4. LLM 输出要求

后续真实 LLM 调用需要返回严格 JSON，字段包括：

```json
{
  "symbol": "ETHUSDT",
  "recommendation": "open_long|open_short|no_trade|wait",
  "stance_on_user_idea": "agree|agree_with_adjustments|disagree|not_applicable",
  "confidence": 0,
  "setup_type": "trend_pullback|breakout_momentum|range_reversal|exhaustion_reversal|failed_breakout|no_trade",
  "entry": {
    "suggested_entry_price": 0,
    "acceptable_entry_range": [0, 0],
    "wait_for_next_candle": false
  },
  "risk": {
    "stop_loss": 0,
    "invalidation_condition": ""
  },
  "reward": {
    "take_profit_1": 0,
    "take_profit_2": 0
  },
  "sizing": {
    "leverage": 0,
    "position_size_usd": 0
  },
  "rr": {
    "net_rr": 0,
    "passes_min_rr": false
  },
  "expected_holding_minutes": 0,
  "time_stop_minutes": 0,
  "reasoning_summary": "",
  "main_risks": []
}
```

关键 prompt 原则：

- 用户方向只是 hypothesis，不是 instruction
- LLM 必须可以反对用户
- 不合适时返回 no_trade / wait
- LLM 不能直接下单
- LLM 计算出的 RR 只能作为参考，程序必须重新计算

## 5. 程序侧校验要求

后续进入真实可用版本前，必须在后端做校验：

1. symbol 是否支持交易
2. side 是否合法
3. SL/TP 方向是否正确
   - long: SL < entry < TP
   - short: TP < entry < SL
4. net RR 由程序重新计算，不能信 LLM
5. min RR 是否达标
6. leverage 是否超限
7. position size 是否超限
8. min stop distance 是否满足
9. margin pre-check 是否通过
10. 用户必须手动确认下单
11. 下单后持久化 advisor_session_id / source / management_trader_id / entry_thesis

## 6. 当前分支已完成内容

### 6.1 后端

新增：

- api/advisor.go
- decision/manual_advisor.go
- manager/manual_advisor.go
- docs/manual_llm_advisor_impact.md

修改：

- api/server.go

后端新增路由：

```text
GET  /api/advisor/management-candidates
POST /api/advisor/analyze
```

当前 POST /api/advisor/analyze 做的事：

- 解析请求
- 校验 symbol / question
- 解析 advisor_trader_id
- 解析 management_trader_id
- 如果 exactly one running candidate，自动填默认 manager
- 如果 multiple candidates 且未传 management_trader_id，返回 400 和 candidate 列表
- 拉取 advisor trader 的 account / positions
- 拉取 target symbol 的 closed short-term heavy market data
- 调用 decision.BuildManualAdvisorPrompts()
- 返回 scaffold_only response，包括 prompt_preview、recommendation placeholder、next_todo

当前不会：

- 调用 LLM
- 解析 LLM JSON
- 创建订单
- 修改自动交易状态
- 写入真实 advisor session log

### 6.2 manager 层

manager/manual_advisor.go 新增：

- ManagementCandidate
- ManagementCandidatesResponse
- TraderManager.GetManualAdvisorManagementCandidates()

用于统一管理候选逻辑，避免前端/handler 分散重复规则。

### 6.3 decision 层

decision/manual_advisor.go 新增：

- ManualAdvisorIntent
- ManualAdvisorUserPlan
- ManualAdvisorPromptInput
- ManualAdvisorPromptBundle
- BuildManualAdvisorPrompts()

这个文件是 advisor prompt 的集中入口，后续真实 LLM 接入应复用它，而不是把 prompt 拼接散落在 api handler 中。

### 6.4 前端

新增：

- web/src/components/ManualAdvisorPanel.tsx

修改：

- web/src/App.tsx
- web/src/lib/api.ts
- web/src/types.ts

前端当前能力：

- 在 TraderDetailsPage 中展示 Manual LLM Advisor 面板
- 输入 symbol/question/intent
- 加载 management candidates
- exactly one candidate 时自动选中
- multiple candidates 时要求用户选择
- 调用 /api/advisor/analyze
- 展示 scaffold response / prompt preview / next_todo

当前 UI 明确标注 scaffold，不直接下单。

## 7. 已运行验证

前端：

```bash
cd /home/admin/nofx-src/web
npm run build
```

结果：通过。

Go：

本机原本没有 go/gofmt，因此临时下载 Go toolchain 到：

```text
/tmp/go1.22.12
```

直接普通 go test ./... 编译 gin/msgpack 依赖时内存被 kill，因此使用项目可用的 nomsgpack tag 做完整检查：

```bash
cd /home/admin/nofx-src
GOMAXPROCS=1 /tmp/go1.22.12/go/bin/go test -tags nomsgpack -p 1 ./...
GOMAXPROCS=1 /tmp/go1.22.12/go/bin/go build -tags nomsgpack -o /tmp/nofx.buildcheck .
```

结果：通过。

格式检查：

```bash
git diff --check
```

结果：通过。

Go 格式化：

```bash
/tmp/go1.22.12/go/bin/gofmt -w api/advisor.go api/server.go decision/manual_advisor.go manager/manual_advisor.go
```

已执行。

## 8. 运行环境注意事项

当前正在运行的 nofx 实例在：

```text
/home/admin/nofx
```

本次开发仓库在：

```text
/home/admin/nofx-src
```

本次没有修改 /home/admin/nofx 的运行二进制，也没有重启服务。

当前运行进程之前核对过：

- ./tools/coin_pool_server/coin_pool_server
- /home/admin/nofx/v2ray/v2ray ...
- ./nofx

用户在 nofx 维护中通常希望：只改源代码、运行编译/测试检查、git commit 并 push；除非明确要求，不编译部署二进制、不重启服务、不影响正在运行的 nofx 程序。

本次遵守了这个约束。

## 9. 后续开发建议顺序

### Phase 1：接入真实 LLM 但仍不下单

1. 找到 trader 中现有 AI client 调用方式
2. 在 advisor analyze 中调用 selected trader 的 AI client
3. 让 LLM 返回 strict JSON
4. 增加 robust JSON extraction/parser
5. 返回 parsed recommendation
6. 前端渲染 structured advice，而不是只看 prompt preview

### Phase 2：程序侧校验

1. 抽象 advisor validation result
2. 复用/抽取现有 RR calculator / min RR config
3. 校验 SL/TP/entry 方向
4. 校验 position size/leverage/margin
5. 在 response 中标明 executable / blocked reasons

### Phase 3：Fill Manual Order

1. 找到现有 manual order form 的状态/组件
2. 增加 Use Plan / Fill Manual Order 按钮
3. 仅填表，不自动下单
4. 下单前显示 human confirmation

### Phase 4：持久化 advisor session 和 entry thesis

1. 新增 advisor session log storage
2. manual order payload 增加 source=manual_llm_advisor
3. manual order payload 增加 advisor_session_id
4. manual order payload 增加 management_trader_id
5. manual order payload 增加 entry_thesis

### Phase 5：已有 trader 管理人工 advisor 仓位

1. 检查 position management cycle 如何选择仓位
2. 确保 manual_llm_advisor position 不会被忽略
3. position management prompt 注入 entry_thesis
4. hold/close/update SL/TP 决策要知道原始开仓理由和 invalidation condition

## 10. 关键文件索引

```text
/home/admin/nofx-src/api/advisor.go
/home/admin/nofx-src/api/server.go
/home/admin/nofx-src/decision/manual_advisor.go
/home/admin/nofx-src/manager/manual_advisor.go
/home/admin/nofx-src/web/src/components/ManualAdvisorPanel.tsx
/home/admin/nofx-src/web/src/lib/api.ts
/home/admin/nofx-src/web/src/types.ts
/home/admin/nofx-src/web/src/App.tsx
/home/admin/nofx-src/docs/manual_llm_advisor_impact.md
```

## 11. 当前提交

```text
87ceafe feat: scaffold manual LLM advisor
```

提交内容统计：

```text
9 files changed, 812 insertions(+)
```

## 12. 本次聊天中的核心决策摘要

- 新开分支专门做该功能：feat/manual-llm-advisor
- 第一版搭框架，不实现全功能
- 功能影响范围包括 api / manager / decision / market / trader / frontend / manual order / position management
- 不新增专门 strategy
- 不自动开仓
- 用户开仓后交给已有 trader instance 管理
- 需要 management_trader_id
- 需要 entry_thesis
- 需要 source=manual_llm_advisor
- 多 trader 时不能自动猜
- 程序侧校验必须优先于真实下单

## 13. 下一位开发者进入状态的最短路径

```bash
cd /home/admin/nofx-src
git checkout feat/manual-llm-advisor
git status
sed -n '1,220p' docs/manual_llm_advisor_impact.md
sed -n '1,260p' api/advisor.go
sed -n '1,220p' decision/manual_advisor.go
sed -n '1,220p' web/src/components/ManualAdvisorPanel.tsx
```

然后优先实现：真实 LLM 调用 + strict JSON parse + validation，但仍然不要让 advisor 直接下单。
