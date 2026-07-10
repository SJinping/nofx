# External Integrations

**Analysis Date:** 2026-07-10

## APIs & External Services

**Cryptocurrency Exchanges:**

| Exchange | Purpose | SDK / Client | Auth |
|----------|---------|--------------|------|
| Binance USDT-M Futures | Live trading, positions, orders, klines, OI, funding | `github.com/adshao/go-binance/v2/futures` in `trader/binance_futures.go` | `binance_api_key` + `binance_secret_key` in `config.json`, or `BINANCE_API_KEY` / `BINANCE_SECRET_KEY` (mainnet), `BINANCE_TESTNET_*` (testnet) |
| Hyperliquid | Decentralized perpetual futures | `github.com/sonirico/go-hyperliquid` in `trader/hyperliquid_trader.go` | Ethereum private key: `hyperliquid_private_key` or `HYPERLIQUID_PRIVATE_KEY`; mainnet/testnet via `hyperliquid_testnet` |
| Aster DEX | Binance-compatible decentralized futures | Custom REST client in `trader/aster_trader.go` | Web3 API wallet: `aster_user`, `aster_signer`, `aster_private_key` (or `ASTER_*` env vars); base URL `https://fapi.asterdex.com` |

**Binance endpoints (configurable):**
- Mainnet FAPI: `https://fapi.binance.com` (`market/data.go`, `main.go`)
- Testnet FAPI: `https://testnet.binancefuture.com` (when `binance_testnet: true`)

**Hyperliquid endpoints:**
- Mainnet / testnet via `hyperliquid.MainnetAPIURL` / `hyperliquid.TestnetAPIURL` (`trader/hyperliquid_trader.go`)

**AI / LLM Providers:**

| Provider | Purpose | Client | Auth |
|----------|---------|--------|------|
| DeepSeek | Trading decision LLM (chain-of-thought) | OpenAI-compatible REST in `mcp/client.go` | `deepseek_key` or `DEEPSEEK_OPENAI_KEY`; default model `deepseek-reasoner`; endpoint `https://api.deepseek.com/v1/chat/completions` |
| Qwen (Alibaba DashScope) | Trading decision LLM | OpenAI-compatible REST in `mcp/client.go` | `qwen_key` or `QWEN_OPENAI_API_KEY`; default model `qwen3.5-plus`; endpoint `https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions` |
| Custom OpenAI-compatible | Any third-party LLM | `NewCustomClient()` in `mcp/client.go` | `custom_api_url`, `custom_api_key`, `custom_model_name` per trader |

**Market intelligence:**

| Service | Purpose | Implementation | Auth |
|---------|---------|----------------|------|
| NOFX Data API (`nofxos.ai`) | AI300 capital flow signals + order-book heatmap | `market/nofxdata.go` | `NOFX_DATA_API_KEY` env var; docs at `https://nofxos.ai/api-docs` |
| Alternative.me Fear & Greed | Market sentiment in dashboard | HTTP GET in `api/server.go` (`handleMarketOverview`) | None — `https://api.alternative.me/fng/?limit=1` |
| Coin Pool API (AI500) | Dynamic candidate coin scoring | `pool/coin_pool.go` → configurable `coin_pool_api_url` | Depends on deployed service; no built-in auth |
| OI Top API | Open-interest growth leaders | `pool/coin_pool.go` → configurable `oi_top_api_url` | Depends on deployed service; optional |

**Self-hosted coin pool service:**
- `tools/coin_pool_server/main.go` — Standalone Go HTTP service exposing:
  - `GET /api/coins` — Coin pool derived from Binance futures data
  - `GET /api/oi/top` — OI top rankings
  - `GET /health`
- Intended to be referenced by `coin_pool_api_url` and `oi_top_api_url` in `config.json`
- Uses same Binance FAPI + V2Ray/proxy patterns as main app

## Data Storage

**Databases:**
- None — No SQL/NoSQL database detected in codebase

**File Storage (local filesystem):**

| Path | Format | Purpose | Written by |
|------|--------|---------|------------|
| `decision_logs/{trader_id}/` | JSON files | AI decision cycle logs (prompt, CoT, actions) | `logger/decision_logger.go` |
| `trade_memory/{trader_id}/` | JSONL + JSON | Trade episodes, analyses, self-learning memory | `memory/trade_memory.go`, `memory/storage.go` |
| `coin_pool_cache/` | JSON | Cached coin pool and OI top API responses | `pool/coin_pool.go` |
| `config.json` | JSON | Runtime config; hot-updated fields persisted by trader manager | `config/config.go`, `trader/runtime_config.go` |
| `stats/` (error stats) | JSON | Error counters per trader | `stats/error_stats.go` |
| Decision recordings | JSON | Backtest context snapshots when `enable_recording: true` | `decision/recorder.go` |

**Caching:**
- In-memory: trader state, market data per cycle, coin pool/OI caches with file fallback
- SWR client-side cache in frontend (`web/src/lib/api.ts`)
- No Redis or external cache service

## Authentication & Identity

**Auth Provider:**
- Custom — No end-user login for the web dashboard
- Exchange auth: API keys (Binance HMAC), Ethereum private keys (Hyperliquid, Aster EIP-712-style signing)
- LLM auth: Bearer tokens per provider

**API security model:**
- Backend Gin API (`api/server.go`) uses open CORS (`Access-Control-Allow-Origin: *`)
- No JWT/session middleware on `/api/*` routes
- Control endpoints (`POST /api/system/pause`, `POST /api/close-positions`, `PUT /api/config`) are unauthenticated — intended for trusted network / private deployment only

## Monitoring & Observability

**Error Tracking:**
- None (no Sentry, Datadog, etc.)

**Logs:**
- stdout/stderr via Go `log` package
- Structured decision logs on disk (`decision_logs/`)
- Docker: `./start.sh logs` → `docker-compose logs`
- Web log viewer reads backend API: `GET /api/logs/traders`, `/api/logs/list`, `/api/logs/detail` (`api/server.go`, `web/src/pages/LogViewer.tsx`)

**Health checks:**
- `GET /health` on backend (`api/server.go`)
- Docker healthcheck: curl/wget to `:8080/health` (`docker-compose.yml`, `Dockerfile`)
- nginx stub `/health` returns 200 (does not proxy to backend)

**Metrics:**
- `GET /api/statistics`, `/api/error-stats`, `/api/performance` — Application-level stats, not Prometheus
- LLM cost tracking: `mcp/cost.go`, `trader/llm_cost_tracker.go` (token pricing tables, no external billing API)

## CI/CD & Deployment

**Hosting:**
- Docker Compose on Linux VPS (e.g. ali-server per project rules)
- Containers: `nofx-trading` (backend), `nofx-frontend` (nginx)

**CI Pipeline:**
- Not detected — No `.github/workflows/` in repository

**Deploy flow:**
1. `config.json` + env secrets on host
2. `./start.sh start` or `./start.sh start --build`
3. Backend `:8080`, frontend `:3000` (nginx → `/api/` proxy to backend)

## Environment Configuration

**Required env vars (production via `docker-compose.yml`):**

| Variable | Purpose |
|----------|---------|
| `BINANCE_API_KEY` / `BINANCE_SECRET_KEY` | Binance mainnet credentials (if not in config.json) |
| `BINANCE_TESTNET_API_KEY` / `BINANCE_TESTNET_SECRET_KEY` | Binance testnet credentials |
| `DEEPSEEK_OPENAI_KEY` | DeepSeek API key |
| `QWEN_OPENAI_API_KEY` | Qwen/DashScope API key |
| `TZ` | Timezone (default `Asia/Shanghai` in compose) |

**Optional env vars:**

| Variable | Purpose |
|----------|---------|
| `NOFX_DATA_API_KEY` | NOFX Data API (AI300 + heatmap) |
| `HYPERLIQUID_PRIVATE_KEY` | Hyperliquid wallet |
| `ASTER_USER`, `ASTER_SIGNER`, `ASTER_PRIVATE_KEY` | Aster API wallet |
| `ALL_PROXY`, `HTTPS_PROXY`, `HTTP_PROXY` | Proxy for Binance (SOCKS5/HTTP) |
| `BINANCE_FAPI_BASE_URL` | Override FAPI base in `tools/coin_pool_server` |

**Secrets location:**
- `config.json` on host (gitignored; mounted into container)
- Docker Compose `${VAR}` substitution from host `.env` or shell environment
- `.env` file not committed (not present in repo snapshot)

## Webhooks & Callbacks

**Incoming:**
- None — System polls exchanges and LLMs on a timer (`scan_interval_minutes` per trader); no inbound webhook handlers

**Outgoing:**
- All integrations are pull-based HTTP/REST:
  - Binance FAPI REST (+ WebSocket via Hyperliquid SDK only)
  - Hyperliquid REST/WebSocket
  - Aster REST
  - DeepSeek / Qwen chat completions
  - NOFX Data, Fear & Greed, coin pool / OI top HTTP APIs

## Network / Proxy Integration

**V2Ray (operational dependency, not a Go module):**
- Config: `v2ray/config.json` on deploy host; runtime read from `/usr/local/etc/v2ray/config.json`
- Auto-detected HTTP (port 10809) or SOCKS inbound in `trader/binance_futures.go` and `market/data.go`
- CLI utilities: `v2ray_cli/` (Python node testing/switching per project docs)
- Required for Binance API access from geo-restricted servers (e.g. ali-server)

## Frontend ↔ Backend Integration

**Pattern:**
- Browser → nginx `:3000` → `/api/*` proxied to `nofx-trading:8080`
- Dev: Vite proxy `/api` → local backend (`web/vite.config.ts`)
- Client: native `fetch` + SWR in `web/src/lib/api.ts` (no axios)

**Key API surface (all under `/api/` unless noted):**
- Read: `/traders`, `/status`, `/account`, `/positions`, `/decisions/latest`, `/statistics`, `/equity-history`, `/performance`, `/competition`, `/market-overview`, `/klines`, `/logs/*`
- Write: `/system/pause`, `/system/auto-resume`, `/close-positions`, `/close-all-positions`, `/config` (PUT), `/peak-hour/*`

---

*Integration audit: 2026-07-10*
