#!/bin/bash
set -e

# Potato Cloud Agent Installer
# Usage: curl -fsSL https://raw.githubusercontent.com/corrieuys/potato-cloud-agent/main/install.sh | bash -s -- --token <install_token> [--version <tag>] [--force-register]

RELEASE_BASE_URL="https://github.com/corrieuys/potato-cloud-agent/releases"
INSTALL_DIR="/opt/potato-cloud"
CONFIG_DIR="/etc/potato-cloud"
DATA_DIR="/var/lib/potato-cloud"
INSTALL_TOKEN=""
CONTROL_PLANE="https://api.potato-cloud.com"
VERSION="latest"
FORCE_REGISTER="false"

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --token)
      INSTALL_TOKEN="$2"
      shift 2
      ;;
    --control-plane)
      CONTROL_PLANE="$2"
      shift 2
      ;;
    --version)
      VERSION="$2"
      shift 2
      ;;
    --force-register)
      FORCE_REGISTER="true"
      shift 1
      ;;
    *)
      echo "Unknown option: $1"
      exit 1
      ;;
  esac
done

if [ -z "$INSTALL_TOKEN" ] && [ ! -f "$CONFIG_DIR/config.json" ]; then
  echo "Error: --token is required"
  echo "Usage: $0 --token <install_token> [--version <tag>]"
  exit 1
fi

echo "Installing Potato Cloud Agent..."

# Detect architecture
ARCH=$(uname -m)
case $ARCH in
  x86_64)
    ARCH="amd64"
    ;;
  aarch64|arm64)
    ARCH="arm64"
    ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
if [ "$OS" != "linux" ]; then
  echo "Unsupported OS: $OS (only Linux is supported)"
  exit 1
fi

# Create directories
echo "Creating directories..."
mkdir -p "$INSTALL_DIR"
mkdir -p "$CONFIG_DIR"
mkdir -p "$DATA_DIR"

# Download agent binary
echo "Downloading agent binary..."
BINARY_NAME="potato-cloud-agent-${OS}-${ARCH}"
if [ "$VERSION" = "latest" ]; then
  DOWNLOAD_URL="${RELEASE_BASE_URL}/latest/download/${BINARY_NAME}"
else
  DOWNLOAD_URL="${RELEASE_BASE_URL}/download/${VERSION}/${BINARY_NAME}"
fi

curl -fL "$DOWNLOAD_URL" -o "$INSTALL_DIR/potato-cloud-agent"
chmod +x "$INSTALL_DIR/potato-cloud-agent"

# Register the agent (skip if config already exists unless forced)
if [ -f "$CONFIG_DIR/config.json" ] && [ "$FORCE_REGISTER" != "true" ]; then
  echo "Config exists at $CONFIG_DIR/config.json; skipping registration."
else
  echo "Registering agent with control plane..."
  "$INSTALL_DIR/potato-cloud-agent" \
    -register "$INSTALL_TOKEN" \
    -control-plane "$CONTROL_PLANE" \
    -config "$CONFIG_DIR/config.json"
fi

# Create systemd service
echo "Creating systemd service..."
cat > /etc/systemd/system/potato-cloud-agent.service << 'EOF'
[Unit]
Description=Potato Cloud Agent
After=network.target

[Service]
Type=simple
ExecStart=/opt/potato-cloud/potato-cloud-agent -config /etc/potato-cloud/config.json
Restart=always
RestartSec=5
User=root
WorkingDirectory=/var/lib/potato-cloud

[Install]
WantedBy=multi-user.target
EOF

# Reload systemd and enable service
systemctl daemon-reload
systemctl enable potato-cloud-agent

# Start the service
echo "Starting agent service..."
systemctl start potato-cloud-agent

# Check status
if systemctl is-active --quiet potato-cloud-agent; then
  echo "✓ Potato Cloud Agent installed and running"
  echo "  Config: $CONFIG_DIR/config.json"
  echo "  Data: $DATA_DIR"
  echo "  Logs: journalctl -u potato-cloud-agent -f"
else
  echo "✗ Agent failed to start. Check logs with: journalctl -u potato-cloud-agent -e"
  exit 1
fi
