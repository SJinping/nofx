<!-- refreshed: 2026-07-10 -->
# Architecture

**Analysis Date:** 2026-07-10

## System Overview

```text
┌─────────────────────────────────────────────────────────────────────────┐
│                     Web Dashboard (React + Vite)                         │
│  `web/src/App.tsx`  →  SWR polling  →  `web/src/lib/api.ts`           │
└───────────────────────────────┬─────────────────────────────────────────┘
                                │ HTTP /api/* (nginx proxy in Docker)
                                ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                    HTTP API Layer (Gin)                                  │
│  `api/server.go` — status, account, competition, logs, config hot-update │
└───────────────────────────────┬─────────────────────────────────────────┘
                                │
                                ▼
┌─────────────────────────────────────────────────────────────────────────┐
│               TraderManager (multi-trader orchestration)               │
│  `manager/trader_manager.go` — one AutoTrader per config entry         │
└───────┬─────────────────────────────────────────────────────────────────┘
        │ goroutine per trader
        ▼
┌─────────────────────────────────────────────────────────────────────────┐
│  AutoTrader loop (`trader/auto_trader.go`)                             │
│  build context → risk gates → decision.GetFullDecision → execute       │
└───┬─────────────┬──────────────┬──────────────┬─────────────────────────┘
    │             │              │              │
    ▼             ▼              ▼              ▼
┌────────┐  ┌──────────┐  ┌──────────┐  ┌─────────────────────────────┐
│ Trader │  │ decision │  │  market  │  │ memory / logger / stats     │
│interface│  │ engine   │  │  data    │  │ persistence & self-learning │
└───┬────┘  └────┬─────┘  └──────────┘  └─────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ Exchange adapters: Binance / Hyperliquid / Aster (+ PaperTrader wrap)  │
│ `trader/binance_futures.go`, `hyperliquid_trader.go`, `aster_trader.go`  │
└─────────────────────────────────────────────────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ External: Binance FAPI, Hyperliquid, Aster DEX, DeepSeek/Qwen APIs       │
│ Optional: V2Ray SOCKS5 proxy (`market/data.go`, `v2ray/`)               │
└─────────────────────────────────────────────────────────────────────────┘
```

## Component Responsibilities

| Component | Responsibility | File |
|-----------|----------------|------|
| `main` | Bootstrap: load config, init pools, create TraderManager, start API + traders | `main.go` |
| `TraderManager` | Registry of `AutoTrader` instances; start/stop/pause; runtime config persistence | `manager/trader_manager.go` |
| `AutoTrader` | Periodic scan loop; build decision context; invoke AI; execute trades; log cycles | `trader/auto_trader.go` |
| `decision` | Market fetch for context, prompt strategies, LLM call, parse/validate decisions | `decision/engine.go`, `decision/validation.go` |
| `Trader` (interface) | Unified exchange operations (open/close, SL/TP, orders) | `trader/interface.go` |
| `mcp.Client` | Per-trader LLM client (DeepSeek, Qwen, custom OpenAI-compatible) | `mcp/client.go` |
| `market` | Binance FAPI market data (klines, funding, indicators); proxy-aware HTTP | `market/data.go`, `market/nofxdata.go` |
| `pool` | Candidate coin selection (AI500 API, OI Top, default mainstream list) | `pool/coin_pool.go` |
| `memory.TradeMemory` | Trade history, vector similarity, Gate/OpenGuard self-learning | `memory/trade_memory.go`, `memory/agents.go` |
| `logger.DecisionLogger` | JSON decision cycle logs per trader | `logger/decision_logger.go` |
| `api.Server` | REST endpoints for dashboard and operational control | `api/server.go` |
| Web SPA | Competition view, per-trader dashboard, log viewer, config controls | `web/src/App.tsx` |

## Pattern Overview

**Overall:** Layered monolith with **strategy + adapter** patterns and **goroutine-per-trader** concurrency.

**Key Characteristics:**
- Single Go binary (`main.go`) hosts all traders and the HTTP API in one process.
- Each configured trader runs an independent ticker loop (`AutoTrader.Run`) in its own goroutine.
- Exchange differences are hidden behind the `Trader` interface; AI differences behind `mcp.Client` and `PromptStrategy`.
- Persistence is **file-based** (JSON logs, JSONL trade memory, `config.json` hot updates) — no database.
- Frontend is a decoupled React SPA that polls REST endpoints via SWR.

## Layers

**Presentation (Web):**
- Purpose: Real-time monitoring, competition ranking, log inspection, runtime config UI.
- Location: `web/src/`
- Contains: React pages/components, `api.ts` fetch wrapper, i18n, Tailwind styles.
- Depends on: Backend REST API at `/api/*`.
- Used by: Browser users (port 3000 via nginx in Docker).

**API / Control Plane:**
- Purpose: Expose trader state, historical logs, market overview, pause/resume, config hot-update.
- Location: `api/`
- Contains: Gin routes, handlers delegating to `TraderManager` and filesystem log reads.
- Depends on: `manager`, `market`, `trader` runtime types.
- Used by: Web frontend, curl/ops scripts.

**Orchestration:**
- Purpose: Multi-trader lifecycle, competition aggregation, config persistence to `config.json`.
- Location: `manager/`
- Contains: `TraderManager` with `sync.RWMutex` over trader map.
- Depends on: `config`, `trader`, `decision` (strategy wiring).
- Used by: `main.go`, `api/server.go`.

**Trading Domain (Core Loop):**
- Purpose: End-to-end decision cycle — context assembly, AI invocation, order execution, logging.
- Location: `trader/auto_trader.go`
- Contains: `AutoTrader`, `runCycle`, execution helpers, risk pause, peak-hour skip.
- Depends on: `decision`, `logger`, `memory`, `pool`, `mcp`, exchange `Trader` impl.
- Used by: `TraderManager.StartAll`.

**Decision / AI Layer:**
- Purpose: Transform market + account snapshot into validated trade decisions.
- Location: `decision/`
- Contains: `GetFullDecision`, `PromptStrategy` implementations (A/B/V), validation, auto rules (SL/TP), parser, backtest helpers.
- Depends on: `market`, `pool`, `mcp` (via `AICaller` interface), `stats`.
- Used by: `AutoTrader.runCycle`, `cmd/*` backtest tools.

**Market & Pool Data:**
- Purpose: Fetch and normalize market indicators; resolve tradable symbol universe.
- Location: `market/`, `pool/`
- Contains: FAPI client, TA indicators, coin pool cache, OI Top integration.
- Depends on: External Binance APIs (optionally via V2Ray proxy).
- Used by: `decision` context building, `api` klines/market-overview.

**Exchange Adapters:**
- Purpose: Platform-specific order placement and account queries behind one interface.
- Location: `trader/binance_futures.go`, `trader/hyperliquid_trader.go`, `trader/aster_trader.go`, `trader/paper_trader.go`
- Contains: `Trader` interface implementations; paper mode wraps real adapter with simulated fills.
- Depends on: Third-party SDKs (`go-binance`, `go-hyperliquid`, Ethereum signing for Aster).
- Used by: `AutoTrader` execution path.

**Persistence & Learning:**
- Purpose: Audit trail, performance analytics, self-learning gates on new opens.
- Location: `logger/`, `memory/`, `stats/`
- Contains: Per-cycle JSON logs in `decision_logs/{trader_id}/`; trade episodes in `trade_memory/{trader_id}/`; error classification.
- Depends on: Local filesystem.
- Used by: `AutoTrader`, API log endpoints, `memory.GateOpenDecision`.

**Configuration:**
- Purpose: Static bootstrap + runtime-mutable trader settings.
- Location: `config/config.go`, root `config.json`
- Contains: `Config`, `TraderConfig`, leverage/risk/margin validation structs.
- Depends on: JSON file I/O.
- Used by: `main.go`, `TraderManager` (persists hot updates).

## Data Flow

### Primary Trading Cycle

1. **Ticker fires** — `AutoTrader.Run` receives tick every `scan_interval_minutes` (`trader/auto_trader.go:357-375`).
2. **Context build** — `buildTradingContext()` loads balance, positions, candidate coins from pool, injects trade memory into positions (`trader/auto_trader.go:604-613`).
3. **Hard risk gates** — Daily loss / max drawdown pause (`shouldPauseForRisk`); peak-hour LLM skip when flat (`shouldSkipForPeakHour`, `trader/auto_trader.go:615-638`).
4. **Decision engine** — `decision.GetFullDecision(ctx)` (`decision/engine.go:14-83`):
   - Fetch market data for all symbols in context.
   - Optionally record context to `data/records/` when `EnableRecording` is on.
   - Run `PromptStrategy.GenerateAutoDecisions` (auto SL/TP rules bypass LLM if triggered).
   - Build system/user prompts via strategy (A/B/V).
   - Call LLM through per-trader `mcp.Client`.
   - Parse JSON decisions; run `validateDecisions` + strategy `ExtraValidate`.
5. **Self-learning gate** — For open actions, `memory.TradeMemory.GateOpenDecision` may reject, shrink size, or approve (`trader/auto_trader.go:756-782`).
6. **Execution** — Decisions sorted close-before-open; `executeDecisionWithRecord` calls `Trader` interface methods (`trader/auto_trader.go:733-796`).
7. **Logging** — Full cycle written as JSON via `DecisionLogger.LogDecision` to `decision_logs/{trader_id}/` (`logger/decision_logger.go`).

### HTTP Read Path (Dashboard)

1. Browser loads SPA from nginx (`nginx.conf` → `web/dist`).
2. SWR polls e.g. `GET /api/account?trader_id=X` (`web/src/App.tsx`, `web/src/lib/api.ts`).
3. Nginx proxies `/api/` to Go container `:8080` (`nginx.conf:27-28`).
4. Gin handler in `api/server.go` calls `TraderManager.GetTrader` → `AutoTrader` snapshot methods.
5. JSON response rendered in React components (equity chart, positions, decisions).

### Config Hot-Update Path

1. `PUT /api/config?trader_id=X` with JSON patch (`api/server.go`).
2. `TraderManager.UpdateRuntimeConfig` applies patch to in-memory `RuntimeConfig` and persists relevant fields back to `config.json` (`manager/trader_manager.go:186+`).
3. Scan interval changes propagate via `scanIntervalCh` to reset ticker (`trader/auto_trader.go:376-378`).

**State Management:**
- **In-process:** Each `AutoTrader` holds runtime config, pause flags, peak-hour state, cycle counter, LLM cost tracker.
- **Shared mutable globals (caution):** `decision.SetErrorStats` / `SetMinRiskReward` per cycle; `market.SetFAPIBaseURL` process-wide.
- **Durable:** Filesystem only — `decision_logs/`, `trade_memory/`, `config.json`, optional `data/records/`.

## Key Abstractions

**`Trader` interface:**
- Purpose: Exchange-agnostic trading operations.
- Examples: `NewFuturesTrader` (Binance), `NewHyperliquidTrader`, `NewAsterTrader`, wrapped by `NewPaperTraderWithConfig`.
- Pattern: Adapter + optional Decorator (paper trading).

**`PromptStrategy` interface:**
- Purpose: Pluggable prompt templates and pre/post validation rules.
- Examples: `decision.StrategyA`, `StrategyB`, `StrategyV` in `decision/strategies.go`; selected via `prompt_strategy` in config (`manager/trader_manager.go:111-119`).
- Pattern: Strategy pattern.

**`decision.Context`:**
- Purpose: Single-cycle snapshot passed through fetch → prompt → validate → execute.
- Examples: Defined in `decision/types.go`; built in `AutoTrader.buildTradingContext`.
- Pattern: Context object / DTO.

**`mcp.Client` + `decision.AICaller`:**
- Purpose: Isolate LLM HTTP details from decision logic.
- Examples: One client per trader in `NewAutoTrader` (`trader/auto_trader.go:188-207`).
- Pattern: Adapter; interface segregation via `AICaller`.

**`AutoTrader`:**
- Purpose: Facade orchestrating one AI trader's full lifecycle.
- Examples: `trader/auto_trader.go`.
- Pattern: Facade + periodic worker.

**`TraderManager`:**
- Purpose: Multi-trader registry and fleet operations (start all, pause all, competition).
- Examples: `manager/trader_manager.go`.
- Pattern: Registry / service locator.

**`memory.TradeMemory`:**
- Purpose: Self-learning from closed trades — similarity search, rule Gate, optional OpenGuard LLM reviewer.
- Examples: `memory/trade_memory.go`, `memory/agents.go`.
- Pattern: Event-sourced JSONL + in-memory episode index.

## Entry Points

**Production binary:**
- Location: `main.go`
- Triggers: `./nofx [config.json] [--fresh]` or Docker `command` in `docker-compose.yml`
- Responsibilities: Load config, wire coin pool, create `TraderManager`, start Gin API goroutine, `StartAll` traders, graceful shutdown on SIGINT/SIGTERM.

**HTTP API:**
- Location: `api/server.go` — `Server.Start()` listens on `APIServerPort` (default 8080).
- Triggers: Incoming HTTP from nginx or direct curl.
- Responsibilities: Route to trader snapshots, log file serving, system pause, position close, config CRUD.

**Frontend:**
- Location: `web/src/main.tsx` → `web/src/App.tsx`
- Triggers: Browser navigation.
- Responsibilities: Poll API, render competition/trader/logs pages.

**CLI / dev tools:**
- Location: `cmd/analyze_logs/main.go`, `cmd/debug_strategy/main.go`, `cmd/test_strategy/main.go`, `cmd/test_records/main.go`, `cm/backtest/main.go`
- Triggers: Manual `go run` for log analysis, strategy debugging, backtest.
- Responsibilities: Offline analysis without live trading loop.

**Auxiliary services:**
- Location: `tools/coin_pool_server/main.go` — optional local coin pool API for development.

## Architectural Constraints

- **Threading:** One goroutine per `AutoTrader.Run` loop; `TraderManager` uses `sync.RWMutex`. Individual cycles are sequential within a trader; no parallel cycles per trader.
- **Global state:** `decision` package uses cycle-scoped globals for error stats (`SetErrorStats`); `market.fapiBaseURL` is process-wide; `pool` package has package-level config vars set at startup from `main.go`.
- **Circular imports:** Avoided via interfaces — `decision.AICaller` decouples from concrete `mcp.Client`; `decision` imports `logger` types for performance snapshots only.
- **Proxy requirement:** Production Binance access typically requires V2Ray SOCKS5; `market/data.go` auto-detects proxy from env or `/usr/local/etc/v2ray/config.json`.
- **No auth on API:** REST endpoints are unauthenticated — intended for private network / Docker internal use only.

## Anti-Patterns

### Package-level mutable globals in decision/market

**What happens:** `decision.SetErrorStats`, `decision.SetMinRiskReward`, and `market.SetFAPIBaseURL` mutate package globals shared across all traders in one process.

**Why it's wrong:** Concurrent traders can overwrite each other's error-stats context between cycles; testnet/mainnet FAPI URL is one setting for the whole process.

**Do this instead:** Pass error recorder and FAPI base URL on `decision.Context` or inject dependencies into `AutoTrader` — see existing `ctx` pattern in `decision/types.go`.

### Monolithic AutoTrader file

**What happens:** `trader/auto_trader.go` exceeds 2000 lines mixing loop, execution, risk, peak-hour, and context building.

**Why it's wrong:** Hard to test and navigate; changes to execution risk touching unrelated peak-hour logic.

**Do this instead:** Extract `runCycle` phases into focused files (`context.go`, `execute.go`, `risk.go`) following existing package conventions.

### Dual config persistence paths

**What happens:** Bootstrap reads `config.json`; runtime updates write back via `TraderManager` while some state (peak-hour flags) lives only on `AutoTrader` fields.

**Why it's wrong:** Restart may not restore all UI-toggled state; operators must know which settings persist.

**Do this instead:** Document persisted fields in `config/config.go` comments; extend `RuntimeConfigSnapshot` for any field exposed in API.

## Error Handling

**Strategy:** Log and continue at cycle level; classify errors for dashboards.

**Patterns:**
- Cycle errors in `runCycle` set `record.Success = false`, log via `DecisionLogger`, increment `stats.ErrorStats` — loop continues on next tick.
- Decision pipeline classifies failures: market data, LLM, parse, validation (`stats.Classify*` in `decision/engine.go`).
- Trade execution errors recorded per `DecisionAction` without aborting remaining decisions in the same cycle.
- API handlers return HTTP 4xx/5xx with JSON error messages via Gin.

## Cross-Cutting Concerns

**Logging:** Standard library `log` to stdout; structured JSON decision logs to `decision_logs/{trader_id}/`; Docker captures stdout.

**Validation:** Layered — auto-strategy rules (`GenerateAutoDecisions`), generic `validateDecisions` (margin, SL distance, RR), strategy `ExtraValidate`, memory Gate before opens.

**Authentication:** None on API; exchange/API keys in `config.json` or Docker env vars (`docker-compose.yml`).

---

*Architecture analysis: 2026-07-10*
