#!/bin/bash

# 基于 data/records/ 目录的快速回测脚本
# 使用方法: ./scripts/test_records_quick.sh [新策略] [数据源策略] [开始周期] [结束周期]
# 示例: ./scripts/test_records_quick.sh V A 1 50

set -e

# 检查参数
if [ $# -lt 2 ]; then
    echo "📖 用法: $0 <新策略> <数据源策略> [开始周期] [结束周期]"
    echo ""
    echo "示例："
    echo "  $0 B A          # 在策略A录制数据上测试策略B（全部周期）"
    echo "  $0 V A 1 50     # 在策略A录制数据上测试策略V（前50个周期）"
    echo "  $0 A V          # 在策略V录制数据上测试策略A"
    echo ""
    echo "可用策略: A, B, V"
    echo "可用数据源:"
    echo "  - A: data/records/binance_deepseek_paper_strategyA/"
    echo "  - V: data/records/binance_deepseek_paper_strategyV/"
    exit 1
fi

NEW_STRATEGY=$1
DATA_SOURCE=$2
START_CYCLE=${3:-0}
END_CYCLE=${4:-0}

# 验证策略参数
if [[ ! "$NEW_STRATEGY" =~ ^[ABV]$ ]]; then
    echo "❌ 错误: 策略必须是 A, B 或 V"
    exit 1
fi

if [[ ! "$DATA_SOURCE" =~ ^[ABV]$ ]]; then
    echo "❌ 错误: 数据源必须是 A, B 或 V"
    exit 1
fi

# 检查API密钥
if [ -z "$DEEPSEEK_OPENAI_KEY" ] && [ -z "$QWEN_OPENAI_API_KEY" ]; then
    echo "❌ 错误: 未设置API密钥"
    echo "请设置以下环境变量之一:"
    echo "  export DEEPSEEK_OPENAI_KEY='your_key_here'"
    echo "  export QWEN_OPENAI_API_KEY='your_key_here'"
    exit 1
fi

# 构建参数
RECORD_DIR="data/records/binance_deepseek_paper_strategy${DATA_SOURCE}"
OUTPUT_FILE="results/records_strategy${NEW_STRATEGY}_on_${DATA_SOURCE}.json"

# 检查目录是否存在
if [ ! -d "$RECORD_DIR" ]; then
    echo "❌ 错误: 目录不存在: $RECORD_DIR"
    echo ""
    echo "可用的录制数据目录:"
    ls -d data/records/*/ 2>/dev/null || echo "  (无)"
    exit 1
fi

# 创建results目录
mkdir -p results

echo "🧪 回测配置:"
echo "  新策略:     $NEW_STRATEGY"
echo "  录制目录:   $RECORD_DIR"
if [ "$START_CYCLE" -gt 0 ] || [ "$END_CYCLE" -gt 0 ]; then
    echo "  周期范围:   $START_CYCLE - $END_CYCLE"
else
    echo "  周期范围:   全部"
fi
echo "  输出文件:   $OUTPUT_FILE"
echo ""

# 构建命令
CMD="go run cmd/test_records/main.go -records=$RECORD_DIR -strategy=$NEW_STRATEGY -output=$OUTPUT_FILE"

if [ "$START_CYCLE" -gt 0 ]; then
    CMD="$CMD -start=$START_CYCLE"
fi

if [ "$END_CYCLE" -gt 0 ]; then
    CMD="$CMD -end=$END_CYCLE"
fi

echo "🚀 开始回测..."
echo "命令: $CMD"
echo ""

# 执行回测
eval $CMD

echo ""
echo "✅ 回测完成！"
echo "📊 结果文件: $OUTPUT_FILE"

