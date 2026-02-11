#!/bin/bash
set -e

# Potato Cloud Agent Uninstaller
# Usage: ./uninstall.sh [--purge] [--stop-containers]
#   --purge            Remove install/config/data directories
#   --stop-containers  Stop all running Docker containers

INSTALL_DIR="/opt/potato-cloud"
CONFIG_DIR="/etc/potato-cloud"
DATA_DIR="/var/lib/potato-cloud"
SERVICE_NAME="potato-cloud-agent"
PURGE="false"
STOP_CONTAINERS="false"

while [[ $# -gt 0 ]]; do
  case $1 in
    --purge)
      PURGE="true"
      shift 1
      ;;
    --stop-containers)
      STOP_CONTAINERS="true"
      shift 1
      ;;
    *)
      echo "Unknown option: $1"
      exit 1
      ;;
  esac
done

if [[ $EUID -ne 0 ]]; then
  echo "Error: run as root (sudo)"
  exit 1
fi

echo "Stopping systemd service (if present)..."
if systemctl list-unit-files | grep -q "^${SERVICE_NAME}\.service"; then
  systemctl stop "$SERVICE_NAME" || true
  systemctl disable "$SERVICE_NAME" || true
  rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
  systemctl daemon-reload
fi

if [[ "$STOP_CONTAINERS" == "true" ]]; then
  if command -v docker >/dev/null 2>&1; then
    echo "Stopping all running Docker containers..."
    RUNNING_IDS=$(docker ps -q)
    if [[ -n "$RUNNING_IDS" ]]; then
      docker stop $RUNNING_IDS
    else
      echo "No running containers found."
    fi
  else
    echo "Docker not found; skipping container stop."
  fi
fi

if [[ "$PURGE" == "true" ]]; then
  echo "Removing agent directories..."
  rm -rf "$INSTALL_DIR" "$CONFIG_DIR" "$DATA_DIR"
fi

echo "Uninstall complete."
if [[ "$PURGE" != "true" ]]; then
  echo "Data preserved. Use --purge to remove agent files and config."
fi
