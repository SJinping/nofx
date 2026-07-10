# Codebase Structure

**Analysis Date:** 2026-07-10

## Directory Layout

```
nofx/
├── main.go                 # Production entry: config load, TraderManager, API, traders
├── config.json             # Runtime config (traders, leverage, API keys) — gitignored in prod
├── go.mod / go.sum         # Go module (module name: nofx)
├── Dockerfile              # Multi-stage: Go backend + Node frontend build
├── docker-compose.yml      # nofx backend :8080 + nginx frontend :3000
├── nginx.conf              # SPA + /api reverse proxy to nofx-trading:8080
├── start.sh                # Local/Docker service management script
│
├── api/                    # Gin HTTP server and REST handlers
├── config/                 # config.json schema and LoadConfig
├── decision/               # AI decision engine, prompts, validation, backtest
├── logger/                 # Decision cycle JSON logging + trade stats cache
├── manager/                # TraderManager — multi-trader registry
├── market/                 # Binance FAPI market data and indicators
├── mcp/                    # LLM clients (DeepSeek, Qwen, custom)
├── memory/                 # Trade memory, self-learning Gate/OpenGuard
├── pool/                   # Candidate coin pool (AI500, OI Top, defaults)
├── stats/                  # Error classification and persistence
├── trader/                 # AutoTrader loop + exchange adapters
│
├── cmd/                    # Standalone CLI tools (not imported by main)
│   ├── analyze_logs/
│   ├── debug_strategy/
│   ├── test_records/
│   └── test_strategy/
├── cm/backtest/            # Backtest runner entry
├── tools/coin_pool_server/ # Dev coin-pool mock server
│
├── web/                    # React + Vite + Tailwind frontend
│   ├── src/
│   │   ├── App.tsx         # Main SPA shell and trader dashboard
│   │   ├── main.tsx        # React DOM entry
│   │   ├── lib/api.ts      # REST client (/api/*)
│   │   ├── components/     # Charts, competition, AI learning UI
│   │   ├── pages/          # LogViewer page
│   │   ├── contexts/       # i18n LanguageContext
│   │   ├── i18n/           # translations.ts
│   │   └── types/          # TypeScript API types
│   ├── Dockerfile
│   └── package.json
│
├── decision_logs/          # Per-trader JSON decision logs (runtime data)
├── trade_memory/           # Per-trader JSONL trades + analyses (runtime data)
├── data/                   # Recorded contexts for backtest (records/, case/)
├── v2ray/ / v2ray_cli/     # Proxy config and node testing (deployment)
├── scripts/                # Shell helpers for backtest/records verification
├── analysis/               # Python ad-hoc analysis scripts
├── bin/                    # Prebuilt helper binaries
└── .planning/codebase/     # GSD codebase map documents (this folder)
```

## Directory Purposes

**Root (`/`):**
- Purpose: Application entry, deployment manifests, primary config.
- Contains: `main.go`, Docker/nginx, `config.json`, docs (`README.md`, `DOCKER_DEPLOY.md`).
- Key files: `main.go`, `docker-compose.yml`, `start.sh`.

**`api/`:**
- Purpose: HTTP control plane for dashboard and ops.
- Contains: Single package — `server.go` (~1800 lines) with all Gin routes and handlers.
- Key files: `api/server.go`.

**`config/`:**
- Purpose: Typed configuration loaded from JSON.
- Contains: `Config`, `TraderConfig`, leverage/risk structs, `LoadConfig`, validation.
- Key files: `config/config.go`.

**`decision/`:**
- Purpose: Core AI trading logic — prompts, parsing, validation, strategies, backtest.
- Contains: `engine.go`, `prompts.go`, `strategies.go`, `validation.go`, `parser.go`, `types.go`, `backtest*.go`, `recorder.go`.
- Key files: `decision/engine.go`, `decision/strategies.go`, `decision/types.go`.

**`trader/`:**
- Purpose: Trading loop and exchange integrations.
- Contains: `auto_trader.go` (orchestrator), `interface.go`, exchange impls, `runtime_config.go`, `paper_trader.go`, `llm_cost_tracker.go`.
- Key files: `trader/auto_trader.go`, `trader/interface.go`, `trader/binance_futures.go`.

**`manager/`:**
- Purpose: Fleet management for multiple concurrent AI traders.
- Contains: `trader_manager.go` only.
- Key files: `manager/trader_manager.go`.

**`market/`:**
- Purpose: External market data fetching and indicator computation.
- Contains: `data.go` (FAPI, proxy), `nofxdata.go`.
- Key files: `market/data.go`.

**`mcp/`:**
- Purpose: LLM HTTP client abstraction.
- Contains: `client.go`, `cost.go`.
- Key files: `mcp/client.go`.

**`memory/`:**
- Purpose: Self-learning from historical trades.
- Contains: `trade_memory.go`, `storage.go`, `vector.go`, `agents.go`, `types.go`.
- Key files: `memory/trade_memory.go`.

**`logger/`:**
- Purpose: Persist each decision cycle as JSON; trade statistics cache for API.
- Contains: `decision_logger.go`, `trade_stats_cache.go`.
- Key files: `logger/decision_logger.go`.

**`pool/`:**
- Purpose: Resolve which symbols each cycle can trade.
- Contains: `coin_pool.go` — AI500 API, OI Top, default coin list, disk cache.
- Key files: `pool/coin_pool.go`.

**`stats/`:**
- Purpose: Error taxonomy and per-trader error stats files.
- Contains: `error_stats.go`.
- Key files: `stats/error_stats.go`.

**`web/`:**
- Purpose: Monitoring UI — competition, trader detail, logs, config controls.
- Contains: Vite React app with Tailwind, SWR data fetching, lightweight-charts/recharts.
- Key files: `web/src/App.tsx`, `web/src/lib/api.ts`, `web/src/components/CompetitionPage.tsx`.

**`cmd/`:**
- Purpose: Offline/dev executables separate from production binary.
- Contains: One `main.go` per subdirectory.
- Key files: `cmd/analyze_logs/main.go`, `cmd/test_strategy/main.go`.

**Runtime data dirs (`decision_logs/`, `trade_memory/`, `data/`):**
- Purpose: Durable artifacts written at runtime; mounted as Docker volumes.
- Generated: Yes, at runtime.
- Committed: Typically no (local/server state).

## Key File Locations

**Entry Points:**
- `main.go`: Production server — loads config, starts API and all traders.
- `web/src/main.tsx`: Frontend bootstrap.
- `api/server.go`: `Server.Start()` — Gin listener.
- `cmd/*/main.go`: CLI tools for analysis/backtest.
- `cm/backtest/main.go`: Backtest entry.
- `tools/coin_pool_server/main.go`: Optional dev coin pool.

**Configuration:**
- `config.json`: Primary runtime config (traders, keys, intervals, risk limits).
- `config/config.go`: Go types and loaders.
- `docker-compose.yml`: Container env vars and volume mounts.
- `nginx.conf`: Frontend routing and API proxy.
- `v2ray/config.json`: Proxy node config (deployment, not app code).

**Core Logic:**
- `trader/auto_trader.go`: Trading loop (`Run`, `runCycle`, execution).
- `decision/engine.go`: `GetFullDecision` pipeline.
- `decision/prompts.go`: Prompt text builders for strategies A/B/V.
- `decision/validation.go`: Decision validation and auto SL/TP rules.
- `manager/trader_manager.go`: Multi-trader lifecycle.
- `memory/trade_memory.go`: Self-learning Gate on opens.

**Testing:**
- `decision/validation_adaptive_stop_loss_test.go`: Unit test in decision package.
- `test_symbol_validation.go`: Root-level validation test file.
- `scripts/test_*_quick.sh`: Integration smoke scripts.

## Naming Conventions

**Files:**
- Go source: `snake_case.go` (e.g. `auto_trader.go`, `binance_futures.go`).
- Go tests: `*_test.go` alongside source.
- React components: PascalCase `.tsx` (e.g. `CompetitionPage.tsx`, `EquityChart.tsx`).
- React utilities: camelCase `.ts` (e.g. `api.ts`).

**Directories:**
- Go packages match directory name (`package trader` in `trader/`).
- `cmd/{tool_name}/` for standalone commands.
- Frontend under `web/src/{components,pages,lib,contexts,i18n,types}/`.

**Types:**
- Go exported types: PascalCase (`AutoTrader`, `DecisionRecord`, `PromptStrategy`).
- Go private helpers: camelCase (`buildTradingContext`, `runCycle`).
- JSON/config fields: snake_case (`scan_interval_minutes`, `prompt_strategy`).
- Trader IDs: snake_case strings used as directory names (`decision_logs/{trader_id}/`).

**Functions:**
- Constructors: `New*` (`NewAutoTrader`, `NewServer`, `NewDecisionLogger`).
- Interface methods: PascalCase verb phrases (`GetBalance`, `BuildSystemPrompt`).
- HTTP handlers: `handle*` (`handleAccount`, `handleCompetition`).

## Where to Add New Code

**New exchange adapter:**
- Implementation: `trader/{exchange}_trader.go` implementing `Trader` in `trader/interface.go`.
- Wire-up: Add case in `NewAutoTrader` switch (`trader/auto_trader.go:223-245`).
- Config fields: `config/config.go` `TraderConfig` + JSON tags.

**New prompt strategy (e.g. Strategy C):**
- Strategy struct + methods: `decision/strategies.go` (implement `PromptStrategy`).
- Prompt text: `decision/prompts.go` (add `buildSystemPromptC` / `buildUserPromptC`).
- Registration: `manager/trader_manager.go` switch on `prompt_strategy` config value.

**New decision validation rule:**
- Generic rules: `decision/validation.go` (`validateDecisions`, `GenerateAutoDecisions`).
- Strategy-specific: `ExtraValidate` on the strategy struct in `decision/strategies.go`.

**New REST endpoint:**
- Route: `api/server.go` `setupRoutes()`.
- Handler: same file as `handle*` method; delegate to `TraderManager` or filesystem.

**New dashboard panel:**
- Component: `web/src/components/{Name}.tsx`.
- Data fetch: Add method to `web/src/lib/api.ts`; add types in `web/src/types/` or `web/src/types.ts`.
- Integrate: Import in `web/src/App.tsx` or relevant page.

**New self-learning rule:**
- Gate logic: `memory/trade_memory.go` (`GateOpenDecision`).
- Optional LLM reviewer: `memory/agents.go`.

**New CLI/analysis tool:**
- Entry: `cmd/{tool_name}/main.go`.
- Reuse: Import `decision`, `logger`, `market` packages — do not import `main`.

**Utilities:**
- Shared Go helpers: add to the most relevant existing package; avoid new top-level packages unless cross-cutting.
- Shell automation: `scripts/`.
- Python one-off analysis: `analysis/`.

## Special Directories

**`decision_logs/`:**
- Purpose: One subdirectory per `trader_id`; JSON file per decision cycle.
- Generated: Yes, by `logger.DecisionLogger`.
- Committed: No (runtime/ops data).

**`trade_memory/`:**
- Purpose: Per-trader `trades.jsonl` and `analyses/` for self-learning.
- Generated: Yes, by `memory.TradeMemory`.
- Committed: No.

**`data/records/`:**
- Purpose: Serialized `RecordedContext` when `enable_recording` is true — used for offline backtest.
- Generated: Yes, by `decision/recorder.go`.
- Committed: No.

**`web/dist/`:**
- Purpose: Vite production build output; copied into Docker shared volume.
- Generated: Yes (`npm run build`).
- Committed: Optional (often built in Docker).

**`.cursor/`:**
- Purpose: Cursor/GSD tooling (agents, skills, workflows) — not part of trading runtime.
- Committed: Mixed; unrelated to `nofx` binary behavior.

**`.gocache/`:**
- Purpose: Local Go build cache.
- Generated: Yes.
- Committed: No.

---

*Structure analysis: 2026-07-10*
