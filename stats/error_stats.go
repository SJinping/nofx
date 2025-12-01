package stats

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrorType 错误类型枚举
type ErrorType string

const (
	// LLM API 相关错误
	ErrLLMAPITimeout     ErrorType = "llm_api_timeout"      // LLM API 超时
	ErrLLMAPINetwork     ErrorType = "llm_api_network"      // LLM API 网络错误
	ErrLLMAPIAuth        ErrorType = "llm_api_auth"         // LLM API 认证失败
	ErrLLMAPIResponse    ErrorType = "llm_api_response"     // LLM API 响应错误（非200）
	ErrLLMAPIParse       ErrorType = "llm_api_parse"        // LLM API 响应解析失败
	ErrLLMAPIEmpty       ErrorType = "llm_api_empty"        // LLM API 空响应
	ErrLLMAPIRetryFailed ErrorType = "llm_api_retry_failed" // LLM API 重试后仍失败

	// AI 决策解析错误
	ErrDecisionJSONNotFound ErrorType = "decision_json_not_found" // 找不到JSON数组
	ErrDecisionJSONParse    ErrorType = "decision_json_parse"     // JSON解析失败
	ErrDecisionExtract      ErrorType = "decision_extract"        // 提取决策失败

	// AI 决策验证错误
	ErrDecisionInvalidAction ErrorType = "decision_invalid_action" // 无效的action
	ErrDecisionLeverage      ErrorType = "decision_leverage"       // 杠杆超限
	ErrDecisionPositionSize  ErrorType = "decision_position_size"  // 仓位大小超限
	ErrDecisionStopLoss      ErrorType = "decision_stop_loss"      // 止损设置错误
	ErrDecisionRiskReward    ErrorType = "decision_risk_reward"    // 风险回报比过低
	ErrDecisionValidateOther ErrorType = "decision_validate_other" // 其他验证错误

	// 市场数据错误
	ErrMarketKline3m     ErrorType = "market_kline_3m"   // 3分钟K线获取失败
	ErrMarketKline4h     ErrorType = "market_kline_4h"   // 4小时K线获取失败
	ErrMarketOI          ErrorType = "market_oi"         // OI数据获取失败
	ErrMarketFundingRate ErrorType = "market_funding"    // 资金费率获取失败
	ErrMarketDataOther   ErrorType = "market_data_other" // 其他市场数据错误

	// 币种池错误
	ErrCoinPoolAPI   ErrorType = "coin_pool_api"   // 币种池API请求失败
	ErrCoinPoolParse ErrorType = "coin_pool_parse" // 币种池解析失败
	ErrCoinPoolEmpty ErrorType = "coin_pool_empty" // 币种池为空
	ErrOITopAPI      ErrorType = "oi_top_api"      // OI Top API请求失败
	ErrOITopParse    ErrorType = "oi_top_parse"    // OI Top解析失败

	// 账户信息错误
	ErrAccountBalance  ErrorType = "account_balance"  // 获取账户余额失败
	ErrAccountPosition ErrorType = "account_position" // 获取持仓失败

	// 交易执行错误
	ErrTradeOpenLong    ErrorType = "trade_open_long"    // 开多仓失败
	ErrTradeOpenShort   ErrorType = "trade_open_short"   // 开空仓失败
	ErrTradeCloseLong   ErrorType = "trade_close_long"   // 平多仓失败
	ErrTradeCloseShort  ErrorType = "trade_close_short"  // 平空仓失败
	ErrTradeSetLeverage ErrorType = "trade_set_leverage" // 设置杠杆失败
	ErrTradeSetStopLoss ErrorType = "trade_set_sl"       // 设置止损失败
	ErrTradeSetTP       ErrorType = "trade_set_tp"       // 设置止盈失败
	ErrTradeCancelOrder ErrorType = "trade_cancel_order" // 取消订单失败
	ErrTradeOther       ErrorType = "trade_other"        // 其他交易错误

	// 上下文构建错误
	ErrContextBuild ErrorType = "context_build" // 构建交易上下文失败
)

// ErrorCategory 错误大类
type ErrorCategory string

const (
	CategoryLLMAPI           ErrorCategory = "LLM_API"           // LLM API 调用
	CategoryDecisionParse    ErrorCategory = "DECISION_PARSE"    // 决策解析
	CategoryDecisionValidate ErrorCategory = "DECISION_VALIDATE" // 决策验证
	CategoryMarketData       ErrorCategory = "MARKET_DATA"       // 市场数据
	CategoryCoinPool         ErrorCategory = "COIN_POOL"         // 币种池
	CategoryAccount          ErrorCategory = "ACCOUNT"           // 账户信息
	CategoryTradeExec        ErrorCategory = "TRADE_EXECUTION"   // 交易执行
	CategoryContext          ErrorCategory = "CONTEXT_BUILD"     // 上下文构建
)

// GetCategory 获取错误类型所属大类
func (et ErrorType) GetCategory() ErrorCategory {
	switch et {
	case ErrLLMAPITimeout, ErrLLMAPINetwork, ErrLLMAPIAuth, ErrLLMAPIResponse, ErrLLMAPIParse, ErrLLMAPIEmpty, ErrLLMAPIRetryFailed:
		return CategoryLLMAPI
	case ErrDecisionJSONNotFound, ErrDecisionJSONParse, ErrDecisionExtract:
		return CategoryDecisionParse
	case ErrDecisionInvalidAction, ErrDecisionLeverage, ErrDecisionPositionSize, ErrDecisionStopLoss, ErrDecisionRiskReward, ErrDecisionValidateOther:
		return CategoryDecisionValidate
	case ErrMarketKline3m, ErrMarketKline4h, ErrMarketOI, ErrMarketFundingRate, ErrMarketDataOther:
		return CategoryMarketData
	case ErrCoinPoolAPI, ErrCoinPoolParse, ErrCoinPoolEmpty, ErrOITopAPI, ErrOITopParse:
		return CategoryCoinPool
	case ErrAccountBalance, ErrAccountPosition:
		return CategoryAccount
	case ErrTradeOpenLong, ErrTradeOpenShort, ErrTradeCloseLong, ErrTradeCloseShort, ErrTradeSetLeverage, ErrTradeSetStopLoss, ErrTradeSetTP, ErrTradeCancelOrder, ErrTradeOther:
		return CategoryTradeExec
	case ErrContextBuild:
		return CategoryContext
	default:
		return "UNKNOWN"
	}
}

// ErrorRecord 单次错误记录
type ErrorRecord struct {
	Type      ErrorType     `json:"type"`
	Category  ErrorCategory `json:"category"`
	Message   string        `json:"message"`
	Symbol    string        `json:"symbol,omitempty"` // 相关币种（如果有）
	Timestamp time.Time     `json:"timestamp"`
	CycleNum  int           `json:"cycle_num"` // 发生在第几个周期
}

// ErrorStats 错误统计
type ErrorStats struct {
	mu sync.RWMutex

	// 持久化配置
	filePath                string `json:"-"` // 持久化文件路径（不序列化）
	saveInterval            int    `json:"-"` // 每隔多少次错误保存一次（0表示每次都保存）
	errorCountSinceLastSave int    `json:"-"` // 上次保存后的错误计数

	// 基本计数
	TotalErrors      int                   `json:"total_errors"`
	ErrorsByType     map[ErrorType]int     `json:"errors_by_type"`
	ErrorsByCategory map[ErrorCategory]int `json:"errors_by_category"`

	// 最近错误记录（保留最近100条）
	RecentErrors    []ErrorRecord `json:"recent_errors"`
	maxRecentErrors int

	// 时间统计
	StartTime     time.Time `json:"start_time"`
	LastErrorTime time.Time `json:"last_error_time,omitempty"`

	// 周期统计
	TotalCycles        int `json:"total_cycles"`
	SuccessfulCycles   int `json:"successful_cycles"`
	FailedCycles       int `json:"failed_cycles"`
	CurrentCycleErrors int `json:"current_cycle_errors"` // 当前周期的错误数
}

// 全局错误统计实例（按trader分开）
var (
	globalStats    = make(map[string]*ErrorStats)
	globalStatsMu  sync.RWMutex
	globalStatsDir = "decision_logs" // 默认存储目录
)

// SetErrorStatsDir 设置错误统计文件的存储目录
func SetErrorStatsDir(dir string) {
	globalStatsDir = dir
}

// GetErrorStats 获取指定trader的错误统计实例（自动从文件加载历史数据）
func GetErrorStats(traderID string) *ErrorStats {
	globalStatsMu.RLock()
	stats, exists := globalStats[traderID]
	globalStatsMu.RUnlock()

	if exists {
		return stats
	}

	// 不存在则创建新的
	globalStatsMu.Lock()
	defer globalStatsMu.Unlock()

	// 双重检查
	if stats, exists = globalStats[traderID]; exists {
		return stats
	}

	// 构建文件路径
	filePath := filepath.Join(globalStatsDir, traderID, "error_stats.json")

	// 创建新实例并尝试加载历史数据
	stats = NewErrorStatsWithFile(filePath)
	globalStats[traderID] = stats

	return stats
}

// GetErrorStatsWithPath 获取指定路径的错误统计实例（自定义路径）
func GetErrorStatsWithPath(traderID, filePath string) *ErrorStats {
	globalStatsMu.Lock()
	defer globalStatsMu.Unlock()

	if stats, exists := globalStats[traderID]; exists {
		return stats
	}

	stats := NewErrorStatsWithFile(filePath)
	globalStats[traderID] = stats
	return stats
}

// GetAllErrorStats 获取所有trader的错误统计
func GetAllErrorStats() map[string]*ErrorStats {
	globalStatsMu.RLock()
	defer globalStatsMu.RUnlock()

	result := make(map[string]*ErrorStats)
	for k, v := range globalStats {
		result[k] = v
	}
	return result
}

// NewErrorStats 创建新的错误统计实例（不带持久化）
func NewErrorStats() *ErrorStats {
	return &ErrorStats{
		ErrorsByType:     make(map[ErrorType]int),
		ErrorsByCategory: make(map[ErrorCategory]int),
		RecentErrors:     make([]ErrorRecord, 0, 100),
		maxRecentErrors:  100,
		StartTime:        time.Now(),
		saveInterval:     5, // 每5次错误保存一次
	}
}

// NewErrorStatsWithFile 创建带持久化的错误统计实例
func NewErrorStatsWithFile(filePath string) *ErrorStats {
	es := &ErrorStats{
		filePath:         filePath,
		ErrorsByType:     make(map[ErrorType]int),
		ErrorsByCategory: make(map[ErrorCategory]int),
		RecentErrors:     make([]ErrorRecord, 0, 100),
		maxRecentErrors:  100,
		StartTime:        time.Now(),
		saveInterval:     5, // 每5次错误保存一次（减少IO）
	}

	// 尝试从文件加载历史数据
	if err := es.LoadFromFile(); err != nil {
		if !os.IsNotExist(err) {
			log.Printf("⚠️  加载错误统计历史数据失败: %v", err)
		}
		// 文件不存在不是错误，使用新的空统计
	} else if es.TotalErrors > 0 {
		log.Printf("📂 已加载错误统计历史数据: %d 个错误, %d 个周期", es.TotalErrors, es.TotalCycles)
	}

	return es
}

// RecordError 记录一次错误
func (es *ErrorStats) RecordError(errType ErrorType, message string, symbol string, cycleNum int) {
	es.mu.Lock()

	es.TotalErrors++
	es.ErrorsByType[errType]++

	category := errType.GetCategory()
	es.ErrorsByCategory[category]++

	es.LastErrorTime = time.Now()
	es.CurrentCycleErrors++
	es.errorCountSinceLastSave++

	// 添加到最近错误列表
	record := ErrorRecord{
		Type:      errType,
		Category:  category,
		Message:   message,
		Symbol:    symbol,
		Timestamp: time.Now(),
		CycleNum:  cycleNum,
	}

	es.RecentErrors = append(es.RecentErrors, record)
	if len(es.RecentErrors) > es.maxRecentErrors {
		es.RecentErrors = es.RecentErrors[1:]
	}

	// 判断是否需要保存
	shouldSave := es.filePath != "" && (es.saveInterval == 0 || es.errorCountSinceLastSave >= es.saveInterval)

	es.mu.Unlock()

	// 在锁外保存文件（避免阻塞其他操作）
	if shouldSave {
		es.saveToFileAsync()
	}
}

// StartCycle 开始新的周期
func (es *ErrorStats) StartCycle() {
	es.mu.Lock()
	defer es.mu.Unlock()

	es.TotalCycles++
	es.CurrentCycleErrors = 0
}

// EndCycle 结束周期，标记成功或失败，并保存到文件
func (es *ErrorStats) EndCycle(success bool) {
	es.mu.Lock()
	if success {
		es.SuccessfulCycles++
	} else {
		es.FailedCycles++
	}
	shouldSave := es.filePath != ""
	es.mu.Unlock()

	// 每个周期结束后保存（确保数据不丢失）
	if shouldSave {
		es.saveToFileAsync()
	}
}

// GetSummary 获取错误统计摘要
func (es *ErrorStats) GetSummary() map[string]interface{} {
	es.mu.RLock()
	defer es.mu.RUnlock()

	runtime := time.Since(es.StartTime)

	// 计算各类错误占比
	categoryPercentage := make(map[ErrorCategory]float64)
	if es.TotalErrors > 0 {
		for cat, count := range es.ErrorsByCategory {
			categoryPercentage[cat] = float64(count) / float64(es.TotalErrors) * 100
		}
	}

	// 成功率
	cycleSuccessRate := 0.0
	if es.TotalCycles > 0 {
		cycleSuccessRate = float64(es.SuccessfulCycles) / float64(es.TotalCycles) * 100
	}

	// 错误频率（每小时）
	errorRate := 0.0
	if runtime.Hours() > 0 {
		errorRate = float64(es.TotalErrors) / runtime.Hours()
	}

	return map[string]interface{}{
		"total_errors":        es.TotalErrors,
		"errors_by_type":      es.ErrorsByType,
		"errors_by_category":  es.ErrorsByCategory,
		"category_percentage": categoryPercentage,
		"total_cycles":        es.TotalCycles,
		"successful_cycles":   es.SuccessfulCycles,
		"failed_cycles":       es.FailedCycles,
		"cycle_success_rate":  cycleSuccessRate,
		"error_rate_per_hour": errorRate,
		"runtime_minutes":     int(runtime.Minutes()),
		"start_time":          es.StartTime.Format("2006-01-02 15:04:05"),
		"last_error_time":     es.LastErrorTime.Format("2006-01-02 15:04:05"),
	}
}

// GetRecentErrors 获取最近的错误记录
func (es *ErrorStats) GetRecentErrors(limit int) []ErrorRecord {
	es.mu.RLock()
	defer es.mu.RUnlock()

	if limit <= 0 || limit > len(es.RecentErrors) {
		limit = len(es.RecentErrors)
	}

	// 返回最近的limit条记录（从后往前）
	start := len(es.RecentErrors) - limit
	if start < 0 {
		start = 0
	}

	result := make([]ErrorRecord, limit)
	copy(result, es.RecentErrors[start:])
	return result
}

// PrintSummary 打印错误统计摘要到日志
func (es *ErrorStats) PrintSummary() {
	es.mu.RLock()
	defer es.mu.RUnlock()

	log.Println()
	log.Println("╔══════════════════════════════════════════════════════════════╗")
	log.Println("║                    📊 错误统计报告                            ║")
	log.Println("╠══════════════════════════════════════════════════════════════╣")

	runtime := time.Since(es.StartTime)
	log.Printf("║  运行时间: %d 分钟                                           ", int(runtime.Minutes()))
	log.Printf("║  总周期数: %d (成功: %d, 失败: %d)                            ", es.TotalCycles, es.SuccessfulCycles, es.FailedCycles)

	if es.TotalCycles > 0 {
		successRate := float64(es.SuccessfulCycles) / float64(es.TotalCycles) * 100
		log.Printf("║  周期成功率: %.1f%%                                          ", successRate)
	}

	log.Printf("║  总错误数: %d                                                 ", es.TotalErrors)

	if es.TotalErrors > 0 {
		log.Println("╠══════════════════════════════════════════════════════════════╣")
		log.Println("║  错误分类统计:                                               ║")

		// 按大类输出
		for category, count := range es.ErrorsByCategory {
			pct := float64(count) / float64(es.TotalErrors) * 100
			log.Printf("║    %s: %d (%.1f%%)                                   ", category, count, pct)
		}
	}

	log.Println("╚══════════════════════════════════════════════════════════════╝")
	log.Println()
}

// ToJSON 导出为JSON
func (es *ErrorStats) ToJSON() (string, error) {
	es.mu.RLock()
	defer es.mu.RUnlock()

	data, err := json.MarshalIndent(es.GetSummary(), "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ===== 辅助函数 - 根据错误信息自动分类 =====

// ClassifyLLMError 根据LLM API错误信息分类
func ClassifyLLMError(errMsg string) ErrorType {
	errLower := strings.ToLower(errMsg)

	if strings.Contains(errLower, "timeout") {
		return ErrLLMAPITimeout
	}
	if strings.Contains(errLower, "eof") || strings.Contains(errLower, "connection") ||
		strings.Contains(errLower, "network") || strings.Contains(errLower, "no such host") {
		return ErrLLMAPINetwork
	}
	if strings.Contains(errLower, "api密钥") || strings.Contains(errLower, "apikey") ||
		strings.Contains(errLower, "unauthorized") || strings.Contains(errLower, "401") {
		return ErrLLMAPIAuth
	}
	if strings.Contains(errLower, "status") || strings.Contains(errLower, "api返回错误") {
		return ErrLLMAPIResponse
	}
	if strings.Contains(errLower, "解析") || strings.Contains(errLower, "unmarshal") ||
		strings.Contains(errLower, "parse") {
		return ErrLLMAPIParse
	}
	if strings.Contains(errLower, "空响应") || strings.Contains(errLower, "empty") {
		return ErrLLMAPIEmpty
	}
	if strings.Contains(errLower, "重试") || strings.Contains(errLower, "retry") {
		return ErrLLMAPIRetryFailed
	}

	return ErrLLMAPIResponse // 默认
}

// ClassifyDecisionParseError 根据决策解析错误信息分类
func ClassifyDecisionParseError(errMsg string) ErrorType {
	errLower := strings.ToLower(errMsg)

	if strings.Contains(errLower, "找不到json") || strings.Contains(errLower, "json数组起始") ||
		strings.Contains(errLower, "json数组结束") {
		return ErrDecisionJSONNotFound
	}
	if strings.Contains(errLower, "json解析") || strings.Contains(errLower, "unmarshal") {
		return ErrDecisionJSONParse
	}

	return ErrDecisionExtract // 默认
}

// ClassifyDecisionValidateError 根据决策验证错误信息分类
func ClassifyDecisionValidateError(errMsg string) ErrorType {
	errLower := strings.ToLower(errMsg)

	if strings.Contains(errLower, "action") || strings.Contains(errLower, "动作") {
		return ErrDecisionInvalidAction
	}
	if strings.Contains(errLower, "杠杆") || strings.Contains(errLower, "leverage") {
		return ErrDecisionLeverage
	}
	if strings.Contains(errLower, "仓位") || strings.Contains(errLower, "position") {
		return ErrDecisionPositionSize
	}
	if strings.Contains(errLower, "止损") || strings.Contains(errLower, "止盈") ||
		strings.Contains(errLower, "stoploss") || strings.Contains(errLower, "takeprofit") {
		return ErrDecisionStopLoss
	}
	if strings.Contains(errLower, "风险回报") || strings.Contains(errLower, "riskreward") {
		return ErrDecisionRiskReward
	}

	return ErrDecisionValidateOther // 默认
}

// ClassifyMarketDataError 根据市场数据错误信息分类
func ClassifyMarketDataError(errMsg string) ErrorType {
	errLower := strings.ToLower(errMsg)

	if strings.Contains(errLower, "3分钟") || strings.Contains(errLower, "3m") {
		return ErrMarketKline3m
	}
	if strings.Contains(errLower, "4小时") || strings.Contains(errLower, "4h") {
		return ErrMarketKline4h
	}
	if strings.Contains(errLower, "openinterest") || strings.Contains(errLower, "持仓量") || strings.Contains(errLower, "oi数据") {
		return ErrMarketOI
	}
	if strings.Contains(errLower, "fundingrate") || strings.Contains(errLower, "资金费率") {
		return ErrMarketFundingRate
	}

	return ErrMarketDataOther // 默认
}

// ClassifyTradeError 根据交易错误信息分类
func ClassifyTradeError(errMsg string) ErrorType {
	errLower := strings.ToLower(errMsg)

	if strings.Contains(errLower, "开多") || strings.Contains(errLower, "open_long") {
		return ErrTradeOpenLong
	}
	if strings.Contains(errLower, "开空") || strings.Contains(errLower, "open_short") {
		return ErrTradeOpenShort
	}
	if strings.Contains(errLower, "平多") || strings.Contains(errLower, "close_long") {
		return ErrTradeCloseLong
	}
	if strings.Contains(errLower, "平空") || strings.Contains(errLower, "close_short") {
		return ErrTradeCloseShort
	}
	if strings.Contains(errLower, "杠杆") || strings.Contains(errLower, "leverage") {
		return ErrTradeSetLeverage
	}
	if strings.Contains(errLower, "止损") || strings.Contains(errLower, "stoploss") {
		return ErrTradeSetStopLoss
	}
	if strings.Contains(errLower, "止盈") || strings.Contains(errLower, "takeprofit") {
		return ErrTradeSetTP
	}
	if strings.Contains(errLower, "取消") || strings.Contains(errLower, "cancel") {
		return ErrTradeCancelOrder
	}

	return ErrTradeOther // 默认
}

// GetErrorTypeDescription 获取错误类型的中文描述
func GetErrorTypeDescription(et ErrorType) string {
	descriptions := map[ErrorType]string{
		ErrLLMAPITimeout:     "LLM API 超时",
		ErrLLMAPINetwork:     "LLM API 网络错误",
		ErrLLMAPIAuth:        "LLM API 认证失败",
		ErrLLMAPIResponse:    "LLM API 响应错误",
		ErrLLMAPIParse:       "LLM API 响应解析失败",
		ErrLLMAPIEmpty:       "LLM API 空响应",
		ErrLLMAPIRetryFailed: "LLM API 重试后仍失败",

		ErrDecisionJSONNotFound: "找不到JSON决策数组",
		ErrDecisionJSONParse:    "JSON决策解析失败",
		ErrDecisionExtract:      "提取决策失败",

		ErrDecisionInvalidAction: "无效的交易动作",
		ErrDecisionLeverage:      "杠杆设置超限",
		ErrDecisionPositionSize:  "仓位大小超限",
		ErrDecisionStopLoss:      "止损/止盈设置错误",
		ErrDecisionRiskReward:    "风险回报比过低",
		ErrDecisionValidateOther: "其他决策验证错误",

		ErrMarketKline3m:     "3分钟K线获取失败",
		ErrMarketKline4h:     "4小时K线获取失败",
		ErrMarketOI:          "持仓量数据获取失败",
		ErrMarketFundingRate: "资金费率获取失败",
		ErrMarketDataOther:   "其他市场数据错误",

		ErrCoinPoolAPI:   "币种池API请求失败",
		ErrCoinPoolParse: "币种池数据解析失败",
		ErrCoinPoolEmpty: "币种池为空",
		ErrOITopAPI:      "OI Top API请求失败",
		ErrOITopParse:    "OI Top数据解析失败",

		ErrAccountBalance:  "获取账户余额失败",
		ErrAccountPosition: "获取持仓信息失败",

		ErrTradeOpenLong:    "开多仓失败",
		ErrTradeOpenShort:   "开空仓失败",
		ErrTradeCloseLong:   "平多仓失败",
		ErrTradeCloseShort:  "平空仓失败",
		ErrTradeSetLeverage: "设置杠杆失败",
		ErrTradeSetStopLoss: "设置止损失败",
		ErrTradeSetTP:       "设置止盈失败",
		ErrTradeCancelOrder: "取消订单失败",
		ErrTradeOther:       "其他交易错误",

		ErrContextBuild: "构建交易上下文失败",
	}

	if desc, ok := descriptions[et]; ok {
		return desc
	}
	return string(et)
}

// GetCategoryDescription 获取错误大类的中文描述
func GetCategoryDescription(cat ErrorCategory) string {
	descriptions := map[ErrorCategory]string{
		CategoryLLMAPI:           "LLM API调用",
		CategoryDecisionParse:    "AI决策解析",
		CategoryDecisionValidate: "AI决策验证",
		CategoryMarketData:       "市场数据获取",
		CategoryCoinPool:         "币种池获取",
		CategoryAccount:          "账户信息获取",
		CategoryTradeExec:        "交易执行",
		CategoryContext:          "上下文构建",
	}

	if desc, ok := descriptions[cat]; ok {
		return desc
	}
	return string(cat)
}

// FormatErrorSummary 格式化错误统计摘要为字符串
func (es *ErrorStats) FormatErrorSummary() string {
	es.mu.RLock()
	defer es.mu.RUnlock()

	var sb strings.Builder

	runtime := time.Since(es.StartTime)
	sb.WriteString(fmt.Sprintf("运行时间: %.0f分钟 | ", runtime.Minutes()))
	sb.WriteString(fmt.Sprintf("总周期: %d | ", es.TotalCycles))
	sb.WriteString(fmt.Sprintf("成功: %d | ", es.SuccessfulCycles))
	sb.WriteString(fmt.Sprintf("失败: %d | ", es.FailedCycles))
	sb.WriteString(fmt.Sprintf("总错误: %d", es.TotalErrors))

	if es.TotalErrors > 0 {
		sb.WriteString("\n错误分布: ")
		for category, count := range es.ErrorsByCategory {
			pct := float64(count) / float64(es.TotalErrors) * 100
			sb.WriteString(fmt.Sprintf("%s=%d(%.0f%%) ", GetCategoryDescription(category), count, pct))
		}
	}

	return sb.String()
}

// ===== 持久化功能 =====

// persistentData 用于JSON序列化的持久化数据结构
type persistentData struct {
	TotalErrors      int                   `json:"total_errors"`
	ErrorsByType     map[ErrorType]int     `json:"errors_by_type"`
	ErrorsByCategory map[ErrorCategory]int `json:"errors_by_category"`
	RecentErrors     []ErrorRecord         `json:"recent_errors"`
	StartTime        time.Time             `json:"start_time"`
	LastErrorTime    time.Time             `json:"last_error_time,omitempty"`
	TotalCycles      int                   `json:"total_cycles"`
	SuccessfulCycles int                   `json:"successful_cycles"`
	FailedCycles     int                   `json:"failed_cycles"`
	SavedAt          time.Time             `json:"saved_at"`
}

// SaveToFile 保存错误统计到文件
func (es *ErrorStats) SaveToFile() error {
	if es.filePath == "" {
		return nil // 没有配置文件路径，跳过
	}

	es.mu.RLock()
	data := persistentData{
		TotalErrors:      es.TotalErrors,
		ErrorsByType:     es.ErrorsByType,
		ErrorsByCategory: es.ErrorsByCategory,
		RecentErrors:     es.RecentErrors,
		StartTime:        es.StartTime,
		LastErrorTime:    es.LastErrorTime,
		TotalCycles:      es.TotalCycles,
		SuccessfulCycles: es.SuccessfulCycles,
		FailedCycles:     es.FailedCycles,
		SavedAt:          time.Now(),
	}
	es.mu.RUnlock()

	// 确保目录存在
	dir := filepath.Dir(es.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// 序列化为JSON
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化失败: %w", err)
	}

	// 写入临时文件，然后重命名（原子操作）
	tmpFile := es.filePath + ".tmp"
	if err := ioutil.WriteFile(tmpFile, jsonData, 0644); err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}

	if err := os.Rename(tmpFile, es.filePath); err != nil {
		os.Remove(tmpFile) // 清理临时文件
		return fmt.Errorf("重命名文件失败: %w", err)
	}

	// 重置保存计数
	es.mu.Lock()
	es.errorCountSinceLastSave = 0
	es.mu.Unlock()

	return nil
}

// saveToFileAsync 异步保存到文件（不阻塞主流程）
func (es *ErrorStats) saveToFileAsync() {
	go func() {
		if err := es.SaveToFile(); err != nil {
			log.Printf("⚠️  保存错误统计失败: %v", err)
		}
	}()
}

// LoadFromFile 从文件加载错误统计
func (es *ErrorStats) LoadFromFile() error {
	if es.filePath == "" {
		return nil
	}

	data, err := ioutil.ReadFile(es.filePath)
	if err != nil {
		return err // 包括 os.IsNotExist
	}

	var loaded persistentData
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("解析JSON失败: %w", err)
	}

	es.mu.Lock()
	defer es.mu.Unlock()

	es.TotalErrors = loaded.TotalErrors
	es.ErrorsByType = loaded.ErrorsByType
	es.ErrorsByCategory = loaded.ErrorsByCategory
	es.RecentErrors = loaded.RecentErrors
	es.StartTime = loaded.StartTime
	es.LastErrorTime = loaded.LastErrorTime
	es.TotalCycles = loaded.TotalCycles
	es.SuccessfulCycles = loaded.SuccessfulCycles
	es.FailedCycles = loaded.FailedCycles

	// 确保map不为nil
	if es.ErrorsByType == nil {
		es.ErrorsByType = make(map[ErrorType]int)
	}
	if es.ErrorsByCategory == nil {
		es.ErrorsByCategory = make(map[ErrorCategory]int)
	}
	if es.RecentErrors == nil {
		es.RecentErrors = make([]ErrorRecord, 0, 100)
	}

	return nil
}

// SetFilePath 设置持久化文件路径（运行时更改）
func (es *ErrorStats) SetFilePath(filePath string) {
	es.mu.Lock()
	es.filePath = filePath
	es.mu.Unlock()
}

// GetFilePath 获取持久化文件路径
func (es *ErrorStats) GetFilePath() string {
	es.mu.RLock()
	defer es.mu.RUnlock()
	return es.filePath
}

// ForceSave 强制立即保存（用于程序退出前）
func (es *ErrorStats) ForceSave() error {
	return es.SaveToFile()
}

// SaveAllErrorStats 保存所有trader的错误统计（用于程序退出前）
func SaveAllErrorStats() {
	globalStatsMu.RLock()
	defer globalStatsMu.RUnlock()

	for traderID, stats := range globalStats {
		if err := stats.ForceSave(); err != nil {
			log.Printf("⚠️  保存 %s 错误统计失败: %v", traderID, err)
		} else if stats.TotalErrors > 0 {
			log.Printf("💾 已保存 %s 错误统计 (%d 个错误)", traderID, stats.TotalErrors)
		}
	}
}
