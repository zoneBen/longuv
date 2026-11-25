#!/bin/sh
set -e

# deb: $1=remove|upgrade|failed-upgrade
# rpm: $1=0 (uninstall) | 1 (upgrade)
case "${1:-}" in
  0|remove)
    if [ -d /run/systemd/system ]; then
      systemctl stop iadu-sim.service 2>/dev/null || true
      systemctl disable iadu-sim.service 2>/dev/null || true
    fi
    ;;
esac

exit 0
