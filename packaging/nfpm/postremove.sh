#!/bin/sh
set -e

if [ -d /run/systemd/system ]; then
  systemctl daemon-reload 2>/dev/null || true
fi

# 保留 /opt/apps/iadu-sim 下的配置、状态与日志；需要彻底清理请手动删除。
exit 0
