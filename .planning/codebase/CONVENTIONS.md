# Coding Conventions

**Analysis Date:** 2026-07-10

## Naming Patterns

**Files:**

- **Go backend:** One package per directory; files use `snake_case.go` for multi-word names (`auto_trader.go`, `decision_logger.go`, `validation_adaptive_stop_loss_test.go`). Single-concept files use plain names (`engine.go`, `types.go`, `parser.go`).
- **Go commands:** Under `cmd/<tool>/main.go` — e.g. `cmd/test_strategy/main.go`, `cmd/analyze_logs/main.go`.
- **Frontend:** React components and pages in `PascalCase.tsx` (`EquityChart.tsx`, `CompetitionPage.tsx`, `LogViewer.tsx`). Utilities in `camelCase.ts` (`api.ts`, `translations.ts`).
- **Scripts:** Shell helpers in `scripts/` with descriptive snake_case names (`test_strategy_quick.sh`, `verify_backtest_setup.sh`).
- **Config:** Root `config.json` (runtime); `config.example.paper-trading.json` as template.

**Functions:**

- **Go exported:** `PascalCase` — `GetFullDecision`, `LoadConfig`, `NewAutoTrader`, `RunBacktest`.
- **Go unexported:** `camelCase` — `validateDecisions`, `coreValidateDecision`, `fetchMarketDataForContext`, `stopLossMinDistance`.
- **Go handlers:** `handle*` prefix on API methods — `handleHealth`, `handleAccount`, `handleLatestDecisions` in `api/server.go`.
- **TypeScript:** `camelCase` for functions and hooks — `getCompetition`, `useLanguage`, `handleSetLanguage`.
- **React components:** `PascalCase` named exports — `export function EquityChart`, `export function LanguageProvider`.

**Variables:**

- **Go:** `camelCase` locals and fields; acronyms follow Go style (`AIModel`, `APIKey`, `BTCETHLeverage`, `LLMCostUSDT`).
- **JSON / API:** `snake_case` tags on Go structs and matching TypeScript interface fields — `total_equity`, `trader_id`, `scan_interval_minutes`.
- **Constants:** Go `const` blocks with PascalCase type names and snake_case string values for error types — `ErrLLMAPITimeout ErrorType = "llm_api_timeout"` in `stats/error_stats.go`.
- **Action strings:** Lowercase snake_case domain literals — `open_long`, `open_short`, `update_stop_loss` in `decision/types.go` and `logger/decision_logger.go`.

**Types:**

- **Go structs:** `PascalCase` with inline Chinese doc comments — `DecisionRecord`, `AutoTraderConfig`, `BacktestConfig`.
- **Go interfaces:** Noun or role name — `Trader` (`trader/interface.go`), `AICaller`, `PromptStrategy` (`decision/types.go`, `decision/strategies.go`).
- **Strategy implementations:** Empty struct types — `StrategyA`, `StrategyB`, `StrategyV` implementing `PromptStrategy`.
- **TypeScript:** `PascalCase` interfaces mirroring API payloads — `SystemStatus`, `AccountInfo`, `DecisionRecord` in `web/src/types/index.ts`.

## Code Style

**Formatting:**

- **Go:** Standard `gofmt` / Go toolchain formatting (tabs, no trailing style config). Module path `nofx`, Go **1.25.0** (`go.mod`).
- **TypeScript:** Strict compiler options in `web/tsconfig.json` — `strict: true`, `noUnusedLocals`, `noUnusedParameters`, `noFallthroughCasesInSwitch`. Build via `tsc && vite build` (`web/package.json`).
- **CSS:** Tailwind utility classes inline in JSX; `tailwind.config.js` and `postcss.config.js` at `web/`.
- **No project-level ESLint, Prettier, Biome, or `.editorconfig` detected.**

**Linting:**

- **Go:** No `golangci-lint` or pre-commit hooks in repo.
- **Frontend:** TypeScript compiler acts as the primary static checker; no ESLint config.

## Import Organization

**Order (Go):**

1. Standard library (`fmt`, `log`, `encoding/json`, …)
2. Third-party (`github.com/gin-gonic/gin`, `github.com/adshao/go-binance/v2`, …)
3. Local module `nofx/...` packages

Example from `main.go`:

```go
import (
    "fmt"
    "log"
    "nofx/api"
    "nofx/config"
    ...
)
```

**Path aliases:**

- Go module import path: `nofx/<package>` — e.g. `nofx/decision`, `nofx/trader`.
- Package alias when name collision: `traderpkg "nofx/trader"` in `api/server.go`.
- Frontend: relative imports with `../types`, `./components/...`; no path alias configured in `tsconfig.json`.

**Frontend import order (observed):**

1. React / third-party (`react`, `swr`, `recharts`, `clsx`)
2. Local lib, contexts, i18n
3. Local components and types

## Error Handling

**Patterns:**

- **Wrap and propagate:** Use `fmt.Errorf("中文描述: %w", err)` when bubbling errors up the call stack — common in `decision/engine.go`, `trader/auto_trader.go`, `decision/validation.go`.
- **Classify for metrics:** Call `stats.Classify*Error(err.Error())` and `recordError(...)` before returning — ties failures to the error dashboard (`stats/error_stats.go`, `GET /api/error-stats`).
- **Fatal on startup:** `log.Fatalf("❌ ...")` for config load and irrecoverable init failures in `main.go` and CLI tools.
- **Non-fatal degradation:** Log warning with `log.Printf("⚠️ ...")` and continue — e.g. recording failures in `decision/engine.go`, best-effort exchange queries in `api/server.go`.
- **Validation errors:** Return descriptive Chinese `fmt.Errorf` strings from `coreValidateDecision` and siblings; tests assert via `strings.Contains(err.Error(), "...")`.
- **API layer:** Gin handlers return JSON error bodies and appropriate HTTP status codes; CORS middleware in `api/server.go` allows all origins (`*`).
- **Frontend:** `api.ts` methods throw `new Error('中文消息')` when `!res.ok`; SWR surfaces errors to components.

**Do this instead of silent failure:** Prefer logging + classified error over empty returns on operational paths (market fetch, LLM call, trade execution).

## Logging

**Framework:** Go standard library `log` package; browser `console.log` in a few API helpers (`web/src/lib/api.ts`).

**Patterns:**

- **Emoji-prefixed operational logs** for quick visual scan in Docker/SSH logs:
  - `✓` success, `❌` failure, `⚠️` warning, `📋`/`📊`/`🚀` informational
- **Chinese log messages** throughout backend — matches operator language on ali-server.
- **Startup banner** ASCII art in `main.go`.
- **Gin release mode** to reduce HTTP noise: `gin.SetMode(gin.ReleaseMode)` in `api/server.go`.
- **No structured JSON logging** or zerolog in application code (zerolog appears only as transitive dependency).

## Comments

**When to Comment:**

- **Exported types and functions:** Chinese doc comment above definition explaining purpose and field semantics — see `trader/interface.go`, `decision/types.go`, `config/config.go`.
- **Non-obvious business rules:** Inline comments for formulas and thresholds — e.g. stop-loss distance calculation in `decision/validation.go`, fee/slippage assumptions in config.
- **TODO:** Sparse; tracked items in `decision/engine.go`, `trader/auto_trader.go`, `decision/backtest_from_logs.go`.

**JSDoc/TSDoc:**

- Not used on frontend; types live in `web/src/types/index.ts` with occasional inline `//` comments.

## Function Design

**Size:**

- Large orchestration files are accepted — `trader/auto_trader.go` (~2100 lines), `api/server.go` (~1800 lines), `decision/prompts.go` (~880 lines). New logic should still prefer extracting helpers within the same package over growing monoliths further.

**Parameters:**

- **Context bag pattern:** `decision.Context` carries account, market data, strategy, leverage, and config slices — passed through decision pipeline instead of long parameter lists.
- **Config structs:** Layer-specific config types decouple packages — `AutoTraderConfig`, `BacktestConfig`, `StopLossDistanceConfig` avoid `config` package import cycles in `decision/`.
- **Optional pointers:** Use `*float64` and `*PeakHourPauseConfig` in JSON config for “unset vs zero” semantics (`config/config.go`).

**Return Values:**

- Go idiomatic `(T, error)` for fallible operations.
- Maps and slices used for exchange API flexibility — `GetBalance() (map[string]interface{}, error)`, `GetPositions() ([]map[string]interface{}, error)` on `Trader` interface.
- Frontend async functions return typed `Promise<T>` from `web/src/lib/api.ts`.

## Module Design

**Exports:**

- **Package = bounded context:** `decision`, `trader`, `market`, `mcp`, `logger`, `manager`, `api`, `config`, `memory`, `pool`, `stats`.
- **Interfaces at boundaries:** `Trader` for exchanges, `AICaller` for LLM, `PromptStrategy` for prompt versions — enables paper trading and backtest without live APIs.
- **Unexported pipeline:** `validateDecisions`, `parseFullDecisionResponse`, `callAIDetailed` stay private; public entry points are `GetFullDecision`, `GetFullDecisionFromText`.

**Barrel Files:**

- Not used. Each Go file belongs to one package; frontend imports components directly by path.

## Domain-Specific Conventions

**Trading actions:**

- Use string constants from `decision` package — `ActionOpenLong`, `ActionOpenShort`, etc. — not raw literals in validation code.
- Symbol format: uppercase concatenated pair — `BTCUSDT`, `ETHUSDT`; validated in `decision/validation.go`.

**Prompt strategies:**

- Register new strategies as types implementing `PromptStrategy` in `decision/strategies.go` with `Name()`, `BuildSystemPrompt`, `BuildUserPrompt`, `GenerateAutoDecisions`, `ExtraValidate`.
- Prompt text lives in `decision/prompts.go`; strategy letter (`A`, `B`, `V`) selected via `config.json` → `prompt_strategy`.

**Configuration:**

- Runtime: `config.json` at repo root (or path passed to `main`).
- Hot-update: `PUT /api/config?trader_id=X` and `trader/runtime_config.go`.
- Secrets in JSON fields (`binance_api_key`, `deepseek_key`) — never commit real `config.json`.

**API query params:**

- Trader scoping via `?trader_id=xxx` on most `/api/*` routes (`api/server.go`).

**Frontend data fetching:**

- Use **SWR** with string cache keys and `refreshInterval` polling — see `web/src/App.tsx` (5–10s intervals).
- API base path `/api` (nginx proxy to Go :8080).

**Build tags:**

- Manual-only test scaffolding: `//go:build manual` on `test_symbol_validation.go` — excluded from default `go test`.

---

*Convention analysis: 2026-07-10*
