# Testing Patterns

**Analysis Date:** 2026-07-10

## Test Framework

**Runner:**

- **Go:** Standard library `testing` package (Go 1.25.0, `go.mod`). No testify, gomock, or third-party assertion libraries.
- **Frontend:** No Vitest, Jest, or React Testing Library — `web/package.json` has no test script or test devDependencies.

**Assertion Library:**

- Go built-in: `t.Fatalf`, `t.Fatal`, boolean checks with custom messages.
- Error assertions via `strings.Contains(err.Error(), "期望子串")` — see `decision/validation_adaptive_stop_loss_test.go`.

**Run Commands:**

```bash
# Unit tests (only decision package has _test.go files today)
go test ./decision/ -v

# Compile-check entire module (downloads deps; most packages have no tests)
go test ./... -count=0

# Strategy replay backtest (calls live LLM unless -dry-run)
go run cmd/test_strategy/main.go \
  -logs=decision_logs/binance_deepseek_paper_strategyA \
  -strategy=B \
  -output=results/strategyB_on_A_data.json

# Records-based virtual account backtest
go run cmd/test_records/main.go \
  -records=data/records/binance_deepseek_paper_strategyA \
  -strategy=B -start=1 -end=5 \
  -output=results/test_backtest_fix.json

# Shell wrappers
./scripts/test_strategy_quick.sh B A 1 50
./scripts/test_backtest_quick.sh
./scripts/verify_backtest_setup.sh
```

## Test File Organization

**Location:**

- **Go unit tests:** Co-located `*_test.go` in the same package directory — currently only `decision/validation_adaptive_stop_loss_test.go`.
- **Manual / example test:** Root `test_symbol_validation.go` with `//go:build manual` — not run by default `go test`.
- **Integration CLIs:** `cmd/test_strategy/main.go`, `cmd/test_records/main.go`, `cmd/debug_strategy/main.go`, `cm/backtest/main.go`.
- **Frontend:** No `*.test.ts` or `*.spec.tsx` files under `web/`.

**Naming:**

- Go: `Test<FunctionOrScenario><ExpectedOutcome>` — e.g. `TestValidateDecisionAdjustsTooCloseLongStopAndShrinksPosition`.
- Test helpers: `testAdaptiveContext()` (lowercase, not `Test*`).

**Structure:**

```
nofx/
├── decision/
│   └── validation_adaptive_stop_loss_test.go   # only automated unit tests
├── test_symbol_validation.go                   # manual build tag
├── cmd/
│   ├── test_strategy/main.go                     # log replay + LLM
│   ├── test_records/main.go                      # records replay
│   └── debug_strategy/main.go                    # strategy debug
├── cm/backtest/main.go                           # BacktestConfig example
├── scripts/
│   ├── test_strategy_quick.sh
│   ├── test_backtest_quick.sh
│   ├── test_records_quick.sh
│   └── verify_backtest_setup.sh
└── analysis/                                     # Python notebooks/scripts (ad-hoc analysis, not CI)
    ├── strategy_analysis.py
    └── compare_llm_decisions.py
```

## Test Structure

**Suite Organization:**

```go
// decision/validation_adaptive_stop_loss_test.go

func testAdaptiveContext() *Context {
    return &Context{
        Account: AccountInfo{TotalEquity: 1000, AvailableBalance: 1000},
        MarketDataMap: map[string]*market.Data{
            "BTCUSDT": {Symbol: "BTCUSDT", CurrentPrice: 100, IntradayATR14: 1},
        },
        BTCETHLeverage:  5,
        AltcoinLeverage: 5,
        StopLossDistance: StopLossDistanceConfig{ /* ... */ },
        AssumedTakerFeeRate: 0.0004,
        AssumedSlippageRate: 0.0005,
    }
}

func TestValidateDecisionAdjustsTooCloseLongStopAndShrinksPosition(t *testing.T) {
    oldRR := minRiskReward
    minRiskReward = 2.2
    defer func() { minRiskReward = oldRR }()

    d := Decision{Symbol: "BTCUSDT", Action: ActionOpenLong, /* ... */}
    if err := coreValidateDecision(&d, testAdaptiveContext()); err != nil {
        t.Fatalf("coreValidateDecision returned error: %v", err)
    }
    if d.StopLoss != 99 {
        t.Fatalf("expected adjusted stop loss 99, got %.8f", d.StopLoss)
    }
    // ...
}
```

**Patterns:**

- **Setup:** Private helper builds minimal `Context` with controlled market data — no external APIs.
- **Package-level state:** Tests temporarily mutate unexported vars (`minRiskReward`) and restore via `defer`.
- **Teardown:** No explicit teardown; pure in-memory validation.
- **Assertions:** Direct field checks + substring match on Chinese error messages.
- **Not yet used:** Table-driven `[]struct{ name, input, want }` tests — each scenario is a separate `Test*` function.

## Mocking

**Framework:** None in unit tests. Simulation via alternate implementations and recorded data.

**Patterns:**

- **`Trader` interface:** `trader/paper_trader.go` implements full exchange surface for paper trading — used in live “paper” mode, not in `_test.go` files today.
- **`AICaller` interface:** `decision/types.go` abstracts LLM; production uses `*mcp.Client`. Backtest can skip LLM via `EnableAI: false` or `-dry-run`.
- **Recorded fixtures:** Decision cycles saved as JSON under `decision_logs/<trader_id>/` and `data/records/` — replayed by `decision/backtest_from_logs.go`, `decision/backtest.go`, `decision/backtest_simulator.go`.
- **No HTTP mocks:** Binance/Hyperliquid clients hit real APIs in integration; paper mode avoids orders.

**What to Mock:**

- Prefer testing pure validation/parsing logic with constructed `Context` (current approach in `decision/`).
- For new unit tests around `trader/` or `market/`, introduce interface seams or fake `Trader` rather than live exchange calls.

**What NOT to Mock:**

- Core validation math (`coreValidateDecision`, stop-loss distance) — test with real `market.Data` structs.
- JSON parsing paths — feed representative AI response strings into `parseFullDecisionResponse` (good candidate for future tests).

## Fixtures and Factories

**Test Data:**

- **Decision logs:** `decision_logs/binance_deepseek_paper_strategy{A,B,V}/decision_*.json` — real captured cycles for replay.
- **Market records:** `data/records/binance_deepseek_paper_strategyA/` — used by `cmd/test_records/main.go` and `decision/backtest.go`.
- **Results output:** `results/*.json` — backtest output (gitignored or local artifacts).
- **Config template:** `config.example.paper-trading.json` for paper-trading setup.

**Location:**

- Runtime-generated logs: `decision_logs/<trader_id>/`
- Static analysis: `analysis/` Python scripts read logs externally
- In-test factories: `testAdaptiveContext()` in `decision/validation_adaptive_stop_loss_test.go`

**Factory pattern to follow when adding tests:**

```go
func testAdaptiveContext() *Context { /* minimal valid context */ }
```

Extend with optional overrides rather than duplicating large struct literals.

## Coverage

**Requirements:** None enforced. No `go test -cover` in scripts or CI.

**View Coverage:**

```bash
go test ./decision/ -coverprofile=coverage.out
go tool cover -func=coverage.out
```

**Current state (2026-07-10):**

- **3 passing unit tests** in `decision/` (adaptive stop-loss validation).
- **0% automated coverage** for `trader/`, `api/`, `market/`, `mcp/`, `memory/`, frontend.
- Primary quality signal is **log replay backtest** + **paper trading** on ali-server, not unit coverage.

## Test Types

**Unit Tests:**

- Scope: Pure logic in `decision` package (validation, parsing, leverage clip).
- Run: `go test ./decision/ -v` — completes in ~2s, no network required for current tests.
- Gap: `parseFullDecisionResponse`, `extractDecisions`, `config.LoadConfig`, API handlers untested.

**Integration Tests:**

- **Strategy replay:** `cmd/test_strategy/main.go` re-runs prompt strategy against historical `decision_logs` with optional LLM calls.
- **Records backtest:** `cmd/test_records/main.go` + `decision.RunBacktest` simulate equity curve from recorded market snapshots.
- **Dry-run:** `-dry-run` flag analyzes log metadata without LLM spend (`cmd/test_strategy/main.go`).
- **Requires:** `DEEPSEEK_OPENAI_KEY` or `QWEN_OPENAI_API_KEY` for full replay; scripts check env (`scripts/test_strategy_quick.sh`).

**E2E Tests:**

- Not used in repo. Manual verification via:
  - Docker stack: `./start.sh start` (`Dockerfile`, `docker-compose.yml`)
  - Web UI at port 3000, API at 8080
  - Paper trading mode in `config.json` (`paper_trading_mode: true`)
  - ali-server production monitoring

**Ad-hoc Analysis (not automated tests):**

- Python in `analysis/` — `strategy_analysis.py`, `compare_llm_decisions.py`, Jupyter notebook `strategy_analysis.ipynb`
- Shell verification: `scripts/verify_backtest_setup.sh` checks directories, compiles `cmd/test_strategy`, prints example commands

## CI/CD

**Pipeline:** No GitHub Actions workflow for tests. `.github/` contains only `ISSUE_TEMPLATE/feature_request.md`.

**Local gates before deploy:**

```bash
go build -o /tmp/nofx .                    # main binary
go build cmd/test_strategy/main.go         # backtest tool
cd web && npm run build                    # tsc + vite (strict TS)
./scripts/verify_backtest_setup.sh         # optional backtest sanity
```

## Common Patterns

**Async Testing:**

- Not applicable to current Go unit tests (synchronous).
- Frontend: no automated async tests; SWR polling tested manually in browser.

**Error Testing:**

```go
err := coreValidateDecision(&d, testAdaptiveContext())
if err == nil || !strings.Contains(err.Error(), "必须≥50.00") {
    t.Fatalf("expected adjusted-position-size rejection, got %v", err)
}
```

Use Chinese substring from production error messages — stable contract for validation tests.

**Build Tags:**

```go
//go:build manual
// +build manual
```

Place exploratory or hand-run tests behind `manual` tag (`test_symbol_validation.go`). Run with:

```bash
go run -tags manual test_symbol_validation.go
```

**Backtest verification checklist** (from `scripts/test_backtest_quick.sh`):

1. Equity curve changes (not flat)
2. `total_trades > 0` when strategy trades
3. Return computed from virtual account
4. Sharpe ratio derived from new strategy equity curve

## Adding New Tests

**New validation / parser unit test:**

- File: `decision/<feature>_test.go`, same `package decision`
- Call unexported helpers directly (same package)
- Build context via factory function; avoid network and filesystem

**New strategy behavior test:**

- Prefer replay: `./scripts/test_strategy_quick.sh <new> <data_source> 1 20`
- Or extend `cmd/test_strategy/main.go` strategy switch for new `PromptStrategy` type

**New frontend behavior:**

- No framework installed — add Vitest + RTL to `web/` if automated UI tests are required
- Until then: type-check via `npm run build` and manual UAT against `/api` endpoints

**New exchange adapter test:**

- Implement against `Trader` interface; exercise via paper trader or recorded fills
- Do not commit API keys; use `config.example.paper-trading.json` and env vars

## Priority Gaps

| Area | What's not tested | Risk | Suggested approach |
|------|-------------------|------|--------------------|
| `decision/parser.go` | AI JSON extraction edge cases | High — bad parse skips trades | Table tests with fixture strings |
| `decision/validation.go` | Margin, leverage, symbol rules | High — rejects valid/accepts invalid | Extend pattern from `validation_adaptive_stop_loss_test.go` |
| `api/server.go` | HTTP handlers | Medium | `httptest` + gin test router |
| `market/` | Kline/OI fetch | Medium | Interface + recorded JSON fixtures |
| `web/` | Components, API client | Medium | Add Vitest when investing in UI tests |
| `mcp/client.go` | LLM retries, auth | Medium | httptest.Server mock OpenAI shape |

---

*Testing analysis: 2026-07-10*
