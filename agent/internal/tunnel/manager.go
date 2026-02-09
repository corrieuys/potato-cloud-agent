package tunnel

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// CloudflareTunnel manages Cloudflare Tunnel connections
type CloudflareTunnel struct {
	tunnelID    string
	tunnelToken string
	accountID   string
	configPath  string
	credentials CloudflareCredentials
	isConnected bool
	process     *exec.Cmd
	httpClient  *http.Client
}

// CloudflareCredentials represents Cloudflare API credentials
type CloudflareCredentials struct {
	AccountID   string `json:"account_id"`
	APIToken    string `json:"api_token"`
	TunnelToken string `json:"tunnel_token,omitempty"`
	TunnelID    string `json:"tunnel_id,omitempty"`
}

// TunnelConfig represents the tunnel configuration
type TunnelConfig struct {
	TunnelID      string        `json:"tunnelId"`
	Ingress       []IngressRule `json:"ingress"`
	OriginRequest OriginRequest `json:"originRequest"`
}

// IngressRule represents a tunnel ingress rule
type IngressRule struct {
	Hostname      string                 `json:"hostname,omitempty"`
	Path          string                 `json:"path,omitempty"`
	Service       string                 `json:"service"`
	OriginRequest map[string]interface{} `json:"originRequest,omitempty"`
}

// OriginRequest represents origin request settings
type OriginRequest struct {
	ConnectTimeout   *int   `json:"connectTimeout,omitempty"`
	TLSTimeout       *int   `json:"tlsTimeout,omitempty"`
	NoTLSVerify      bool   `json:"noTLSVerify,omitempty"`
	HTTPHostHeader   string `json:"httpHostHeader,omitempty"`
	OriginServerName string `json:"originServerName,omitempty"`
}

// NewCloudflareTunnel creates a new Cloudflare tunnel manager
func NewCloudflareTunnel(configPath string, credentials CloudflareCredentials) *CloudflareTunnel {
	return &CloudflareTunnel{
		configPath:  configPath,
		credentials: credentials,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// CreateTunnel creates a new Cloudflare tunnel
func (ct *CloudflareTunnel) CreateTunnel(name, secret string) error {
	if ct.credentials.TunnelID != "" {
		ct.tunnelID = ct.credentials.TunnelID
		return nil
	}

	// Create tunnel via Cloudflare API
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/tunnels", ct.credentials.AccountID)

	payload := map[string]interface{}{
		"name":   name,
		"secret": secret,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal tunnel creation request: %w", err)
	}

	req, err := http.NewRequest("POST", url, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("failed to create tunnel request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+ct.credentials.APIToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := ct.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to create tunnel: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("tunnel creation failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool `json:"success"`
		Result  struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Secret string `json:"secret"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode tunnel response: %w", err)
	}

	if !result.Success {
		return fmt.Errorf("tunnel creation was not successful")
	}

	ct.tunnelID = result.Result.ID
	return nil
}

// GetTunnelToken retrieves the tunnel token
func (ct *CloudflareTunnel) GetTunnelToken() (string, error) {
	if ct.credentials.TunnelToken != "" {
		return ct.credentials.TunnelToken, nil
	}

	if ct.tunnelID == "" {
		return "", fmt.Errorf("tunnel not created")
	}

	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/tunnels/%s/token", ct.credentials.AccountID, ct.tunnelID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+ct.credentials.APIToken)

	resp, err := ct.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get tunnel token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to get tunnel token with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool   `json:"success"`
		Result  string `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	if !result.Success {
		return "", fmt.Errorf("token retrieval was not successful")
	}

	ct.tunnelToken = result.Result
	return ct.tunnelToken, nil
}

// WriteConfig writes the tunnel configuration
func (ct *CloudflareTunnel) WriteConfig(services []ServiceConfig) error {
	if err := os.MkdirAll(filepath.Dir(ct.configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	config := TunnelConfig{
		TunnelID: ct.tunnelID,
		Ingress:  []IngressRule{},
		OriginRequest: OriginRequest{
			ConnectTimeout: intPtr(30),
			TLSTimeout:     intPtr(10),
		},
	}

	// Add service ingress rules
	for _, service := range services {
		if service.Hostname != "" {
			rule := IngressRule{
				Hostname: service.Hostname,
				Service:  fmt.Sprintf("http://localhost:%d", service.Port),
			}
			config.Ingress = append(config.Ingress, rule)
		}
	}

	// Add catch-all rule that blocks traffic
	config.Ingress = append(config.Ingress, IngressRule{
		Service: "http_status:404",
	})

	configData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tunnel config: %w", err)
	}

	if err := os.WriteFile(ct.configPath, configData, 0600); err != nil {
		return fmt.Errorf("failed to write tunnel config: %w", err)
	}

	return nil
}

// WriteCredentials writes the tunnel credentials file
func (ct *CloudflareTunnel) WriteCredentials() error {
	credentialsPath := strings.Replace(ct.configPath, ".json", "-credentials.json", 1)

	credentials := map[string]interface{}{
		"accountTag":   ct.credentials.AccountID,
		"tunnelSecret": ct.tunnelToken,
	}

	credData, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	if err := os.WriteFile(credentialsPath, credData, 0600); err != nil {
		return fmt.Errorf("failed to write credentials file: %w", err)
	}

	return nil
}

// Start starts the Cloudflare tunnel
func (ct *CloudflareTunnel) Start() error {
	if ct.isConnected {
		return fmt.Errorf("tunnel already running")
	}

	if ct.tunnelToken == "" {
		return fmt.Errorf("tunnel token not available")
	}

	// Write credentials file
	if err := ct.WriteCredentials(); err != nil {
		return fmt.Errorf("failed to write credentials: %w", err)
	}

	credentialsPath := strings.Replace(ct.configPath, ".json", "-credentials.json", 1)

	// Start cloudflared process
	args := []string{
		"tunnel",
		"--config", ct.configPath,
		"--credentials-file", credentialsPath,
		"run",
	}

	if ct.tunnelID != "" {
		args = []string{
			"tunnel",
			"--config", ct.configPath,
			"--credentials-file", credentialsPath,
			"--id", ct.tunnelID,
			"run",
		}
	}

	ct.process = exec.Command("cloudflared", args...)
	ct.process.Stdout = os.Stdout
	ct.process.Stderr = os.Stderr

	if err := ct.process.Start(); err != nil {
		return fmt.Errorf("failed to start cloudflared: %w", err)
	}

	// Wait a moment to check if it started successfully
	time.Sleep(2 * time.Second)
	if ct.process.Process != nil {
		if err := ct.process.Process.Signal(syscall.Signal(0)); err != nil {
			return fmt.Errorf("cloudflared process failed to start")
		}
	}

	ct.isConnected = true
	return nil
}

// Stop stops the Cloudflare tunnel
func (ct *CloudflareTunnel) Stop() error {
	if !ct.isConnected || ct.process == nil {
		return nil
	}

	if ct.process.Process != nil {
		if err := ct.process.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill cloudflared process: %w", err)
		}
		ct.process.Wait()
	}

	ct.isConnected = false
	ct.process = nil
	return nil
}

// IsConnected returns whether the tunnel is currently connected
func (ct *CloudflareTunnel) IsConnected() bool {
	return ct.isConnected && ct.process != nil && ct.process.Process != nil
}

// GetStatus returns the current tunnel status
func (ct *CloudflareTunnel) GetStatus() map[string]interface{} {
	status := map[string]interface{}{
		"connected":   ct.IsConnected(),
		"tunnel_id":   ct.tunnelID,
		"config_path": ct.configPath,
	}

	if ct.process != nil && ct.process.Process != nil {
		status["pid"] = ct.process.Process.Pid
	}

	return status
}

// ServiceConfig represents a service to be exposed through the tunnel
type ServiceConfig struct {
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Hostname string `json:"hostname"`
}

// Helper function for pointers
func intPtr(i int) *int {
	return &i
}

// IsCloudflaredAvailable checks if cloudflared is available
func IsCloudflaredAvailable() bool {
	_, err := exec.LookPath("cloudflared")
	return err == nil
}
