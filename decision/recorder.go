package decision

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// saveContextToFile 将上下文保存到文件（用于回放/回测）
// 文件路径格式: data/records/{trader_id}/{timestamp}_{cycle_num}.json
func saveContextToFile(ctx *Context, traderID string) error {
	// 1. 确定目录
	// 使用 traderID 作为子目录，方便区分不同策略/账户的数据
	dir := fmt.Sprintf("data/records/%s", traderID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建录制目录失败: %w", err)
	}

	// 2. 确定文件名
	// 格式: 20231201_120000_cycle61.json
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("%s/%s_cycle%d.json", dir, timestamp, ctx.CallCount)

	// 3. 序列化
	data, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return fmt.Errorf("Context序列化失败: %w", err)
	}

	// 4. 写入文件
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("写入录制文件失败: %w", err)
	}

	// log.Printf("📼 已录制决策上下文: %s", filename) // 减少日志噪音，可选
	return nil
}

