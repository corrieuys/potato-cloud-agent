package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/buildvigil/agent/internal/tunnel"
)

// Config holds the agent configuration
type Config struct {
	AgentID           string `json:"agent_id"`
	APIKey            string `json:"api_key"`
	StackID           string `json:"stack_id"`
	ControlPlane      string `json:"control_plane"`
	PollInterval      int    `json:"poll_interval"`
	DataDir           string `json:"data_dir"`
	ExternalProxyPort int    `json:"external_proxy_port"`
	SecurityMode      string `json:"security_mode"`
	GitSSHKeyDir      string `json:"git_ssh_key_dir"`
	// Tunnel configuration
	CloudflareAccountID   string `json:"cloudflare_account_id,omitempty"`
	CloudflareAPIToken    string `json:"cloudflare_api_token,omitempty"`
	CloudflareTunnelID    string `json:"cloudflare_tunnel_id,omitempty"`
	CloudflareTunnelToken string `json:"cloudflare_tunnel_token,omitempty"`
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		ControlPlane: "http://localhost:8787",
		PollInterval: 30,
		DataDir:      "/var/lib/buildvigil",
	}
}

// Load reads configuration from file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return &cfg, nil
}

// Save writes configuration to file
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// ConfigPath returns the default configuration file path
func ConfigPath() string {
	return "/etc/buildvigil/config.json"
}

// StateDBPath returns the path to the SQLite state database
func (c *Config) StateDBPath() string {
	return filepath.Join(c.DataDir, "state.db")
}

// ReposPath returns the path where repositories are cloned
func (c *Config) ReposPath() string {
	return filepath.Join(c.DataDir, "repos")
}

// SSHKeyDir returns the path where SSH keys are stored for git access
func (c *Config) SSHKeyDir() string {
	if c.GitSSHKeyDir != "" {
		return c.GitSSHKeyDir
	}
	return filepath.Join(c.DataDir, "ssh")
}

// SecretsPath returns the path where encrypted secrets are stored
func (c *Config) SecretsPath() string {
	return filepath.Join(c.DataDir, "secrets")
}

// TunnelConfigPath returns the path to the Cloudflare tunnel config
func (c *Config) TunnelConfigPath() string {
	return filepath.Join(c.DataDir, "tunnel.json")
}

// HasCloudflareConfig returns whether Cloudflare configuration is available
func (c *Config) HasCloudflareConfig() bool {
	return c.CloudflareAccountID != "" && c.CloudflareAPIToken != ""
}

// GetCloudflareCredentials returns Cloudflare credentials
func (c *Config) GetCloudflareCredentials() tunnel.CloudflareCredentials {
	return tunnel.CloudflareCredentials{
		AccountID:   c.CloudflareAccountID,
		APIToken:    c.CloudflareAPIToken,
		TunnelID:    c.CloudflareTunnelID,
		TunnelToken: c.CloudflareTunnelToken,
	}
}
