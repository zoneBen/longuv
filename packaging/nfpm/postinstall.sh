#!/bin/sh
set -e

USER_NAME=iadu-sim
GROUP_NAME=iadu-sim
APP_DIR=/opt/apps/iadu-sim

if ! getent group "$GROUP_NAME" >/dev/null 2>&1; then
  groupadd --system "$GROUP_NAME" 2>/dev/null || groupadd -r "$GROUP_NAME"
fi
if ! id -u "$USER_NAME" >/dev/null 2>&1; then
  useradd --system --gid "$GROUP_NAME" --home-dir "$APP_DIR" \
    --shell /usr/sbin/nologin --no-create-home "$USER_NAME" 2>/dev/null || \
  useradd -r -g "$GROUP_NAME" -d "$APP_DIR" -s /sbin/nologin "$USER_NAME"
fi

install -d -m 0755 -o "$USER_NAME" -g "$GROUP_NAME" "$APP_DIR"
install -d -m 0750 -o "$USER_NAME" -g "$GROUP_NAME" "$APP_DIR/logs" "$APP_DIR/data"
install -d -m 0755 -o "$USER_NAME" -g "$GROUP_NAME" "$APP_DIR/run"
install -d -m 0755 -o root -g root "$APP_DIR/config"

if [ ! -e "$APP_DIR/config/config.yaml" ] && [ -f /usr/share/iadu-sim/config.example.yaml ]; then
  install -m 0640 -o root -g "$GROUP_NAME" \
    /usr/share/iadu-sim/config.example.yaml "$APP_DIR/config/config.yaml"
fi
if [ -f /usr/share/iadu-sim/README.md ]; then
  install -m 0644 /usr/share/iadu-sim/README.md "$APP_DIR/README.md"
fi

if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
  systemctl enable iadu-sim.service || true
  systemctl restart iadu-sim.service 2>/dev/null || systemctl start iadu-sim.service || true
fi

exit 0
