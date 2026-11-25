#!/bin/sh
set -eu

APP_NAME=iadu-sim
APP_DIR=/opt/apps/iadu-sim
BIN=/usr/bin/iadu-sim
UNIT=/etc/systemd/system/iadu-sim.service
NO_START=0

for arg in "$@"; do
  case "$arg" in
    --no-start) NO_START=1 ;;
    -h|--help)
      echo "用法: sudo ./install.sh [--no-start]"
      exit 0
      ;;
    *) echo "未知参数: $arg" >&2; exit 2 ;;
  esac
done

if [ "$(id -u)" -ne 0 ]; then
  echo "请使用 root 运行安装脚本（sudo ./install.sh）" >&2
  exit 1
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SRC_BIN="$SCRIPT_DIR/$APP_NAME"
if [ ! -f "$SRC_BIN" ]; then
  echo "未找到二进制: $SRC_BIN" >&2
  exit 1
fi

if ! getent group "$APP_NAME" >/dev/null 2>&1; then
  groupadd --system "$APP_NAME" 2>/dev/null || groupadd -r "$APP_NAME"
fi
if ! id -u "$APP_NAME" >/dev/null 2>&1; then
  useradd --system --gid "$APP_NAME" --home-dir "$APP_DIR" \
    --shell /usr/sbin/nologin --no-create-home "$APP_NAME" 2>/dev/null || \
  useradd -r -g "$APP_NAME" -d "$APP_DIR" -s /sbin/nologin "$APP_NAME"
fi

install -d -m 0755 -o "$APP_NAME" -g "$APP_NAME" "$APP_DIR"
install -d -m 0750 -o "$APP_NAME" -g "$APP_NAME" "$APP_DIR/logs" "$APP_DIR/data"
install -d -m 0755 -o "$APP_NAME" -g "$APP_NAME" "$APP_DIR/run"
install -d -m 0755 -o root -g root "$APP_DIR/config"
install -m 0755 "$SRC_BIN" "$BIN"

if [ ! -e "$APP_DIR/config/config.yaml" ] && [ -f "$SCRIPT_DIR/config.example.yaml" ]; then
  install -m 0640 -o root -g "$APP_NAME" \
    "$SCRIPT_DIR/config.example.yaml" "$APP_DIR/config/config.yaml"
  echo "已安装默认配置: $APP_DIR/config/config.yaml"
fi
if [ -f "$SCRIPT_DIR/README.md" ]; then
  install -m 0644 "$SCRIPT_DIR/README.md" "$APP_DIR/README.md"
fi
if [ -f "$SCRIPT_DIR/$APP_NAME.service" ]; then
  install -m 0644 "$SCRIPT_DIR/$APP_NAME.service" "$UNIT"
fi

if [ -d /run/systemd/system ]; then
  systemctl daemon-reload
  systemctl enable "$APP_NAME.service" >/dev/null 2>&1 || true
  if [ "$NO_START" -eq 0 ]; then
    systemctl restart "$APP_NAME.service" 2>/dev/null || systemctl start "$APP_NAME.service" || true
  fi
else
  echo "未检测到运行中的 systemd；已安装文件，请手动启动 $BIN --config $APP_DIR/config/config.yaml"
fi

cat <<EOF
iadu-sim 安装完成。
  配置文件 : $APP_DIR/config/config.yaml
  运行目录 : $APP_DIR/run （COM1 -> $APP_DIR/run/com1）
  状态文件 : $APP_DIR/data/state.json
  日志目录 : $APP_DIR/logs
  管理页面 : http://127.0.0.1:18090 （默认仅本机；远程访问请修改配置并设置 token）
  采集接入 : 目标程序打开 $APP_DIR/run/com1（9600 8N1 语义，从站地址 1）
EOF
