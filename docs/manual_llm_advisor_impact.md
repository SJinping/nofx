# Manual LLM Advisor impact assessment

This branch (`feat/manual-llm-advisor`) scaffolds the first version of an in-UI LLM trade advisor for manual entries.

## Product boundary

The advisor is a pre-trade decision-support feature, not a new autonomous strategy.

- User explicitly chooses a symbol and asks for advice.
- The backend gathers trader/account/position context plus single-symbol heavy market data.
- The LLM will eventually return a structured plan: side, entry range, stop loss, take profit, sizing, net RR, confidence, invalidation, and risk notes.
- The first production implementation should fill the existing manual order form only. It must not place orders directly without human confirmation.
- After a user manually opens a position, the position should be assigned to an existing trader via `management_trader_id`.

## Strategy assignment rule

Do not introduce a dedicated ManualAdvisorStrategy for v1.

- If exactly one running trader can manage positions, default to that trader.
- If multiple running traders can manage positions, require the user to choose one.
- If no running trader can manage positions, advisor analysis can still run but the UI must warn that no LLM position management will happen after manual entry.

The management binding should be stored as trader-instance identity, not just strategy name:

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

## Backend impact

### `api`

New advisor endpoints should live under `/api/advisor`:

- `GET /api/advisor/management-candidates`
  - Returns running trader instances and a default candidate if exactly one is available.
  - Used by the UI to implement automatic single-trader assignment and forced multi-trader selection.

- `POST /api/advisor/analyze`
  - v1 scaffold validates request, resolves `advisor_trader_id` and `management_trader_id`, fetches target symbol market data, and returns a typed placeholder response with prompt previews.
  - Later implementation should call the selected trader's AI client and parse strict JSON.

### `manager`

Add a small read-only candidate resolver around existing `TraderManager` state. This avoids duplicating trader selection logic in handlers and front-end code.

### `trader`

No new strategy is required.

Likely future additions:

- expose prompt strategy name for display/debug;
- attach advisor entry thesis to positions/trade memory after manual entry;
- route manual-advisor positions through existing position management cycle by `management_trader_id`.

### `decision`

Advisor prompt construction should be isolated from automatic decision prompts.

Future implementation should:

- reuse StrategyV short-term heavy market data for short-term advisor questions;
- reuse existing validation/RR logic after the LLM returns candidate SL/TP;
- set `decision_source = manual_llm_advisor` for auditability.

### `market`

No broad market scan is needed. Advisor should fetch a single heavy symbol plus optional BTC/ETH anchors. This keeps token/API cost bounded and avoids altering StrategyA/B data paths.

## Frontend impact

Add a panel/component to the trader details page:

- selected trader is the default advisor trader;
- input symbol/question/intent;
- show management assignment state;
- call `/api/advisor/analyze`;
- render structured result and prompt preview;
- future button: `Fill Manual Order`.

The scaffold deliberately does not implement direct order placement.

## Safety gates for future implementation

Before manual order execution from advisor output:

1. Validate symbol format and exchange support.
2. Validate side/SL/TP directionality.
3. Recalculate net RR with program logic; do not trust LLM arithmetic.
4. Enforce min RR, leverage clip, margin pre-check, and stop-loss distance.
5. Require user confirmation before order placement.
6. Persist `advisor_session_id`, `source`, `management_trader_id`, and `entry_thesis` for later position management and replay.
