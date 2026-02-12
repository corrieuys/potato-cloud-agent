package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client communicates with the control plane
type Client struct {
	baseURL            string
	agentID            string
	accessClientID     string
	accessClientSecret string
	httpClient         *http.Client
}

// NewClient creates a new API client
func NewClient(baseURL, agentID, accessClientID, accessClientSecret string) *Client {
	return &Client{
		baseURL:            baseURL,
		agentID:            agentID,
		accessClientID:     accessClientID,
		accessClientSecret: accessClientSecret,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) setAccessHeaders(req *http.Request) {
	if c.agentID != "" {
		req.Header.Set("X-Agent-Id", c.agentID)
	}
	if c.accessClientID != "" {
		req.Header.Set("CF-Access-Client-Id", c.accessClientID)
	}
	if c.accessClientSecret != "" {
		req.Header.Set("CF-Access-Client-Secret", c.accessClientSecret)
	}
}

// Language constants
const (
	LangBun     = "bun"
	LangNodeJS  = "nodejs"
	LangGo      = "golang"
	LangPython  = "python"
	LangRust    = "rust"
	LangJava    = "java"
	LangGeneric = "generic"
	LangAuto    = "auto"
)

// Service represents a service in the desired state
type Service struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	ServiceType         string            `json:"service_type"`
	GitURL              string            `json:"git_url"`
	GitRef              string            `json:"git_ref"`
	GitCommit           string            `json:"git_commit"`
	GitSSHKey           string            `json:"git_ssh_key"`
	DockerImage         string            `json:"docker_image"`
	DockerRunArgs       string            `json:"docker_run_args"`
	BuildCommand        string            `json:"build_command"`
	RunCommand          string            `json:"run_command"`
	Runtime             string            `json:"runtime"`
	DockerfilePath      string            `json:"dockerfile_path"`
	DockerContext       string            `json:"docker_context"`
	DockerContainerPort int               `json:"docker_container_port"`
	ImageRetainCount    int               `json:"image_retain_count"`
	BaseImage           string            `json:"base_image"` // Optional: override default base image
	Language            string            `json:"language"`   // Language/runtime: nodejs, golang, python, rust, java, generic, auto
	Port                int               `json:"port"`
	Hostname            string            `json:"hostname"`
	HealthCheckPath     string            `json:"health_check_path"`
	HealthCheckInterval int               `json:"health_check_interval"` // Defaults to global config
	EnvironmentVars     map[string]string `json:"environment_vars"`
}

// DesiredState represents the full desired state from the control plane
type DesiredState struct {
	StackID           string    `json:"stack_id"`
	Version           int       `json:"version"`
	Hash              string    `json:"hash"`
	PollInterval      int       `json:"poll_interval"`
	HeartbeatInterval int       `json:"heartbeat_interval"`
	SecurityMode      string    `json:"security_mode"`
	ExternalProxyPort int       `json:"external_proxy_port"`
	Services          []Service `json:"services"`
}

// GetDesiredState fetches the desired state from the control plane
func (c *Client) GetDesiredState(stackID string) (*DesiredState, error) {
	url := fmt.Sprintf("%s/api/stacks/%s/desired-state", c.baseURL, stackID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAccessHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch desired state: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var state DesiredState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &state, nil
}

// HeartbeatRequest represents a heartbeat payload
type HeartbeatRequest struct {
	StackVersion   int                    `json:"stack_version"`
	AgentStatus    string                 `json:"agent_status"`
	ServicesStatus []ServiceStatus        `json:"services_status"`
	SecurityState  map[string]interface{} `json:"security_state"`
	SystemInfo     map[string]interface{} `json:"system_info"`
}

// ServiceStatus represents the status of a running service
type ServiceStatus struct {
	ServiceID    string `json:"service_id"`
	Name         string `json:"name"`
	Status       string `json:"status"` // "running", "stopped", "error", "building"
	PID          int    `json:"pid,omitempty"`
	RestartCount int    `json:"restart_count"`
	LastError    string `json:"last_error,omitempty"`
	HealthStatus string `json:"health_status,omitempty"`
}

// SendHeartbeat sends a heartbeat to the control plane
func (c *Client) SendHeartbeat(req HeartbeatRequest) error {
	url := fmt.Sprintf("%s/api/agents/heartbeat", c.baseURL)

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal heartbeat: %w", err)
	}

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	c.setAccessHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send heartbeat: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("heartbeat failed with status: %d", resp.StatusCode)
	}

	return nil
}
