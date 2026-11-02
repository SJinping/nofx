# NOFX 公开只读 API

公开 API 用于其他网站展示收益百分比，与现有管理 API 使用独立端口、独立路由和独立响应结构。默认关闭；不会自动发布任何 Trader。本次不包含 Profile 网站接入或线上部署。

## 启用配置

在现有 `config.json` 中增加以下字段。`trader_id` 必须替换为已有 Trader 的 ID；它仅用于内部映射，不会出现在公开响应中。

```json
{
  "api_server_port": 8080,
  "api_listen_host": "127.0.0.1",
  "public_api": {
    "enabled": true,
    "port": 8082,
    "strategies": [
      {
        "slug": "strategy-a",
        "name": "Strategy A",
        "trader_id": "YOUR_EXISTING_TRADER_ID"
      }
    ]
  }
}
```

这是配置片段，应合并到已有配置，不能替代包含 Trader 等字段的完整配置。发布名单和监听配置在进程启动时读取，变更后需要重启，不支持管理接口热更新。

- `public_api.enabled`：默认 `false`。
- `port`：默认 `8082`，不得与管理端口相同。
- `strategies`：启用时必须明确列出 1～20 个策略；每个 Trader 最多发布一次。
- `slug`：公开路径中的标识，最长 64 字节，仅允许小写字母、数字及中间的单个连字符；必须唯一。
- `name`：自行设定的公开展示名称，不自动沿用内部 Trader 或模型名称。
- 公开 API 固定监听 `127.0.0.1`，由同机反向代理向网站提供服务。
- 管理 API 默认也改为监听 `127.0.0.1`；可通过 `api_listen_host` 明确配置，或在该字段为空时通过 `NOFX_API_HOST` 设置。原生部署应保持回环地址，因为现有管理接口没有身份鉴权。

已有配置不包含 `public_api` 时，公开服务不会启动。不要为了公开展示而将管理端口改为 `0.0.0.0`。

## 接口

以下接口只接受 `GET`，其他方法返回 `405`。公开端口不注册管理路由，不能通过它读取决策、日志、配置、订单或执行交易操作。

| 接口 | 内容 |
| --- | --- |
| `/health` | HTTP 进程存活状态，不代表交易所或快照可用 |
| `/api/public/v1/strategies` | 明确发布的策略及各策略汇总 |
| `/api/public/v1/strategies/:slug/summary` | 单策略累计收益率、采样窗口最大回撤及数据状态 |
| `/api/public/v1/strategies/:slug/returns?limit=500` | 收益率曲线，默认最多 500 点，可设置 2～1000 点 |
| `/api/public/v1/strategies/:slug/positions` | 币种、多空方向和持仓价格涨跌幅 |

除曲线接口的 `limit` 外，不支持其他查询参数。重复参数或非法参数返回 `400`，包括 `trader_id`、`include_actions`、文件名和内部周期范围。未知策略和路径返回 `404`。

汇总响应示例：

```json
{
  "slug": "strategy-a",
  "name": "Strategy A",
  "state": "ready",
  "stale": false,
  "as_of": "2026-11-02T12:00:00Z",
  "return_pct": 12.34,
  "sampled_max_drawdown_pct": 4.56,
  "history_from": "2026-10-01T00:00:00Z",
  "history_to": "2026-11-02T11:57:00Z",
  "history_limit_reached": false
}
```

曲线响应包含同样的汇总字段，并增加：

```json
{
  "points": [
    { "timestamp": "2026-10-01T00:00:00Z", "return_pct": 0 },
    { "timestamp": "2026-11-02T11:57:00Z", "return_pct": 12.34 }
  ]
}
```

持仓接口仅包含 `slug`、`state`、`stale`、`as_of` 和 `positions`：

```json
{
  "slug": "strategy-a",
  "state": "ready",
  "stale": false,
  "as_of": "2026-11-02T12:00:00Z",
  "positions": [
    { "symbol": "BTCUSDT", "side": "long", "price_change_pct": 2.34 }
  ]
}
```

响应不包含账户金额、初始本金、持仓数量、开仓价、保证金金额、杠杆、订单号、内部 Trader ID、文件名、决策周期、Prompt、推理过程、运行配置或上游错误内容。公开币种、方向及价格涨跌幅会透露持仓行为；不希望披露时，网站可以只使用汇总和曲线接口。

## 数据口径与状态

- 所有百分比的数值 `1` 代表 `1%`，最多保留两位小数；时间统一为带时区的 UTC RFC 3339。
- `return_pct = (账户净值 / 配置的初始资金 - 1) × 100`，与管理端账户口径一致。它包含账户层面的浮动盈亏，不是单笔交易收益率，也不是年化收益率。
- 当前没有入出金调整账本。因此有充值、提现或调整初始资金时，不能将该值解释为扣除资金流影响的策略收益。修改初始资金也会改变历史曲线的归一化基准。
- `price_change_pct` 是按多空方向调整后的价格涨跌幅，不含杠杆、手续费和资金费，不应标成保证金 ROI。
- `sampled_max_drawdown_pct` 在保留的历史净值采样窗口内计算：`(此前采样峰值 - 当前采样净值) / 此前采样峰值 × 100`，以正数表示。它不包含独立的实时账户点，也不保证捕获盘中最大回撤；窗口内没有历史数据时返回 `null`。
- 历史数据复用现有轻量曲线索引，按时间排序，最多保留最近 20,000 个有效决策周期点。`history_limit_reached=true` 表示已触及上限，不能将统计称为全历史最大回撤。
- 曲线响应按整个保留窗口均匀抽样，并保留首尾点；回撤在抽样前计算。展示曲线可能省略窗口内极值，不能直接用返回的精简曲线重新计算最大回撤。
- 当前汇总和持仓约每 60 秒刷新一次，复用交易引擎现有的约 30 秒缓存。`as_of` 是最近一次成功采集并生成公开快照的时间，不是交易所原始事件时间。
- `history_to` 是曲线最后一个历史采样点的时间。决策暂停时曲线可能停止更新，即使当前账户快照仍然正常刷新。前端应分别显示两个时间。
- HTTP 请求只读取已经脱敏的内存缓存，不触发交易所请求、历史文件扫描或决策调用。访客增长不会按请求数增加交易所调用。
- 启动后尚未生成快照、曲线索引尚未就绪或首次采集失败时，单策略接口返回 `503`、`Retry-After: 60`，收益和时间为 `null`；列表仍返回 `200`，对应策略 `state=unavailable`。
- 更新失败时保留上一份有效快照，并立即标记 `state=stale`、`stale=true`。超过 3 分钟未成功更新也会标记过期。成功恢复后自动清除过期标记。
- 过期数据仍返回 `200` 并附带原有 `as_of`，网站应明确提示“数据已过期”，不能继续显示实时状态。空持仓返回 `[]`；不可用数据不会伪造为零收益。
- 响应使用 `Cache-Control: no-store`，防止代理缓存掩盖过期状态。可在前端每 60 秒更新一次。

第一版不公开逐笔已平仓交易、胜率、日收益和年化收益；这些指标需进一步明确费用、资金流、交易配对及统计周期口径。

## 后续网站接入示例（本次未部署）

在网站的 Nginx `server` 中增加独立代理路径，例如：

```nginx
location ^~ /api/trading/ {
    limit_except GET { deny all; }
    proxy_pass http://127.0.0.1:8082/api/public/v1/;
    proxy_connect_timeout 2s;
    proxy_read_timeout 10s;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
}
```

页面可使用 `fetch('/api/trading/strategies')`，以及 `/api/trading/strategies/strategy-a/returns` 等同域路径，不需要跨域配置或浏览器中的密钥。不要将网站 `/api/` 整体转发到管理端口 `8080`。

公开前应配置 HTTPS 和请求限流，确认安全组、防火墙和其他反向代理没有暴露管理端口。只读端口与管理端口之间不能设置通配转发。管理端继续使用 SSH 隧道访问。

仓库 Docker Compose 已将管理 API 的宿主机端口及管理前端端口绑定到 `127.0.0.1`。容器内通过 `NOFX_API_HOST=0.0.0.0` 保持与同网络管理前端通信；显式配置的 `api_listen_host` 优先于环境变量。公开服务当前为原生同机代理场景提供回环监听，未增加 Docker 公开端口；跨容器接入需要另行设计网络边界。

## 验证

```bash
go test -race ./api ./config
go build -o /tmp/nofx-review .
```

目前仓库 `api/log_files.go` 使用 Linux 专用的 `syscall.Openat`，API 测试和完整构建需要 Linux；本次没有修改该既有实现。测试使用假数据源和临时目录，不需要交易所密钥，也不会连接实盘账户。
