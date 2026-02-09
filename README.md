# Potato Cloud Agent

The Potato Cloud Agent is a lightweight Go application that runs on Linux servers to deploy and manage applications.

## Features

- **Git-based deployments**: Clone repositories and checkout specific commits
- **Process supervision**: Automatically restart crashed services
- **State synchronization**: Poll control plane for configuration changes
- **Health reporting**: Send heartbeats to dashboard
- **Local state persistence**: SQLite database for tracking service status

## Installation

### Quick Install

```bash
curl -fsSL https://your-domain.com/install.sh | bash -s -- --token <install_token>
```

### Manual Build

```bash
cd agent
go mod tidy
go build -o potato-cloud-agent ./cmd/agent
```

### Registration

```bash
sudo ./potato-cloud-agent -register <install_token> -control-plane https://api.potato-cloud.com
```

## Running

### As a systemd service (recommended):
```bash
sudo systemctl start potato-cloud-agent
sudo systemctl enable potato-cloud-agent
sudo journalctl -u potato-cloud-agent -f
```

### Manually:
```bash
sudo ./potato-cloud-agent -config /etc/potato-cloud/config.json
```

## Configuration

The agent stores configuration in `/etc/potato-cloud/config.json`:

```json
{
  "agent_id": "uuid",
  "api_key": "secret",
  "stack_id": "uuid",
  "control_plane": "https://api.potato-cloud.com",
  "poll_interval": 30,
  "data_dir": "/var/lib/potato-cloud",
  "git_ssh_key_dir": "/var/lib/potato-cloud/ssh"
}
```

## SSH Deploy Keys (Out-of-Band)

The agent can generate SSH keys locally. The control plane never receives private keys.

### Generate a key

```bash
sudo ./potato-cloud-agent -gen-ssh-key -ssh-key-name default
```

Add the printed public key in GitHub as a Deploy Key (read-only recommended).

### Add GitHub host key

```bash
sudo mkdir -p /var/lib/potato-cloud/ssh
sudo ssh-keyscan github.com | sudo tee -a /var/lib/potato-cloud/ssh/known_hosts
```

### Use SSH URLs in services

Set `git_url` to `git@github.com:org/repo.git` and `git_ssh_key` to the key name (e.g., `default`).

## Directory Structure

```
/var/lib/potato-cloud/
├── state.db          # SQLite database
└── repos/            # Cloned repositories
    └── <service-id>/
```

## Development

### Running Tests
```bash
go test ./...
```

### Building for different architectures
```bash
# Linux AMD64
GOOS=linux GOARCH=amd64 go build -o potato-cloud-agent-linux-amd64 ./cmd/agent

# Linux ARM64
GOOS=linux GOARCH=arm64 go build -o potato-cloud-agent-linux-arm64 ./cmd/agent
```
