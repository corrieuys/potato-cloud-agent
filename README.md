# Potato Cloud Agent 🥔

**The deployment agent for Potato Cloud - Simple cloud management, as easy as a potato.**

The Potato Cloud Agent is a lightweight Go application that runs on your Linux servers to deploy and manage containerized applications with zero downtime. It pulls configuration from the Potato Cloud control plane and handles all the complexity of building, deploying, and monitoring your services.

## Table of Contents

- [What This Agent Does](#what-this-agent-does)
- [System Requirements](#system-requirements)
- [Key Features](#key-features)
- [Installation](#installation)
- [Configuration](#configuration)
- [How It Works](#how-it-works)
- [Service Configuration](#service-configuration)
- [CLI Commands](#cli-commands)
- [Using Private GitHub Repositories](#using-private-github-repositories)
- [Managing Secrets Securely](#managing-secrets-securely)
- [Directory Structure](#directory-structure)
- [Port Requirements](#port-requirements)
- [Logs and Monitoring](#logs-and-monitoring)
- [Troubleshooting](#troubleshooting)
- [Security Notes](#security-notes)
- [Development](#development)

## What This Agent Does

The agent is the worker that runs on your server. It:

- **Pulls configuration** from the control plane every 30 seconds
- **Auto-containerizes** your applications by detecting the language and generating Dockerfiles
- **Deploys with zero downtime** using blue/green deployment strategy
- **Monitors health** continuously and reports status back to the dashboard
- **Manages ports** automatically, assigning from a configurable range
- **Handles secrets** securely with local AES-256-GCM encryption
- **Routes traffic** via built-in HTTP proxy
- **Provides internal DNS** for service-to-service communication
- **Cleans up** old Docker images automatically (keeps last 10)

## System Requirements

**Minimum Hardware:**
- Linux server (Ubuntu 20.04+, Debian 11+, or any Linux with systemd)
- ARM64 or AMD64 architecture
- 1GB RAM (2GB+ recommended for multiple services)
- 10GB disk space (20GB+ for Docker)
- Internet connection
- Root/sudo access

**Recommended:**
- Docker 20.10+ installed
- 2GB+ RAM
- 20GB+ disk space

**Required for source builds:**
- GCC/build toolchain (`build-essential` on Debian/Ubuntu)
- CGO enabled (`CGO_ENABLED=1`) because SQLite uses `github.com/mattn/go-sqlite3`

**Perfect For:**
- Raspberry Pi 4B+ ($35-75)
- Old laptops collecting dust (free!)
- Cheap VPS ($3-5/month)
- Any Linux machine in your closet

## Key Features

### 🐳 Docker-First Architecture
- All services run in isolated Docker containers
- No process mode - everything is containerized for security
- Auto-generated Dockerfiles based on detected language
- Support for custom Dockerfiles if present in repo

### 🔄 Blue/Green Deployment
- **Zero downtime** deployments
- **Automatic rollback** if health checks fail
- **60-second health check** timeout
- Only switches traffic after confirming new version is healthy
- Keeps old version running if deployment fails

### 🎯 Auto-Containerization
The agent automatically detects your application's language:

| File Detected | Language | Base Image |
|--------------|----------|------------|
| `package.json` | Node.js | `node:20-alpine` |
| `go.mod` | Go | `golang:1.23-alpine` |
| `requirements.txt` | Python | `python:3.11-slim` |
| `Cargo.toml` | Rust | `rust:1.75-slim` |
| `pom.xml` / `build.gradle` | Java | `eclipse-temurin:21-jre-alpine` |
| None | Generic | `alpine:latest` |

Generated Dockerfiles:
- Use official slim images for small footprint
- Run as non-root user (UID 1000)
- Include health check support
- **Multi-stage builds** for Go and Rust (90% smaller images)
- Single-stage builds for Node.js, Python, and Java (simpler, still efficient)

### 🔌 Automatic Port Management
- Ports assigned from configurable range (default: 3000-3100)
- Each service gets a persistent port
- Automatic port conflict resolution
- Searches beyond range if exhausted

### 🔒 Secure Secret Management
- Secrets stored locally with AES-256-GCM encryption
- Never transmitted to or stored in control plane
- Per-service isolation
- Set via CLI on the agent server only

### 📊 Health Monitoring
- Continuous health checks every 30 seconds
- HTTP endpoint monitoring (if configured)
- Automatic restart on failure
- Status reported to dashboard in real-time

### 🧹 Automatic Cleanup
- Retains only 10 most recent Docker images per service
- Prevents disk space bloat
- Runs automatically after successful deployments

### 🌐 Built-in Proxy & DNS
- External proxy routes HTTP traffic by hostname (Host header)
- Internal DNS for service-to-service communication
- No need for external reverse proxy (nginx/traefik)

## Installation

### Quick Install (Recommended)

Get your agent setup command from the Potato Cloud dashboard, then run:

```bash
curl -fsSL https://your-domain.com/install.sh | sudo bash -s -- --agent-id <AGENT_ID> --stack-id <STACK_ID> --control-plane https://your-control-plane.workers.dev --access-client-id <CF_ACCESS_CLIENT_ID> --access-client-secret <CF_ACCESS_CLIENT_SECRET>
```

The install script will:
- Download prebuilt agent binary
- Write the agent config with Cloudflare Access credentials
- Create necessary directories
- Start as a systemd service

### Cloudflare Access Setup

Potato Cloud uses Cloudflare Access for agent authentication and recommends Cloudflare Tunnel for inbound traffic.

1. Cloudflare Zero Trust → Access → Service Tokens → Create token
2. Add the token to the Access policy protecting the control plane
3. Copy the Client ID and Client Secret for use in the agent setup command

Suggested implementation:
- Protect the control plane with an Access app and a service-token policy
- Use Cloudflare Tunnel for HTTPS ingress to services (no open inbound ports)
- Rotate service tokens periodically and keep them out of logs

**Optional flags:**
- `--version <tag>`: Install specific version
- `--control-plane <url>`: Override control plane URL
- `--stack-id <id>`: Stack ID
- `--force-config`: Rewrite config even if already configured

### Manual Build

```bash
git clone https://github.com/corrieuys/potato-cloud-agent.git
cd potato-cloud-agent
go mod tidy
CGO_ENABLED=1 go build -o potato-cloud-agent ./cmd/agent
sudo mv potato-cloud-agent /usr/local/bin/
```

### Manual Configuration

```bash
sudo potato-cloud-agent \
  -config /etc/potato-cloud/config.json \
  -agent-id <AGENT_ID> \
  -stack-id <STACK_ID> \
  -control-plane https://your-control-plane.workers.dev \
  -access-client-id <CF_ACCESS_CLIENT_ID> \
  -access-client-secret <CF_ACCESS_CLIENT_SECRET>
```

## Configuration

Configuration is stored in `/etc/potato-cloud/config.json`:

```json
{
  "agent_id": "agent-id-from-control-plane",
  "stack_id": "uuid-of-your-stack",
  "control_plane": "https://your-control-plane.workers.dev",
  "access_client_id": "cloudflare-access-client-id",
  "access_client_secret": "cloudflare-access-client-secret",
  "poll_interval": 30,
  "data_dir": "/var/lib/potato-cloud",
  "external_proxy_port": 8080,
  "security_mode": "none",
  "git_ssh_key_dir": "/var/lib/potato-cloud/ssh",
  "verbose_logging": false,
  "port_range_start": 3000,
  "port_range_end": 3100,
  "log_retention": 10000
}
```

### Configuration Options

| Option | Description | Default |
|--------|-------------|---------|
| `agent_id` | Unique agent identifier (from control plane) | - |
| `stack_id` | Stack this agent belongs to | - |
| `control_plane` | Control plane URL | - |
| `access_client_id` | Cloudflare Access client ID | - |
| `access_client_secret` | Cloudflare Access client secret | - |
| `poll_interval` | Config check interval (seconds) | 30 |
| `data_dir` | Data storage directory | `/var/lib/potato-cloud` |
| `external_proxy_port` | HTTP proxy port | 8080 |
| `security_mode` | Firewall mode: "none", "daemon-port", "blocked" | "none" |
| `git_ssh_key_dir` | SSH keys directory | `/var/lib/potato-cloud/ssh` |
| `verbose_logging` | Enable detailed logging | false |
| `port_range_start` | First port to assign | 3000 |
| `port_range_end` | Last port in range | 3100 |
| `log_retention` | Log entries per service | 10000 |

## How It Works

### Auto-Containerization Flow

1. **Language Detection**: Checks repo for language-specific files
2. **Dockerfile Generation**: Creates optimized Dockerfile using templates
   - **Go/Rust**: Multi-stage builds for ~90% smaller images (350MB → 25MB)
   - **Node.js/Python/Java**: Single-stage builds for simplicity
3. **Image Building**: Builds image with tag: `potato-cloud-<service-id>:latest`
4. **Or Uses Existing**: If `Dockerfile` exists in repo root, uses that instead

### Blue/Green Deployment Flow

1. **Build** new Docker image
2. **Allocate Port Pair**: Blue port (external) + Green port (deployment)
3. **Start "green"** container on green port: `potato-cloud-<service-id>-green`
4. **Health Check** (up to 60s):
   - If `health_check_path` set: HTTP GET must return 200-299
   - Uses `health_check_interval` if set on the service (otherwise default interval)
   - If not set: Verify container is running
5. **Success**:
   - Update proxy to route traffic to green port
   - **Graceful shutdown** of blue container (waits for in-flight requests)
   - Stop blue container after connections drain (up to 30s)
   - Rename green → stable service container name (`potato-cloud-<service-id>`)
6. **Failure**: Stop green, keep blue running (rollback)
7. **Cleanup**: Remove old images (keep last 10)

### Graceful Shutdown

When switching traffic from blue to green:

1. **Proxy Update**: Immediately stops sending new requests to blue
2. **Connection Draining**: Waits up to 30 seconds for in-flight requests to complete
3. **Container Stop**: Sends SIGTERM, then SIGKILL if needed
4. **Zero Downtime**: No dropped requests during deployment

The agent uses a fixed 30-second drain window before stopping the previous container version.

**Key Points:**
- Maximum drain time: 30 seconds (configurable via code)
- Long-running requests complete naturally
- No connection resets or 502 errors
- Safe for WebSocket connections and file uploads

### Port Allocation (Two-Port Strategy)

Each service gets **two ports** for blue/green deployment:

```
Service A → Blue: 3000, Green: 3001
Service B → Blue: 3002, Green: 3003
Service C → Blue: 3004, Green: 3005
...
```

**How it works:**
- **Blue Port**: Always the external/public port
- **Green Port**: Used for deployment and health checks
- **Active Port**: Tracks which port is currently serving traffic
- Ports alternate between deployments for zero downtime

**Benefits:**
- True zero-downtime deployments
- No port conflicts during deployment
- Graceful connection draining
- Persistent across restarts
- Automatic range management

### Health Monitoring

After deployment, agent continuously monitors:
- Container status every 30 seconds
- HTTP health checks (if configured)
- Logs failures to database
- Reports to control plane via heartbeats

## Service Configuration

Services are configured via the control plane dashboard. The agent expects this format:

```json
{
  "id": "my-api",
  "name": "My API",
  "git_url": "git@github.com:org/api.git",
  "git_ref": "main",
  "git_commit": "abc123",
  "base_image": "node:20-alpine",
  "language": "nodejs",
  "hostname": "api.example.com",
  "health_check_path": "/health",
  "environment_vars": {
    "NODE_ENV": "production"
  }
}
```

### Service Fields

**Required:**
- `id`: Unique service identifier
- `name`: Human-readable name
- `git_url`: Repository URL (HTTPS or SSH)

**Optional:**
- `git_ref`: Branch/tag (default: "main")
- `git_commit`: Pin to specific commit
- `base_image`: Override default base image
- `language`: Language/runtime ("nodejs", "golang", "python", "rust", "java", "generic", "auto")
- `hostname`: Full domain name for external routing (e.g., "api.example.com")
- `health_check_path`: HTTP path for health checks
- `environment_vars`: Non-sensitive environment variables

**Note:** Set `language` to "auto" to let the agent detect automatically.

## CLI Commands

### Service Status
```bash
# View all services and their status
sudo potato-cloud-agent -status
```

### Service Logs
```bash
# Show last 100 logs for a service
sudo potato-cloud-agent -logs -log-service <service-id>

# Follow logs in real-time (tail -f style)
sudo potato-cloud-agent -logs -log-service <service-id> -f

# List all services
sudo potato-cloud-agent -logs
```

### Agent Management
```bash
# Check agent status
sudo systemctl status potato-cloud-agent

# View agent logs
sudo journalctl -u potato-cloud-agent -f

# Restart agent
sudo systemctl restart potato-cloud-agent

# Stop agent
sudo systemctl stop potato-cloud-agent
```

### SSH Key Management
```bash
# Generate SSH key for git access
sudo potato-cloud-agent -gen-ssh-key -ssh-key-name default

# Add GitHub to known hosts
sudo mkdir -p /var/lib/potato-cloud/ssh
sudo ssh-keyscan github.com | sudo tee -a /var/lib/potato-cloud/ssh/known_hosts
```

### Secret Management
```bash
# Add a secret (interactive)
sudo potato-cloud-agent -add-secret -service <service-id> -secret-name DATABASE_URL

# Add a secret (non-interactive)
sudo potato-cloud-agent -add-secret -service <service-id> -secret-name API_KEY -value "secret"

# List secrets
sudo potato-cloud-agent -list-secrets -service <service-id>

# Delete a secret
sudo potato-cloud-agent -delete-secret -service <service-id> -secret-name API_KEY
```

## Using Private GitHub Repositories

### 1. Generate SSH Key
```bash
sudo potato-cloud-agent -gen-ssh-key -ssh-key-name default
# Copy the public key output
```

### 2. Add to GitHub
1. Go to repository → Settings → Deploy keys
2. Click "Add deploy key"
3. Paste the public key
4. Title: "Potato Cloud Agent"
5. Check "Allow write access" if needed
6. Click "Add key"

### 3. Add GitHub to Known Hosts
```bash
sudo mkdir -p /var/lib/potato-cloud/ssh
sudo ssh-keyscan github.com | sudo tee -a /var/lib/potato-cloud/ssh/known_hosts
```

### 4. Configure Service
In the dashboard:
- Set **Repository URL** to SSH format: `git@github.com:username/repo.git`
- Set **SSH Key Name** to: `default`

## Managing Secrets Securely

**Important:** Never store sensitive values in Environment Variables in the dashboard. Use Secrets instead.

### How It Works
1. Define secret names in dashboard (e.g., `DATABASE_URL`)
2. Set actual values via CLI on agent server
3. Agent injects secrets as environment variables at runtime
4. Secrets encrypted with AES-256-GCM using agent ID as key

### Security Features
- ✅ AES-256-GCM encryption
- ✅ Unique key per agent
- ✅ 0600 file permissions
- ✅ Never leaves agent server
- ✅ Never stored in control plane
- ✅ Per-service isolation

### Example
```bash
# Set database URL
sudo potato-cloud-agent -add-secret -service abc-123 -secret-name DATABASE_URL
# Enter: postgres://user:pass@localhost:5432/db

# Your app can now use:
# const dbUrl = process.env.DATABASE_URL;
```

### Backup Note
Secrets are encrypted with agent-specific keys. To migrate:
1. Set up new server with new agent
2. Manually re-add all secrets
3. Keep secure offline record for disaster recovery

## Directory Structure

```
/var/lib/potato-cloud/
├── state.db              # SQLite database
│   ├── service_processes # Service status and metadata
│   └── service_logs      # Application logs
├── repos/                # Cloned Git repositories
│   └── <service-id>/
│       ├── .git/
│       ├── Dockerfile    # Custom Dockerfile from repo (if provided)
│       ├── Dockerfile.auto # Generated when no Dockerfile exists
│       └── <app-files>
├── secrets/              # Encrypted secrets
│   └── <service-id>/
│       └── <secret-name>.enc
└── ssh/                  # SSH keys for Git
    ├── default
    ├── default.pub
    └── known_hosts
```

## Port Requirements

**Incoming Ports:**
- `8080` (default): External HTTP proxy
- `9090` (default): Agent health check server
- `3000-3100` (default): Service ports (auto-assigned)

**Outgoing:**
- `443`: HTTPS to control plane
- `22`: SSH to Git providers (GitHub, GitLab, etc.)

## Logs and Monitoring

### View Service Logs
```bash
# Last 100 logs
sudo potato-cloud-agent -logs -log-service <service-id>

# Real-time tail
sudo potato-cloud-agent -logs -log-service <service-id> -f
```

### View Agent Logs
```bash
sudo journalctl -u potato-cloud-agent -f
```

### Log Retention
- Default: 10,000 entries per service
- Auto-cleanup when limit exceeded
- Configurable via `log_retention` in config

## Troubleshooting

### Enable Verbose Logging
Edit `/etc/potato-cloud/config.json`:
```json
{
  "verbose_logging": true
}
```
Then restart: `sudo systemctl restart potato-cloud-agent`

### Check Service Status
```bash
sudo potato-cloud-agent -status
```

### Docker Issues
```bash
# Check Docker is running
sudo systemctl status docker

# Check container logs
sudo docker logs potato-cloud-<service-id>

# List all agent containers
sudo docker ps -a | grep potato-cloud
```

### Port Conflicts
```bash
# Check used ports
sudo netstat -tlnp

# Adjust range in config.json
{
  "port_range_start": 4000,
  "port_range_end": 4100
}
```

### Common Issues

**"No available ports"**
- Check what's using ports: `sudo lsof -i :3000-3100`
- Expand port range in config
- Stop unused services

**"Health check failed"**
- Check service logs: `sudo potato-cloud-agent -logs -log-service <id>`
- Verify `health_check_path` is correct
- Check container is actually listening on expected port

**"Build failed"**
- Check Dockerfile exists or language is detected
- Verify Git repository is accessible
- Check Docker daemon is running

## Security Notes

### Container Isolation
- All services run in Docker containers
- No host filesystem access (except copied source)
- Network isolation between services
- Read-only root filesystem where possible

### User Permissions
- Containers run as non-root (UID 1000)
- Agent requires root for Docker and firewall management
- Secrets stored with 0600 permissions

### Network Security
- Services isolated by default
- Only exposed via external proxy or internal DNS
- Firewall rules can restrict access (see `security_mode`)

### Secret Security
- AES-256-GCM encryption
- Keys derived from unique agent ID
- Values never transmitted to control plane
- CLI-only access for setting secrets

## Development

### Running Tests
```bash
# All tests
go test ./...

# With verbose output
go test -v ./internal/container/...
go test -v ./internal/state/...
go test -v ./internal/api/...
go test -v ./internal/service/...

# Specific test
go test -v ./internal/container -run TestDetectLanguage_NodeJS
```

### Test Coverage
- **Container**: Language detection, Dockerfile generation, port management
- **State**: Database operations, log streaming, retention
- **API**: HTTP client, error handling
- **Service**: Blue/green deployment with mocked Docker

All tests use:
- SQLite in-memory (`:memory:`)
- Temporary directories (auto-cleaned)
- Mocked Docker commands

### Building
```bash
# Current platform
go build -o potato-cloud-agent ./cmd/agent

# Linux AMD64
GOOS=linux GOARCH=amd64 go build -o potato-cloud-agent-linux-amd64 ./cmd/agent

# Linux ARM64
GOOS=linux GOARCH=arm64 go build -o potato-cloud-agent-linux-arm64 ./cmd/agent
```

## License

MIT
