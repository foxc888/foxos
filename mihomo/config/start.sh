#!/bin/sh

# 将路由修补的逻辑放进后台执行
# 这样不仅不阻塞主程序的启动，还能持续等待 tun0
(
    # 检查并等待 tun0 接口出现
    echo "Waiting for tun0 interface..."
    while ! ip link show tun0 > /dev/null 2>&1; do 
        sleep 1
    done
    echo "tun0 is up."

    # 针对 RouterOS 7.22 修复路由规则优先级
    echo "Adjusting routing rules for RouterOS 7.22..."
    ip rule del pref 2 2>/dev/null
    ip rule add pref 32766 lookup main 2>/dev/null

    # 针对 Telegram 流量增加到 tun0 的路由
    echo "Adding specific routes (like TG) to tun0..."
    ip route replace 198.18.0.0/16 dev tun0 2>/dev/null
    ip route add 149.154.0.0/15 dev tun0 2>/dev/null
    ip route add 91.108.0.0/15 dev tun0 2>/dev/null
    echo "Network initialization complete."
) &

# 使用 exec 启动 mihomo，这样可以将其提升为容器的 PID 1 主进程
# 当您在 RouterOS 点击停止时，mihomo 能直接收到停止信号(SIGTERM)从而优雅退出
echo "Starting mihomo..."
exec /mihomo -d /root/.config/mihomo
