# Technology Stack

**Analysis Date:** 2026-07-10

## Languages

**Primary:**
- Go 1.25.0 — Backend trading engine, HTTP API, exchange adapters, AI client, market data, decision engine (`go.mod`, `main.go`, `trader/`, `decision/`, `api/`, `market/`, `mcp/`)

**Secondary:**
- TypeScript 5.8 — React monitoring dashboard (`web/src/`)
- Python 3 — Offline log/strategy analysis scripts (`analysis/*.py`, `scripts/patch_v2ray_binance_testnet.py`)
- Bash — Docker lifecycle and deployment (`start.sh`)
- JSON — Runtime configuration and persisted logs (`config.json`, `decision_logs/`, `trade_memory/`)

## Runtime

**Environment:**
- Go binary `./nofx` (single process, multi-trader goroutines)
- Node.js 18 (frontend build only, via `web/Dockerfile` and root `Dockerfile` frontend stage)
- nginx:alpine (production frontend static hosting + API reverse proxy)
- Docker Compose 3.8 (`docker-compose.yml`)

**Package Manager:**
- Go modules — `go mod` / `go.sum`
- npm — `web/package-lock.json` (implied by `npm ci` in Dockerfile)
- pip — `analysis/requirements.txt` (analysis tooling only; not part of Docker runtime)

## Frameworks

**Core (Backend):**
- Gin v1.11.0 — REST API server (`api/server.go`)
- Standard library `net/http` — Exchange REST calls, AI API calls, market data fetching

**Core (Frontend):**
- React 18.3 — SPA dashboard (`web/src/App.tsx`)
- Vite 6.0 — Dev server and production bundler (`web/vite.config.ts`)
- Tailwind CSS 3.4 — Styling (`web/tailwind.config.js`)
- SWR 2.2 — Data fetching and polling (`web/src/lib/api.ts`, components)
- Zustand 5.0 — Client state (where used in `web/src/`)
- Recharts 2.15 + lightweight-charts 5.1 — Equity/performance charts

**Testing:**
- Go `testing` package — Unit tests in `decision/` (e.g. `decision/validation_adaptive_stop_loss_test.go`)
- No frontend test runner configured in `web/package.json`

**Build/Dev:**
- Vite + `@vitejs/plugin-react` — Frontend HMR and build
- Multi-stage Docker build — Go backend + Node frontend + Alpine runtime (`Dockerfile`)
- `start.sh` — Docker Compose wrapper (start/stop/logs/status)

## Key Dependencies

**Critical (Backend — direct `require` in `go.mod`):**
- `github.com/adshao/go-binance/v2` v2.8.9 — Binance USDT-M futures trading and market data (`trader/binance_futures.go`, `market/data.go`)
- `github.com/sonirico/go-hyperliquid` v0.17.0 — Hyperliquid DEX perpetuals (`trader/hyperliquid_trader.go`)
- `github.com/ethereum/go-ethereum` v1.16.5 — ECDSA signing for Hyperliquid and Aster wallet auth (`trader/hyperliquid_trader.go`, `trader/aster_trader.go`)
- `github.com/gin-gonic/gin` v1.11.0 — HTTP API framework (`api/server.go`)
- `golang.org/x/net` v0.47.0 — SOCKS5 proxy dialer for Binance access behind V2Ray (`market/data.go`, `trader/binance_futures.go`)

**Notable indirect:**
- `github.com/gorilla/websocket` — Pulled by Hyperliquid SDK (WebSocket market data)
- `github.com/joho/godotenv` — Present in module graph; config loading uses `os.Getenv` directly in `config/config.go`
- `github.com/rs/zerolog` — Structured logging (Hyperliquid SDK dependency chain)

**Critical (Frontend — `web/package.json`):**
- `react`, `react-dom` ^18.3.1
- `swr` ^2.2.5 — API polling
- `recharts`, `lightweight-charts` — Charting
- `date-fns`, `clsx` — Utilities

**Infrastructure / Ops (not Go modules):**
- V2Ray — HTTP/SOCKS proxy for Binance API access in restricted regions (`v2ray/`, read at runtime from `/usr/local/etc/v2ray/config.json`)
- nginx — Reverse proxy `/api/` → backend `:8080` (`nginx.conf`)
- TA-Lib 0.4.0 — Compiled in `Dockerfile` with `CGO_ENABLED=1`, but **no `#cgo` imports in Go source**; technical indicators (EMA, MACD, RSI, ATR) are implemented in pure Go in `market/data.go`

## Configuration

**Environment:**
- Primary config file: `config.json` (mounted read-only in Docker at `/app/config.json`)
- Example/template: `config.example.paper-trading.json`; README references `config.json.example`
- Env var overlay in `config/config.go` via `loadFromEnvironment()` — secrets can live in env instead of JSON
- Docker Compose injects: `BINANCE_API_KEY`, `BINANCE_SECRET_KEY`, `BINANCE_TESTNET_*`, `DEEPSEEK_OPENAI_KEY`, `QWEN_OPENAI_API_KEY`, `TZ=Asia/Shanghai`
- Optional: `NOFX_DATA_API_KEY` (NOFX Data API), `ALL_PROXY` / `HTTPS_PROXY` (proxy), `BINANCE_FAPI_BASE_URL` (coin pool tool)

**Key config fields (`config/config.go`):**
- `traders[]` — Per-trader AI model, exchange, keys, scan interval, paper trading, prompt strategy
- `binance_testnet` — Global Binance mainnet vs testnet switch
- `coin_pool_api_url`, `oi_top_api_url` — External coin selection APIs
- `use_default_coins`, `default_coins` — Static symbol list fallback
- `api_server_port` — Backend listen port (default 8080)
- Risk controls: `leverage`, `max_daily_loss`, `max_drawdown`, `stop_loss_distance`, `auto_take_profit`, etc.

**Build:**
- `Dockerfile` — Multi-stage: golang:1.25-alpine → node:18-alpine → alpine:latest
- `docker-compose.yml` — Services `nofx` (backend) + `nofx-frontend` (nginx), shared volume `frontend-dist`
- `web/vite.config.ts` — Dev proxy `/api` → `http://localhost:8082` (local dev port may differ from production 8080)

**Local dev (from README):**
- Backend: `go run .` or `go build -o nofx .`
- Frontend: `cd web && npm run dev` (port 3000)
- TA-Lib system library optional for local builds; Docker image includes it

## Platform Requirements

**Development:**
- Go 1.25+
- Node.js 18+ and npm (frontend)
- Docker + Docker Compose (recommended deployment path per `start.sh`)
- Optional: Python 3 + `analysis/requirements.txt` for offline analysis
- Optional: V2Ray for Binance connectivity in geo-restricted environments

**Production:**
- Linux host (documented remote deploy on ali-server via Docker)
- Ports: **8080** (backend API), **3000** (nginx frontend)
- Persistent volumes: `decision_logs/`, `config.json`, optional `trade_memory/`, `coin_pool_cache/`
- Binance access often requires HTTP/SOCKS proxy (V2Ray on port 10809 per project docs)

---

*Stack analysis: 2026-07-10*
