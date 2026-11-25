#!/bin/sh
set -eu

APP_NAME=iadu-sim
APP_DIR=/opt/apps/iadu-sim
BIN=/usr/bin/iadu-sim
UNIT=/etc/systemd/system/iadu-sim.service
PURGE=0

for arg in "$@"; do
  case "$arg" in
    --purge) PURGE=1 ;;
    -h|--help)
      echo "用法: sudo ./uninstall.sh [--purge]"
      echo "  默认卸载程序并保留 $APP_DIR；--purge 同时删除配置/状态/日志和服务用户。"
      exit 0
      ;;
    *) echo "未知参数: $arg" >&2; exit 2 ;;
  esac
done

if [ "$(id -u)" -ne 0 ]; then
  echo "请使用 root 运行卸载脚本（sudo ./uninstall.sh）" >&2
  exit 1
fi

if [ -d /run/systemd/system ]; then
  systemctl stop "$APP_NAME.service" >/dev/null 2>&1 || true
  systemctl disable "$APP_NAME.service" >/dev/null 2>&1 || true
fi

rm -f "$BIN" "$UNIT"

if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi

if [ "$PURGE" -eq 1 ]; then
  if [ -t 0 ]; then
    printf '确认删除 %s 下的全部配置、状态与日志？[y/N] ' "$APP_DIR"
    read -r answer
    case "$answer" in
      y|Y) ;;
      *) echo "已保留 $APP_DIR"; exit 0 ;;
    esac
  fi
  rm -rf "$APP_DIR"
  userdel "$APP_NAME" >/dev/null 2>&1 || true
  groupdel "$APP_NAME" >/dev/null 2>&1 || true
  echo "已删除 $APP_DIR 与服务用户 $APP_NAME"
else
  echo "已卸载程序；配置/状态/日志保留在 $APP_DIR（使用 --purge 可删除）"
fi
