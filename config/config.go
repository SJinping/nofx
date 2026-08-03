package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// TraderConfig 单个trader的配置
type TraderConfig struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	AIModel string `json:"ai_model"` // "qwen" or "deepseek"

	// 交易平台选择（二选一）
	Exchange string `json:"exchange"` // "binance" or "hyperliquid"

	// 币安配置
	BinanceAPIKey    string `json:"binance_api_key,omitempty"`
	BinanceSecretKey string `json:"binance_secret_key,omitempty"`

	// Hyperliquid配置
	HyperliquidPrivateKey string `json:"hyperliquid_private_key,omitempty"`
	HyperliquidTestnet    bool   `json:"hyperliquid_testnet,omitempty"`

	// Aster配置
	AsterUser       string `json:"aster_user,omitempty"`        // Aster主钱包地址
	AsterSigner     string `json:"aster_signer,omitempty"`      // Aster API钱包地址
	AsterPrivateKey string `json:"aster_private_key,omitempty"` // Aster API钱包私钥

	// AI配置
	QwenKey     string `json:"qwen_key,omitempty"`
	DeepSeekKey string `json:"deepseek_key,omitempty"`

	// 模型名称（可选，不填则使用默认值）
	// DeepSeek 默认: deepseek-reasoner, Qwen 默认: qwen3.5-plus
	DeepSeekModel string `json:"deepseek_model,omitempty"`
	QwenModel     string `json:"qwen_model,omitempty"`

	// 自定义AI API配置（支持任何OpenAI格式的API）
	CustomAPIURL    string `json:"custom_api_url,omitempty"`
	CustomAPIKey    string `json:"custom_api_key,omitempty"`
	CustomModelName string `json:"custom_model_name,omitempty"`

	InitialBalance      float64 `json:"initial_balance"`
	ScanIntervalMinutes int     `json:"scan_interval_minutes"`

	// 纸上交易模式（只做策略分析，不实际下单）
	PaperTradingMode bool `json:"paper_trading_mode,omitempty"`

	// 纸上交易配置（仅在PaperTradingMode=true时有效）
	// 设置为0可以禁用相应的费用，默认使用币安标准费率
	PaperTradingTakerFeeRate *float64 `json:"paper_trading_taker_fee_rate,omitempty"` // Taker手续费率（0.04%=0.0004；不填=默认；填0=禁用）
	PaperTradingSlippageRate *float64 `json:"paper_trading_slippage_rate,omitempty"`  // 滑点比例（0.05%=0.0005；不填=默认；填0=禁用）

	// 实盘/回测的成本假设（用于风控阈值、自动止盈、RR校验等；不影响实际下单）
	// 不填：使用默认（taker=0.0004, slippage=0.0005）；填0：表示不计成本（不推荐）
	AssumedTakerFeeRate *float64 `json:"assumed_taker_fee_rate,omitempty"`
	AssumedSlippageRate *float64 `json:"assumed_slippage_rate,omitempty"`

	// 最小风险回报比（默认 2.2，支持热更新）
	MinRiskReward float64 `json:"min_risk_reward,omitempty"`

	// 新增：Prompt策略选择（A 或 B）
	PromptStrategy string `json:"prompt_strategy,omitempty"`

	// 高峰时段暂停（节省 API 费用）
	PeakHourPause *PeakHourPauseConfig `json:"peak_hour_pause,omitempty"`
}

// PeakHourPauseConfig 高峰时段暂停配置
// 在高峰时段暂停 LLM 调用以节省 API 费用（如 DeepSeek 高峰定价 2x）。
// 有持仓时仍继续调用 LLM 直到平仓。
type PeakHourPauseConfig struct {
	Enabled bool   `json:"enabled"`         // 是否启用高峰暂停
	Start   string `json:"start,omitempty"` // 高峰开始时间 "HH:MM"（北京时间，默认 "09:00"）
	End     string `json:"end,omitempty"`   // 高峰结束时间 "HH:MM"（北京时间，默认 "18:00"）
}

// LeverageConfig 杠杆配置
type LeverageConfig struct {
	BTCETHLeverage                   int     `json:"btc_eth_leverage"`                               // BTC和ETH的杠杆倍数（主账户建议5-50，子账户≤5）
	AltcoinLeverage                  int     `json:"altcoin_leverage"`                               // 山寨币的杠杆倍数（主账户建议5-20，子账户≤5）
	AltcoinMaxPositionEquityMultiple float64 `json:"altcoin_max_position_equity_multiple,omitempty"` // 山寨币单币名义仓位上限：账户净值倍数（默认2.0）
}

// LeverageClipConfig 杠杆裁剪配置。
// 开启后，AI 给出的杠杆超过系统配置上限时，会在 validation 前裁剪到配置上限。
type LeverageClipConfig struct {
	Enabled   bool `json:"enabled,omitempty"`     // 是否启用杠杆裁剪
	ClipToMax bool `json:"clip_to_max,omitempty"` // 是否裁剪到当前币种配置上限
}

// MarginValidationConfig 开仓前保证金预检配置。
type MarginValidationConfig struct {
	Enabled                  bool    `json:"enabled,omitempty"`                     // 是否启用保证金预检
	AvailableBalanceUsagePct float64 `json:"available_balance_usage_pct,omitempty"` // 新增保证金最多使用可用余额百分比，默认95
	FeeBufferPct             float64 `json:"fee_buffer_pct,omitempty"`              // 按名义仓位预留费用/波动缓冲百分比，默认0.1
}

// StopLossDistanceConfig 止损最小距离配置
// 最终 minDist = max(minPct% * price, atrMult * ATR14, volMult * price * volatilityPct)
// 所有 pct 字段单位为百分比，如 0.15 表示 0.15%
type StopLossDistanceConfig struct {
	MajorMinPct  float64 `json:"major_min_pct,omitempty"`  // BTC/ETH 百分比底线（默认0.15%）
	MajorATRMult float64 `json:"major_atr_mult,omitempty"` // BTC/ETH ATR14倍数（默认0.3）
	MajorVolMult float64 `json:"major_vol_mult,omitempty"` // BTC/ETH 波动率倍数（默认0.3）
	AltMinPct    float64 `json:"alt_min_pct,omitempty"`    // 山寨币百分比底线（默认0.35%）
	AltATRMult   float64 `json:"alt_atr_mult,omitempty"`   // 山寨币 ATR14倍数（默认0.6）
	AltVolMult   float64 `json:"alt_vol_mult,omitempty"`   // 山寨币波动率倍数（默认0.5）
}

// AutoTakeProfitConfig 自动止盈配置
type AutoTakeProfitConfig struct {
	Stage0Threshold      float64               `json:"stage0_threshold,omitempty"`        // Stage0 触发净ROI%（默认10.0）
	Stage0ClosePct       float64               `json:"stage0_close_pct,omitempty"`        // Stage0 平仓比例%（默认35）
	Stage1Threshold      float64               `json:"stage1_threshold,omitempty"`        // Stage1 触发净ROI%（默认15.0）
	Stage1ClosePct       float64               `json:"stage1_close_pct,omitempty"`        // Stage1 平仓比例%（默认35）
	FullCloseThreshold   float64               `json:"full_close_threshold,omitempty"`    // 全部平仓净ROI%（默认22.0）
	CooldownMinutes      int                   `json:"cooldown_minutes,omitempty"`        // 冷却时间（分钟，默认15）
	BreakevenEnabled     bool                  `json:"breakeven_enabled,omitempty"`       // TP1 后设置交易级 breakeven
	BreakevenFloorUSDT   float64               `json:"breakeven_floor_usdt,omitempty"`    // 交易级 breakeven 最低净收益
	TrailingEnabled      bool                  `json:"trailing_enabled,omitempty"`        // TP2 后启用 trailing
	TrailingDistancePct  float64               `json:"trailing_distance_pct,omitempty"`   // trailing 距最佳价的价格百分比
	TrailingMinUpdatePct float64               `json:"trailing_min_update_pct,omitempty"` // 最小止损改善百分比
	Major                *AutoTakeProfitConfig `json:"major,omitempty"`                   // BTC/ETH 专用自动止盈配置；未设置字段继承顶层配置
}

// Config 总配置
type Config struct {
	Traders         []TraderConfig `json:"traders"`
	UseDefaultCoins bool           `json:"use_default_coins"` // 是否使用默认主流币种列表
	DefaultCoins    []string       `json:"default_coins"`     // 默认主流币种池
	CoinPoolAPIURL  string         `json:"coin_pool_api_url"`
	OITopAPIURL     string         `json:"oi_top_api_url"`
	// Binance Futures 环境：false=主网(https://fapi.binance.com)，true=测试网(https://testnet.binancefuture.com)
	// 说明：当前实现按“全局开关”切换，适用于一次运行只选一种环境的场景。
	BinanceTestnet     bool                   `json:"binance_testnet,omitempty"`
	APIServerPort      int                    `json:"api_server_port"`
	MaxDailyLoss       float64                `json:"max_daily_loss"`
	MaxDrawdown        float64                `json:"max_drawdown"`
	StopTradingMinutes int                    `json:"stop_trading_minutes"`
	Leverage           LeverageConfig         `json:"leverage"`                    // 杠杆配置
	LeverageClip       LeverageClipConfig     `json:"leverage_clip,omitempty"`     // 杠杆裁剪配置
	MarginValidation   MarginValidationConfig `json:"margin_validation,omitempty"` // 保证金预检配置
	MinRiskReward      float64                `json:"min_risk_reward"`             // 最小风险回报比
	StopLossDistance   StopLossDistanceConfig `json:"stop_loss_distance"`          // 止损最小距离配置
	AutoTakeProfit     AutoTakeProfitConfig   `json:"auto_take_profit"`            // 自动止盈配置
	MinHoldMinutes     int                    `json:"min_hold_minutes"`            // LLM 平仓最低持仓时间（分钟，0=不限制）

	// 候选币种 OI 价值过滤门槛（单位：百万USD，默认50）
	MinOIValueMillions float64 `json:"min_oi_value_millions,omitempty"`

	// 新增：是否开启数据录制（用于回测）
	EnableRecording bool `json:"enable_recording"` // 是否开启录制

	// 接续运行：启动时自动恢复交易（跳过手动点击开始）
	AutoResume bool `json:"auto_resume,omitempty"`
}

// LoadConfig 从文件加载配置
func LoadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 从环境变量读取 API 密钥（如果配置文件中为空）
	config.loadFromEnvironment()

	// 设置默认值：如果use_default_coins未设置（为false）且没有配置coin_pool_api_url，则默认使用默认币种列表
	if !config.UseDefaultCoins && config.CoinPoolAPIURL == "" {
		config.UseDefaultCoins = true
	}

	// 设置默认币种池
	if len(config.DefaultCoins) == 0 {
		config.DefaultCoins = []string{
			"BTCUSDT",
			"ETHUSDT",
			"SOLUSDT",
			"BNBUSDT",
			"XRPUSDT",
			"DOGEUSDT",
			"ADAUSDT",
			"HYPEUSDT",
		}
	}

	// 验证配置
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	return &config, nil
}

// loadFromEnvironment 从环境变量加载 API 密钥
func (c *Config) loadFromEnvironment() {
	for i := range c.Traders {
		trader := &c.Traders[i]

		// DeepSeek API Key
		if trader.DeepSeekKey == "" {
			if envKey := os.Getenv("DEEPSEEK_OPENAI_KEY"); envKey != "" {
				trader.DeepSeekKey = envKey
			}
		}

		// Qwen API Key
		if trader.QwenKey == "" {
			if envKey := os.Getenv("QWEN_OPENAI_API_KEY"); envKey != "" {
				trader.QwenKey = envKey
			}
		}

		// 币安 API Keys
		// - 主网：BINANCE_API_KEY / BINANCE_SECRET_KEY
		// - 测试网：BINANCE_TESTNET_API_KEY / BINANCE_TESTNET_SECRET_KEY
		if strings.TrimSpace(trader.Exchange) == "binance" {
			if c.BinanceTestnet {
				if trader.BinanceAPIKey == "" {
					if envKey := os.Getenv("BINANCE_TESTNET_API_KEY"); envKey != "" {
						trader.BinanceAPIKey = envKey
					}
				}
				if trader.BinanceSecretKey == "" {
					if envKey := os.Getenv("BINANCE_TESTNET_SECRET_KEY"); envKey != "" {
						trader.BinanceSecretKey = envKey
					}
				}
			} else {
				if trader.BinanceAPIKey == "" {
					if envKey := os.Getenv("BINANCE_API_KEY"); envKey != "" {
						trader.BinanceAPIKey = envKey
					}
				}
				if trader.BinanceSecretKey == "" {
					if envKey := os.Getenv("BINANCE_SECRET_KEY"); envKey != "" {
						trader.BinanceSecretKey = envKey
					}
				}
			}
		}

		// Hyperliquid Private Key
		if trader.HyperliquidPrivateKey == "" {
			if envKey := os.Getenv("HYPERLIQUID_PRIVATE_KEY"); envKey != "" {
				trader.HyperliquidPrivateKey = envKey
			}
		}

		// Aster Keys
		if trader.AsterUser == "" {
			if envKey := os.Getenv("ASTER_USER"); envKey != "" {
				trader.AsterUser = envKey
			}
		}
		if trader.AsterSigner == "" {
			if envKey := os.Getenv("ASTER_SIGNER"); envKey != "" {
				trader.AsterSigner = envKey
			}
		}
		if trader.AsterPrivateKey == "" {
			if envKey := os.Getenv("ASTER_PRIVATE_KEY"); envKey != "" {
				trader.AsterPrivateKey = envKey
			}
		}

		// 自定义 API Keys
		if trader.CustomAPIKey == "" {
			if envKey := os.Getenv("CUSTOM_API_KEY"); envKey != "" {
				trader.CustomAPIKey = envKey
			}
		}
		if trader.CustomAPIURL == "" {
			if envKey := os.Getenv("CUSTOM_API_URL"); envKey != "" {
				trader.CustomAPIURL = envKey
			}
		}
	}
}

// Validate 验证配置有效性
func (c *Config) Validate() error {
	if len(c.Traders) == 0 {
		return fmt.Errorf("至少需要配置一个trader")
	}

	traderIDs := make(map[string]bool)
	for i, trader := range c.Traders {
		if trader.ID == "" {
			return fmt.Errorf("trader[%d]: ID不能为空", i)
		}
		if traderIDs[trader.ID] {
			return fmt.Errorf("trader[%d]: ID '%s' 重复", i, trader.ID)
		}
		traderIDs[trader.ID] = true

		if trader.Name == "" {
			return fmt.Errorf("trader[%d]: Name不能为空", i)
		}
		if trader.AIModel != "qwen" && trader.AIModel != "deepseek" && trader.AIModel != "custom" {
			return fmt.Errorf("trader[%d]: ai_model必须是 'qwen', 'deepseek' 或 'custom'", i)
		}

		// 验证交易平台配置
		if trader.Exchange == "" {
			trader.Exchange = "binance" // 默认使用币安
		}
		if trader.Exchange != "binance" && trader.Exchange != "hyperliquid" && trader.Exchange != "aster" {
			return fmt.Errorf("trader[%d]: exchange必须是 'binance', 'hyperliquid' 或 'aster'", i)
		}

		// 新增：校验 PromptStrategy
		if trader.PromptStrategy != "" {
			ps := strings.ToUpper(trader.PromptStrategy)
			if ps != "A" && ps != "B" && ps != "V" {
				return fmt.Errorf("trader[%d]: prompt_strategy必须是 'A' 或 'B' 或 'V'", i)
			}
		}

		// 根据平台验证对应的密钥
		if trader.Exchange == "binance" {
			if trader.BinanceAPIKey == "" || trader.BinanceSecretKey == "" {
				return fmt.Errorf("trader[%d]: 使用币安时必须配置binance_api_key和binance_secret_key", i)
			}
		} else if trader.Exchange == "hyperliquid" {
			if trader.HyperliquidPrivateKey == "" {
				return fmt.Errorf("trader[%d]: 使用Hyperliquid时必须配置hyperliquid_private_key", i)
			}
		} else if trader.Exchange == "aster" {
			if trader.AsterUser == "" || trader.AsterSigner == "" || trader.AsterPrivateKey == "" {
				return fmt.Errorf("trader[%d]: 使用Aster时必须配置aster_user, aster_signer和aster_private_key", i)
			}
		}

		if trader.AIModel == "qwen" && trader.QwenKey == "" {
			return fmt.Errorf("trader[%d]: 使用Qwen时必须配置qwen_key", i)
		}
		if trader.AIModel == "deepseek" && trader.DeepSeekKey == "" {
			return fmt.Errorf("trader[%d]: 使用DeepSeek时必须配置deepseek_key", i)
		}
		if trader.AIModel == "custom" {
			if trader.CustomAPIURL == "" {
				return fmt.Errorf("trader[%d]: 使用自定义API时必须配置custom_api_url", i)
			}
			if trader.CustomAPIKey == "" {
				return fmt.Errorf("trader[%d]: 使用自定义API时必须配置custom_api_key", i)
			}
			if trader.CustomModelName == "" {
				return fmt.Errorf("trader[%d]: 使用自定义API时必须配置custom_model_name", i)
			}
		}
		if trader.InitialBalance <= 0 {
			return fmt.Errorf("trader[%d]: initial_balance必须大于0", i)
		}
		// paper trading 费用/滑点：允许不填（nil），若填写则必须≥0
		if trader.PaperTradingTakerFeeRate != nil && *trader.PaperTradingTakerFeeRate < 0 {
			return fmt.Errorf("trader[%d]: paper_trading_taker_fee_rate 必须≥0（不填则使用默认）", i)
		}
		if trader.PaperTradingSlippageRate != nil && *trader.PaperTradingSlippageRate < 0 {
			return fmt.Errorf("trader[%d]: paper_trading_slippage_rate 必须≥0（不填则使用默认）", i)
		}
		// 成本假设：允许不填（nil），若填写则必须≥0
		if trader.AssumedTakerFeeRate != nil && *trader.AssumedTakerFeeRate < 0 {
			return fmt.Errorf("trader[%d]: assumed_taker_fee_rate 必须≥0（不填则使用默认）", i)
		}
		if trader.AssumedSlippageRate != nil && *trader.AssumedSlippageRate < 0 {
			return fmt.Errorf("trader[%d]: assumed_slippage_rate 必须≥0（不填则使用默认）", i)
		}
		if trader.ScanIntervalMinutes <= 0 {
			trader.ScanIntervalMinutes = 3 // 默认3分钟
		}
	}

	if c.APIServerPort <= 0 {
		c.APIServerPort = 8080 // 默认8080端口
	}

	// 设置杠杆默认值（适配币安子账户限制，最大5倍）
	if c.Leverage.BTCETHLeverage <= 0 {
		c.Leverage.BTCETHLeverage = 5 // 默认5倍（安全值，适配子账户）
	}
	if c.Leverage.BTCETHLeverage > 5 {
		fmt.Printf("⚠️  警告: BTC/ETH杠杆设置为%dx，如果使用子账户可能会失败（子账户限制≤5x）\n", c.Leverage.BTCETHLeverage)
	}
	if c.Leverage.AltcoinLeverage <= 0 {
		c.Leverage.AltcoinLeverage = 5 // 默认5倍（安全值，适配子账户）
	}
	if c.Leverage.AltcoinLeverage > 5 {
		fmt.Printf("⚠️  警告: 山寨币杠杆设置为%dx，如果使用子账户可能会失败（子账户限制≤5x）\n", c.Leverage.AltcoinLeverage)
	}
	if c.Leverage.AltcoinMaxPositionEquityMultiple <= 0 {
		c.Leverage.AltcoinMaxPositionEquityMultiple = 2.0
	}

	return nil
}

// GetScanInterval 获取扫描间隔
func (tc *TraderConfig) GetScanInterval() time.Duration {
	return time.Duration(tc.ScanIntervalMinutes) * time.Minute
}
