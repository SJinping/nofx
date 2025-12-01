#!/bin/bash

echo "🔧 开始升级 Go 版本..."

# 卸载旧版本
sudo apt remove golang-go -y
sudo apt autoremove -y

# 下载 Go 1.21.5
cd ~
wget https://go.dev/dl/go1.21.5.linux-amd64.tar.gz

# 安装
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.21.5.linux-amd64.tar.gz

# 配置环境变量
if ! grep -q "/usr/local/go/bin" ~/.bashrc; then
    echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
    echo 'export GOPATH=$HOME/go' >> ~/.bashrc
    echo 'export PATH=$PATH:$GOPATH/bin' >> ~/.bashrc
fi

# 清理
rm go1.21.5.linux-amd64.tar.gz

echo "✅ Go 升级完成!"
echo "请执行: source ~/.bashrc"
echo "然后验证: go version"