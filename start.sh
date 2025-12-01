#!/bin/bash

# NOFX AI Trading System - Docker Quick Start Script
# 使用方法: ./start.sh [command]

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 打印带颜色的消息
print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# 检查 Docker 是否安装
check_docker() {
    if ! command -v docker &> /dev/null; then
        print_error "Docker 未安装！请先安装 Docker: https://docs.docker.com/get-docker/"
        exit 1
    fi

    if ! command -v docker-compose &> /dev/null; then
        print_error "Docker Compose 未安装！请先安装 Docker Compose"
        exit 1
    fi

    print_success "Docker 和 Docker Compose 已安装"
}

# 检查配置文件
check_config() {
    if [ ! -f "config.json" ]; then
        print_warning "config.json 不存在，从模板复制..."
        cp config.json.example config.json
        print_info "请编辑 config.json 填入你的 API 密钥"
        print_info "运行: nano config.json 或使用其他编辑器"
        exit 1
    fi
    print_success "配置文件存在"
}

# 启动服务
start() {
    print_info "正在启动 NOFX AI Trading System..."

    if [ "$1" == "--build" ]; then
        print_info "重新构建镜像..."
        docker-compose up -d --build
    else
        docker-compose up -d
    fi

    print_success "服务已启动！"
    print_info "Web 界面: http://localhost:3000"
    print_info "API 端点: http://localhost:8080"
    print_info ""
    print_info "查看日志: ./start.sh logs"
    print_info "停止服务: ./start.sh stop"
}

# 停止服务
stop() {
    print_info "正在停止服务..."
    docker-compose stop
    print_success "服务已停止"
}

# 重启服务
restart() {
    print_info "正在重启服务..."
    docker-compose restart
    print_success "服务已重启"
}

# 查看日志
logs() {
    if [ -z "$2" ]; then
        docker-compose logs -f
    else
        docker-compose logs -f "$2"
    fi
}

# 查看状态
status() {
    print_info "服务状态:"
    docker-compose ps
    echo ""
    print_info "健康检查:"
    curl -s http://localhost:8080/health | jq '.' || echo "后端未响应"
}

# 清理
clean() {
    print_warning "这将删除所有容器和数据！"
    read -p "确认删除？(yes/no): " confirm
    if [ "$confirm" == "yes" ]; then
        print_info "正在清理..."
        docker-compose down -v
        print_success "清理完成"
    else
        print_info "已取消"
    fi
}

# 平掉所有模型的所有持仓
close_all() {
    print_warning "⚠️  即将平掉所有模型的所有持仓！"
    echo ""
    read -p "确认操作？(yes/no): " confirm
    if [ "$confirm" == "yes" ]; then
        print_info "正在执行平仓操作..."
        response=$(curl -s -X POST http://localhost:8080/api/close-all-positions)
        
        if echo "$response" | jq -e '.success' > /dev/null 2>&1; then
            if [ "$(echo "$response" | jq -r '.success')" == "true" ]; then
                print_success "✓ 平仓操作已完成"
                echo ""
                print_info "详细结果:"
                echo "$response" | jq '.results'
            else
                print_warning "部分平仓失败"
                echo "$response" | jq '.'
            fi
        else
            print_error "平仓操作失败"
            echo "$response"
        fi
    else
        print_info "已取消操作"
    fi
}

# 平掉指定trader的持仓
close_trader() {
    if [ -z "$1" ]; then
        print_error "请指定 trader_id"
        echo ""
        print_info "用法: ./start.sh close <trader_id>"
        print_info "示例: ./start.sh close binance_qwen_paper"
        echo ""
        print_info "查看所有 trader_id:"
        curl -s http://localhost:8080/api/traders | jq -r '.[] | "  • \(.trader_id) - \(.trader_name) (\(.ai_model))"'
        exit 1
    fi
    
    trader_id="$1"
    print_warning "⚠️  即将平掉 $trader_id 的所有持仓！"
    echo ""
    read -p "确认操作？(yes/no): " confirm
    if [ "$confirm" == "yes" ]; then
        print_info "正在执行平仓操作..."
        response=$(curl -s -X POST "http://localhost:8080/api/close-positions?trader_id=$trader_id")
        
        if echo "$response" | jq -e '.success' > /dev/null 2>&1; then
            if [ "$(echo "$response" | jq -r '.success')" == "true" ]; then
                print_success "✓ 平仓操作已完成"
            else
                print_error "平仓操作失败"
                echo "$response" | jq '.'
            fi
        else
            print_error "平仓操作失败"
            echo "$response"
        fi
    else
        print_info "已取消操作"
    fi
}

# 查看所有trader的持仓
list_positions() {
    print_info "获取所有 trader 的持仓信息..."
    echo ""
    
    traders=$(curl -s http://localhost:8080/api/traders)
    echo "$traders" | jq -r '.[] | .trader_id' | while read trader_id; do
        trader_name=$(echo "$traders" | jq -r ".[] | select(.trader_id==\"$trader_id\") | .trader_name")
        print_info "[$trader_name ($trader_id)]"
        
        positions=$(curl -s "http://localhost:8080/api/positions?trader_id=$trader_id")
        position_count=$(echo "$positions" | jq 'length')
        
        if [ "$position_count" -gt 0 ]; then
            echo "$positions" | jq -r '.[] | "  • \(.symbol) \(.side) - 数量: \(.quantity) - 盈亏: \(.unrealized_pnl) USDT (\(.unrealized_pnl_pct)%)"'
        else
            echo "  无持仓"
        fi
        echo ""
    done
}

# 更新
update() {
    print_info "正在更新..."
    git pull
    docker-compose up -d --build
    print_success "更新完成"
}

# 显示帮助
show_help() {
    echo "NOFX AI Trading System - Docker 管理脚本"
    echo ""
    echo "用法: ./start.sh [command] [options]"
    echo ""
    echo "命令:"
    echo "  start [--build]    启动服务（可选：重新构建）"
    echo "  stop               停止服务"
    echo "  restart            重启服务"
    echo "  logs [service]     查看日志（可选：指定服务名 backend/frontend）"
    echo "  status             查看服务状态"
    echo "  positions          查看所有持仓"
    echo "  close-all          平掉所有模型的所有持仓"
    echo "  close <trader_id>  平掉指定模型的持仓"
    echo "  clean              清理所有容器和数据"
    echo "  update             更新代码并重启"
    echo "  help               显示此帮助信息"
    echo ""
    echo "示例:"
    echo "  ./start.sh start --build           # 构建并启动"
    echo "  ./start.sh logs backend            # 查看后端日志"
    echo "  ./start.sh status                  # 查看状态"
    echo "  ./start.sh positions               # 查看所有持仓"
    echo "  ./start.sh close-all               # 平掉所有持仓"
    echo "  ./start.sh close binance_qwen_paper  # 平掉指定模型的持仓"
}

# 主函数
main() {
    check_docker

    case "${1:-start}" in
        start)
            check_config
            start "$2"
            ;;
        stop)
            stop
            ;;
        restart)
            restart
            ;;
        logs)
            logs "$@"
            ;;
        status)
            status
            ;;
        positions)
            list_positions
            ;;
        close-all)
            close_all
            ;;
        close)
            close_trader "$2"
            ;;
        clean)
            clean
            ;;
        update)
            update
            ;;
        help|--help|-h)
            show_help
            ;;
        *)
            print_error "未知命令: $1"
            show_help
            exit 1
            ;;
    esac
}

# 运行主函数
main "$@"
