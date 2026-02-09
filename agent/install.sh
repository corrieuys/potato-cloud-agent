#!/bin/bash
set -e

# BuildVigil Agent Installer
# Usage: curl -fsSL https://your-domain.com/install.sh | bash -s -- --token <install_token>

REPO_URL="https://github.com/buildvigil/agent/releases/latest"
INSTALL_DIR="/opt/buildvigil"
CONFIG_DIR="/etc/buildvigil"
DATA_DIR="/var/lib/buildvigil"
INSTALL_TOKEN=""
CONTROL_PLANE="https://api.buildvigil.com"

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
    *)
      echo "Unknown option: $1"
      exit 1
      ;;
  esac
done

if [ -z "$INSTALL_TOKEN" ]; then
  echo "Error: --token is required"
  echo "Usage: $0 --token <install_token>"
  exit 1
fi

echo "Installing BuildVigil Agent..."

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
BINARY_NAME="buildvigil-agent-${OS}-${ARCH}"
DOWNLOAD_URL="${REPO_URL}/download/${BINARY_NAME}"

# For MVP, we'll build locally or use a placeholder
# In production, this would download from GitHub releases
echo "Note: In production, this would download from: $DOWNLOAD_URL"

# Check if agent binary exists locally (for development)
if [ -f "./buildvigil-agent" ]; then
  echo "Using local agent binary..."
  cp "./buildvigil-agent" "$INSTALL_DIR/buildvigil-agent"
else
  echo "Please build the agent binary first with: cd agent && go build -o buildvigil-agent ./cmd/agent"
  exit 1
fi

chmod +x "$INSTALL_DIR/buildvigil-agent"

# Register the agent
echo "Registering agent with control plane..."
"$INSTALL_DIR/buildvigil-agent" \
  -register "$INSTALL_TOKEN" \
  -control-plane "$CONTROL_PLANE" \
  -config "$CONFIG_DIR/config.json"

# Create systemd service
echo "Creating systemd service..."
cat > /etc/systemd/system/buildvigil-agent.service << 'EOF'
[Unit]
Description=BuildVigil Agent
After=network.target

[Service]
Type=simple
ExecStart=/opt/buildvigil/buildvigil-agent -config /etc/buildvigil/config.json
Restart=always
RestartSec=5
User=root
WorkingDirectory=/var/lib/buildvigil

[Install]
WantedBy=multi-user.target
EOF

# Reload systemd and enable service
systemctl daemon-reload
systemctl enable buildvigil-agent

# Start the service
echo "Starting agent service..."
systemctl start buildvigil-agent

# Check status
if systemctl is-active --quiet buildvigil-agent; then
  echo "✓ BuildVigil Agent installed and running"
  echo "  Config: $CONFIG_DIR/config.json"
  echo "  Data: $DATA_DIR"
  echo "  Logs: journalctl -u buildvigil-agent -f"
else
  echo "✗ Agent failed to start. Check logs with: journalctl -u buildvigil-agent -e"
  exit 1
fi
