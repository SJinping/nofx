# 模型提供商与参数配置

所有当前提供商（DeepSeek、Qwen、custom、OpenAI）共用一个 Chat Completions 适配器，使用 `/chat/completions`、`messages` 和统一的回答/用量解析。`ai_model` 选择提供商、默认地址和凭证；`model_name` 选择模型；`model_options` 描述模型支持的参数。交易决策、风控和订单执行不依赖提供商协议。

## 使用 gpt-6.2-sol

在现有 trader 条目中设置以下字段，其余交易配置继续沿用该条目的配置：

```json
{
  "ai_model": "openai",
  "model_name": "gpt-6.2-sol",
  "model_options": {
    "reasoning_mode": "standard",
    "reasoning_effort": "high",
    "verbosity": "medium",
    "service_tier": "default",
    "max_output_tokens": 25000,
    "store": true
  }
}
```

将密钥放在与所选配置文件同目录的 `.env`：

```dotenv
OPENAI_API_KEY=your-api-key
```

启动时加载该文件，已导出的环境变量优先，不会被 `.env` 覆盖。也可配置 `openai_api_key`，但建议保留密钥在环境中。本次改动不会修改实际 `config.json`、启动本地交易或部署服务器。

OpenAI 默认模型为 `gpt-6.2-sol`，默认参数就是上述参数。其余提供商保留原来的 `temperature=0.25`、`max_tokens=10000` 默认行为。

### 参数与实际请求

| 配置 | Chat Completions 请求 |
| --- | --- |
| `reasoning_mode: standard` | 采用标准执行模式；不发送不存在的 `reasoning_mode` 字段。`pro` 配置会报错，不能静默降级。 |
| `reasoning_effort: high` | `reasoning_effort: high` |
| `verbosity: medium` | `verbosity: medium` |
| `service_tier: default` | Standard 档；配置中也接受 `standard` 并转换为 `default`。 |
| `max_output_tokens: 25000` | OpenAI 使用 `max_completion_tokens`，预算同时包含内部推理和可见回答。 |
| `store: true` | 请求 OpenAI 存储该次 completion；与本地决策日志的保存独立。 |

普通文本是接口默认输出格式，继续支持原有“分析摘要 + JSON 决策数组”的解析。此实现使用 HTTP，不实现截图中的 Responses WebSocket 或 reasoning summary；这些不是本次接入的必要条件。每轮仅发送当轮 system/user prompt，不串接之前的市场快照或模型建议。

## 更换模型

`model_name` 是所有提供商共用的模型字段，优先于历史字段 `deepseek_model`、`qwen_model`、`custom_model_name`。原配置无需迁移，仍能运行。运行时修改模型也会持久化到正确字段。

同协议新模型不需要新增适配器；根据目标模型的 API 文档调整参数即可。例如，自定义 OpenAI 兼容的推理服务可以配置 `token_limit_parameter: max_completion_tokens`、`omit_temperature: true` 和 `reasoning_effort`，仍使用同一适配器。`custom_api_url` 的结尾 `#` 仍表示完整请求 URL。

可选设置包括 `temperature`、`omit_temperature`、`token_limit_parameter`、`reasoning_effort`、`verbosity`、`service_tier`、`store`。未指定的设置继承提供商默认值；字符串参数显式设为 `""` 表示不发送该参数。向不支持 reasoning/verbosity 的旧模型切换时，可用这一方式关闭相应参数。不同模型支持哪些参数仍以提供商文档为准。

当前所有服务都兼容 Chat Completions，因此只有一个适配器实现。只有以后出现无法表达的协议需求（例如 Responses 工具调用）时，才需要为该协议增加适配器，而不是为每个模型单独写分支。

## 用量、计费与失败记录

统一解析 DeepSeek 顶层缓存计数和 OpenAI `prompt_tokens_details` / `completion_tokens_details`。内部推理 token 已包含在 completion tokens，不重复计费。缓存写入、缓存读取和普通输入分开计费；缓存写入没有拆分保留时间时，按客户端未请求延长保留时间的默认 30 分钟估算。

内置 `gpt-6.1-sol`、`gpt-6.2-sol` Standard 估算价格：普通输入 $2、缓存读取 $0.10、30分钟缓存写入 $2.50、12小时写入 $5、输出 $10，均为每百万 token。支持输入超过 272K 时的长上下文倍率，以及 Fast/Flex 倍率；费用展示继续使用现有 USDT 字段，按 USD≈USDT 估算，实际费用以 OpenAI 账单为准。

空回答、拒答和 `finish_reason=length` 等非正常结束均视为失败，即使返回内容看上去是合法 JSON，也不执行其中的动作。不自动重试，避免生成重复决策或重复付费。失败仍保留提交的 user prompt、已返回的原始可见回答、响应 ID、结束原因、模型和用量；没有返回用量的网络错误不能凭空估算费用。

官方参考（核对日期：2026-11-18）：
- https://developers.openai.com/api/docs/models/gpt-6.2-sol
- https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create
- https://developers.openai.com/api/docs/guides/reasoning
- https://developers.openai.com/api/docs/pricing
