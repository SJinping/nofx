# Codebase Concerns

**Analysis Date:** 2026-07-10

## Tech Debt

**Monolithic core files:**
- Issue: Core trading and API logic concentrated in two very large files, making changes risky and reviews slow.
- Files: `trader/auto_trader.go` (~2156 lines), `api/server.go` (~1829 lines), `decision/validation.go` (~781 lines), `decision/prompts.go` (~882 lines)
- Impact: High coupling between decision execution, logging, memory, and risk control; difficult to unit-test in isolation.
- Fix approach: Extract execution pipeline (context build → decision → validate → execute), API handlers, and validation rules into smaller packages with clear interfaces.

**Deprecated `io/ioutil` usage:**
- Issue: Widespread use of deprecated `io/ioutil` instead of `io`/`os` APIs.
- Files: `api/server.go`, `market/data.go`, `logger/decision_logger.go`, `pool/coin_pool.go`, `trader/binance_futures.go`, `stats/error_stats.go`
- Impact: Technical deprecation warnings on newer Go versions; inconsistent with current stdlib conventions.
- Fix approach: Mechanical migration to `os.ReadFile`, `os.WriteFile`, `io.ReadAll`.

**Package-level global mutable state in decision layer:**
- Issue: `decision` package uses process-wide globals for error stats, cycle number, and min risk-reward ratio.
- Files: `decision/constants.go` (`currentErrorStats`, `currentCycleNum`, `minRiskReward`); callers in `trader/auto_trader.go` (`decision.SetErrorStats`), `trader/runtime_config.go` (`decision.SetMinRiskReward`)
- Impact: With multiple traders running concurrently (`manager/trader_manager.go` `StartAll` spawns one goroutine per trader), one trader can overwrite another's error-stats context mid-cycle; per-trader `min_risk_reward` hot updates can clobber each other.
- Fix approach: Pass `*stats.ErrorStats` and validation config through `decision.Context` instead of package globals.

**Global MCP client for memory agents:**
- Issue: Trade-memory OpenGuard uses `mcp.CallWithMessagesGlobal` backed by a shared `defaultClient`, while each trader has its own `mcp.Client` in `trader/auto_trader.go`.
- Files: `memory/agents.go`, `memory/trade_memory.go` (`GateOpenDecision`), `mcp/client.go` (`CallWithMessagesGlobal`)
- Impact: Multi-trader competition may send OpenGuard requests with wrong API key/model; race on `defaultClient` config.
- Fix approach: Inject per-trader `*mcp.Client` into `TradeMemory` or pass client into `callOpenGuardLLM`.

**Incomplete stop-loss update on non-Binance exchanges:**
- Issue: `executeUpdateStopLossWithRecord` has a TODO to cancel old stop orders; Binance implements cancel-via-`SetStopLoss` in `trader/binance_futures.go`, but Hyperliquid and Aster do not.
- Files: `trader/auto_trader.go` (line ~1551), `trader/hyperliquid_trader.go` (`SetStopLoss`), `trader/aster_trader.go` (`SetStopLoss`)
- Impact: Duplicate stop-loss orders on Hyperliquid/Aster when AI issues `update_stop_loss`; potential over-close or margin lock.
- Fix approach: Add cancel-before-create logic per exchange, mirroring `cancelOpenAlgoOrdersPrecise` in `trader/binance_futures.go`.

**Auto-decision / validation drift:**
- Issue: TODO comments warn that `GenerateAutoDecisions` output must stay compatible with `validateDecisions`; no automated guard.
- Files: `decision/engine.go` (lines ~39, ~99)
- Impact: Strategy auto-rules can silently fail validation or bypass intended constraints after prompt/strategy changes.
- Fix approach: Add integration tests per strategy (`decision/strategies.go`) covering auto-decision paths.

**Aster exchange API endpoint risk:**
- Issue: Aster `SetStopLoss`/`SetTakeProfit` POST to `/fapi/v3/order`; Binance migrated conditional orders to Algo Order API (see comments in `trader/binance_futures.go`).
- Files: `trader/aster_trader.go`
- Impact: Aster integration may break or behave differently from Binance if API parity diverges.
- Fix approach: Verify against current Aster docs; align with algo-order endpoints if required.

**Manual test file in repo root:**
- Issue: `test_symbol_validation.go` is a build-tag-gated manual test (`//go:build manual`) living at repo root, not in `_test.go` harness.
- Files: `test_symbol_validation.go`
- Impact: Validation regressions not caught by `go test ./...`.
- Fix approach: Move to `decision/validation_test.go` with standard test cases.

## Known Bugs

**Race on `isPaused` / `isRunning` without synchronization:**
- Symptoms: Pause/resume from API (`api/server.go` `handleSystemPause` → `manager/trader_manager.go` `SetAllPaused`) may race with `Run()` loop reads in `trader/auto_trader.go`.
- Files: `trader/auto_trader.go` (`isPaused`, `isRunning` fields; no mutex unlike `peakMu`)
- Trigger: Concurrent API pause + ticker-fired `runCycle`
- Workaround: Operational — avoid rapid pause/resume toggling during active cycles.

**Log detail path: `trader_id` not sanitized:**
- Symptoms: `handleLogDetail` validates `filename` against `..` and slashes but not `trader_id`; `filepath.Join(logsDir, traderID, filename)` could escape `decision_logs/` with a malicious `trader_id`.
- Files: `api/server.go` (`handleLogDetail`, lines ~972–996)
- Trigger: `GET /api/logs/detail?trader_id=../../etc&filename=passwd`
- Workaround: Only expose API on trusted network (see Security section).

**LLM retry effectively disabled:**
- Symptoms: `maxRetries := 1` in MCP client means no actual retry despite retry scaffolding.
- Files: `mcp/client.go` (`CallWithMessagesDetailed`, line ~170)
- Trigger: Transient network blips or rate limits on DeepSeek/Qwen
- Workaround: Manual cycle retry on next scan interval.

**Runtime config persistence overwrites full `config.json`:**
- Symptoms: Hot-update persists via read-modify-write of entire JSON file with mode `0644`.
- Files: `manager/trader_manager.go` (`persistRuntimeConfigChanges`, `SetAutoResume`)
- Trigger: Concurrent API config updates from multiple clients
- Workaround: Serialize config changes operationally.

## Security Considerations

**Unauthenticated control-plane API:**
- Risk: All endpoints—including `POST /api/close-all-positions`, `POST /api/system/pause`, `PUT /api/config`—are open with no auth middleware.
- Files: `api/server.go` (`setupRoutes`), `docker-compose.yml` (ports `8080:8080`, `3000:80`), production exposure on ali-server (`8.148.223.19:8080` per project rules)
- Current mitigation: CORS allows `*` (`corsMiddleware`); nginx proxies `/api/` without auth (`nginx.conf`)
- Recommendations: Add API token or mTLS; bind backend to localhost only; restrict firewall; never expose 8080 publicly without auth.

**Secrets in plaintext config:**
- Risk: `config.json` holds exchange API keys, private keys, and AI keys; written with `0644` permissions on hot-update.
- Files: `config/config.go`, `manager/trader_manager.go`, `docker-compose.yml` (env var injection)
- Current mitigation: `config.json` in `.gitignore`; env var override supported
- Recommendations: Use `0600` file permissions; prefer env-only secrets in production; rotate keys if config was ever world-readable.

**Decision logs may contain sensitive prompt/response data:**
- Risk: Full AI prompts, chain-of-thought, and account snapshots written to `decision_logs/{trader_id}/` and served via unauthenticated `/api/logs/*`.
- Files: `logger/decision_logger.go`, `api/server.go` (logs routes)
- Recommendations: Redact API keys from logs; restrict log API; add retention policy.

**External data API key in URL query string:**
- Risk: NOFX Data API appends `auth=` key to URL (`market/nofxdata.go`).
- Files: `market/nofxdata.go`
- Recommendations: Use header-based auth if API supports it; ensure keys not logged.

## Performance Bottlenecks

**Sequential market data fetch per cycle:**
- Problem: Each trader cycle fetches market data for all candidate symbols; multiple traders duplicate Binance API calls.
- Files: `decision/engine.go` (`fetchMarketDataForContext`), `market/data.go`, `pool/coin_pool.go`
- Cause: No shared cross-trader market-data cache; proxy adds latency (`market/data.go` V2Ray/SOCKS5 chain).
- Improvement path: Shared TTL cache keyed by symbol+interval; batch kline requests where Binance allows.

**Heavy prompt construction every cycle:**
- Problem: `decision/prompts.go` builds large system/user prompts including performance history, BTC environment, and multi-timeframe data.
- Files: `decision/prompts.go`, `decision/engine.go`
- Cause: Full context serialized to LLM each scan interval (default ~3 min per trader).
- Improvement path: Incremental/delta prompts; cache static system prompt sections.

**Trade memory OpenGuard adds extra LLM calls:**
- Problem: Borderline opens trigger a second LLM call via `memory/trade_memory.go` `GateOpenDecision`.
- Files: `memory/trade_memory.go`, `memory/agents.go`, `trader/auto_trader.go` (open path ~756)
- Cause: `needsReview` true when sharpe < 0 or losing streak ≥ 2
- Improvement path: Make OpenGuard opt-in per trader; reuse main decision client with cost tracking.

**Binance API calls use `context.Background()` without timeout per call:**
- Problem: All Binance SDK calls in `trader/binance_futures.go` use `context.Background()`; hung connections block execution.
- Files: `trader/binance_futures.go` (20+ occurrences)
- Improvement path: Per-call contexts with deadlines derived from scan interval.

**No Binance rate-limit handling in main trading path:**
- Problem: Only `tools/coin_pool_server/main.go` handles HTTP 429/418 with backoff; trading client does not.
- Files: `trader/binance_futures.go`, `market/data.go`
- Cause: Multiple traders + market data + order history can hit weight limits.
- Improvement path: Centralized rate limiter; exponential backoff on 429.

## Fragile Areas

**LLM JSON parsing pipeline:**
- Files: `decision/parser.go` (`extractDecisions`, `fixMissingQuotes`, bracket matching)
- Why fragile: Relies on heuristic extraction of first `[...]` array from free-form model output; post-hoc JSON repair for missing quotes.
- Safe modification: Add golden-file tests with real logged responses from `decision_logs/`; fail closed on parse errors (already partially done in `parseFullDecisionResponse`).
- Test coverage: Only `decision/validation_adaptive_stop_loss_test.go` exists in entire Go codebase.

**Complex validation rules with adaptive stop-loss:**
- Files: `decision/validation.go` (~781 lines)
- Why fragile: Interdependent rules (min stop distance, margin validation, BTC direction constraints, risk-reward, leverage clip) with symbol-specific branches.
- Safe modification: Change one rule at a time with targeted tests; use recorded contexts from `decision/recorder.go`.
- Test coverage: Single adaptive stop-loss test; no coverage for margin, RR, or BTC constraint paths.

**Proxy-dependent Binance connectivity:**
- Files: `market/data.go` (V2Ray SOCKS at `/usr/local/etc/v2ray/config.json`), `trader/binance_futures.go` (proxy setup)
- Why fragile: Production (ali-server) requires V2Ray; init-order race noted in comments (package init before `ALL_PROXY` set).
- Safe modification: Ensure proxy configured before first market fetch; health endpoint should include Binance reachability.

**Multi-exchange abstraction leakage:**
- Files: `trader/interface.go`, `trader/binance_futures.go`, `trader/hyperliquid_trader.go`, `trader/aster_trader.go`, `trader/paper_trader.go`
- Why fragile: Exchange-specific behaviors (algo orders, precision rounding, hedge mode) handled differently; paper wrapper may mask live bugs.
- Safe modification: Contract tests per `Trader` implementation for SetStopLoss, SetTakeProfit, Open/Close.

**Frontend typed loosely:**
- Files: `web/src/lib/api.ts` (many `any` return types), `web/src/components/ComparisonChart.tsx`, `web/src/pages/LogViewer.tsx`
- Why fragile: API schema changes won't fail at compile time.
- Safe modification: Align `web/src/types.ts` with API responses; remove `any`.

## Scaling Limits

**Concurrent traders on single process:**
- Current capacity: All traders in one Go process (`main.go` → `manager/trader_manager.go` `StartAll`); each runs independent goroutine with own scan ticker.
- Limit: Binance API weight, LLM rate limits, and global state races (`decision` globals, `mcp` default client) degrade beyond ~3–5 active traders.
- Scaling path: Process-per-trader or fix globals; shared rate limiter; horizontal API workers.

**Decision log disk growth:**
- Current capacity: Unbounded JSON files under `decision_logs/{trader_id}/` (`logger/decision_logger.go`); archive logic exists but retention policy unclear.
- Limit: Disk fill on long-running production (ali-server).
- Scaling path: Log rotation, S3/archive, compress old cycles.

**Single-writer config.json:**
- Current capacity: One file mutated by runtime hot-update (`manager/trader_manager.go`).
- Limit: No merge conflict handling for concurrent PUT `/api/config`.
- Scaling path: Per-trader config files or versioned updates.

## Dependencies at Risk

**`github.com/adshao/go-binance/v2`:**
- Risk: Binance API evolves (algo orders, `-4120` errors already required migration comments in `trader/binance_futures.go`).
- Impact: Order placement, SL/TP, position queries break on API changes.
- Migration plan: Pin version; monitor Binance changelog; abstract order layer.

**V2Ray / SOCKS proxy stack:**
- Risk: Hard dependency for China-region Binance access; config path hardcoded (`/usr/local/etc/v2ray/config.json` in `market/data.go`).
- Impact: All market data and trading fail if proxy down.
- Migration plan: Health checks; fallback proxy config via env; alert on proxy failure.

**DeepSeek / Qwen API availability and pricing:**
- Risk: Peak-hour pricing, model deprecations, reasoning-model latency (`deepseek-reasoner` default in `mcp/client.go`).
- Impact: Missed cycles, higher cost; `PeakHourPause` mitigates cost only.
- Migration plan: Model fallback chain; increase MCP retries; circuit breaker.

**NOFX Data external service (`nofxos.ai`):**
- Risk: Optional but integrated market enrichment (`market/nofxdata.go`); 3s timeout, nil on failure.
- Impact: Degraded prompts when unavailable (best-effort today).
- Migration plan: Cache last-good signals; degrade gracefully (already partial).

## Missing Critical Features

**Production-grade auth and audit log for control actions:**
- Problem: Close-all, pause, config PUT have no authentication or audit trail beyond stdout logs.
- Blocks: Safe public deployment of monitoring UI.

**Automated integration / e2e test suite:**
- Problem: Only one Go test file; zero frontend tests (`web/` has no `*.test.*` files).
- Blocks: Confident refactoring of validation, parser, and API layers.

**Graceful trader crash recovery:**
- Problem: `Run()` logs cycle errors and continues (`trader/auto_trader.go`); no alert or auto-pause after repeated failures.
- Blocks: Silent degradation during exchange outages.

**Unified order state reconciliation:**
- Problem: Local position state vs exchange can drift; no periodic reconcile job beyond cycle-time fetches.
- Blocks: Reliable SL/TP management after partial fills or manual exchange edits.

## Test Coverage Gaps

**Decision parser and validation:**
- What's not tested: JSON extraction heuristics, `fixMissingQuotes`, full `validateDecisions` matrix, auto-strategy paths.
- Files: `decision/parser.go`, `decision/validation.go`, `decision/engine.go`
- Risk: AI output format changes cause silent empty decisions or rejected cycles.
- Priority: High

**Multi-trader concurrency / global state:**
- What's not tested: Parallel `runCycle` with distinct `ErrorStats`; `SetMinRiskReward` isolation.
- Files: `decision/constants.go`, `manager/trader_manager.go`
- Risk: Wrong error attribution in competition mode; incorrect RR threshold.
- Priority: High

**Exchange trader implementations:**
- What's not tested: Binance algo order cancel/create, Hyperliquid precision, Aster order API.
- Files: `trader/binance_futures.go`, `trader/hyperliquid_trader.go`, `trader/aster_trader.go`
- Risk: Live-only failures on stop-loss updates and close paths.
- Priority: High

**API security and path traversal:**
- What's not tested: `handleLogDetail` trader_id sanitization; unauthenticated control endpoints.
- Files: `api/server.go`
- Risk: Path traversal or unauthorized trading actions on exposed deployments.
- Priority: High (if API is public)

**Memory / OpenGuard pipeline:**
- What's not tested: `GateOpenDecision` rule gates, vector similarity, LLM guard parsing.
- Files: `memory/trade_memory.go`, `memory/agents.go`, `memory/vector.go`
- Risk: Incorrect size scaling or rejected valid trades.
- Priority: Medium

**Frontend API contract:**
- What's not tested: React components against live API shapes.
- Files: `web/src/lib/api.ts`, `web/src/App.tsx`
- Risk: UI breaks on backend field renames.
- Priority: Medium

---

*Concerns audit: 2026-07-10*
