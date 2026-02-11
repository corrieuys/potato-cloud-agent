package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/buildvigil/agent/internal/tunnel"
)

// Config holds the agent configuration.
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

	VerboseLogging bool `json:"verbose_logging"`
	PortRangeStart int  `json:"port_range_start"`
	PortRangeEnd   int  `json:"port_range_end"`
	LogRetention   int  `json:"log_retention"`

	StackNetworkPrefix string `json:"stack_network_prefix"`
	StackNetworkSubnet string `json:"stack_network_subnet"`

	CloudflareAccountID   string `json:"cloudflare_account_id,omitempty"`
	CloudflareAPIToken    string `json:"cloudflare_api_token,omitempty"`
	CloudflareTunnelID    string `json:"cloudflare_tunnel_id,omitempty"`
	CloudflareTunnelToken string `json:"cloudflare_tunnel_token,omitempty"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		ControlPlane:       "http://localhost:8787",
		PollInterval:       30,
		DataDir:            "/var/lib/potato-cloud",
		ExternalProxyPort:  8080,
		SecurityMode:       "none",
		VerboseLogging:     false,
		PortRangeStart:     3000,
		PortRangeEnd:       3100,
		LogRetention:       10000,
		StackNetworkPrefix: "stack-",
		StackNetworkSubnet: "172.20.0.0/16",
	}
}

// Load reads configuration from file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return cfg, nil
}

// Save writes configuration to file.
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// ConfigPath returns the default configuration file path.
func ConfigPath() string {
	return "/etc/potato-cloud/config.json"
}

// StateDBPath returns the path to the SQLite state database.
func (c *Config) StateDBPath() string {
	return filepath.Join(c.DataDir, "state.db")
}

// ReposPath returns the path where repositories are cloned.
func (c *Config) ReposPath() string {
	return filepath.Join(c.DataDir, "repos")
}

// SSHKeyDir returns the path where SSH keys are stored for git access.
func (c *Config) SSHKeyDir() string {
	if c.GitSSHKeyDir != "" {
		return c.GitSSHKeyDir
	}
	return filepath.Join(c.DataDir, "ssh")
}

// SecretsPath returns the path where encrypted secrets are stored.
func (c *Config) SecretsPath() string {
	return filepath.Join(c.DataDir, "secrets")
}

// TunnelConfigPath returns the path to the Cloudflare tunnel config.
func (c *Config) TunnelConfigPath() string {
	return filepath.Join(c.DataDir, "tunnel.json")
}

// HasCloudflareConfig returns whether Cloudflare configuration is available.
func (c *Config) HasCloudflareConfig() bool {
	return c.CloudflareAccountID != "" && c.CloudflareAPIToken != ""
}

// GetCloudflareCredentials returns Cloudflare credentials.
func (c *Config) GetCloudflareCredentials() tunnel.CloudflareCredentials {
	return tunnel.CloudflareCredentials{
		AccountID:   c.CloudflareAccountID,
		APIToken:    c.CloudflareAPIToken,
		TunnelID:    c.CloudflareTunnelID,
		TunnelToken: c.CloudflareTunnelToken,
	}
}

// GetStackNetworkConfig returns the stack network configuration.
func (c *Config) GetStackNetworkConfig() (string, string) {
	return c.StackNetworkPrefix, c.StackNetworkSubnet
}

// SetStackNetworkConfig updates the stack network configuration.
func (c *Config) SetStackNetworkConfig(prefix, subnet string) {
	c.StackNetworkPrefix = prefix
	c.StackNetworkSubnet = subnet
}

// GetNetworkName returns the network name for a stack.
func (c *Config) GetNetworkName(stackID string) string {
	return fmt.Sprintf("%s%s-network", c.StackNetworkPrefix, stackID)
}

// GetNetworkSubnet returns the subnet for a stack.
func (c *Config) GetNetworkSubnet(stackID string) string {
	hash := uint32(0)
	for _, r := range stackID {
		hash = hash*31 + uint32(r)
	}

	parts := strings.Split(c.StackNetworkSubnet, "/")
	if len(parts) != 2 {
		return c.StackNetworkSubnet
	}

	octets := strings.Split(parts[0], ".")
	if len(octets) != 4 {
		return c.StackNetworkSubnet
	}

	return fmt.Sprintf("%s.%s.%d.0/%s", octets[0], octets[1], hash%256, parts[1])
}

// ValidateStackNetworkConfig validates the stack network configuration.
func (c *Config) ValidateStackNetworkConfig() error {
	if len(c.StackNetworkPrefix) < 2 {
		return fmt.Errorf("stack network prefix must be at least 2 characters")
	}
	if c.StackNetworkSubnet == "" {
		return fmt.Errorf("stack network subnet cannot be empty")
	}

	parts := strings.Split(c.StackNetworkSubnet, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid subnet format: %s", c.StackNetworkSubnet)
	}

	mask, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("invalid subnet mask: %s", parts[1])
	}
	if mask < 16 || mask > 30 {
		return fmt.Errorf("subnet mask must be between 16 and 30")
	}

	octets := strings.Split(parts[0], ".")
	if len(octets) != 4 {
		return fmt.Errorf("invalid IP address in subnet: %s", parts[0])
	}
	if octets[2] != "0" {
		return fmt.Errorf("third octet must be 0 for subnet base")
	}

	return nil
}

// ResetStackNetworkConfig resets stack network configuration to defaults.
func (c *Config) ResetStackNetworkConfig() {
	c.StackNetworkPrefix = "stack-"
	c.StackNetworkSubnet = "172.20.0.0/16"
}
