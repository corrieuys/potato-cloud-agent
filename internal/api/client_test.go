package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetDesiredState_Success(t *testing.T) {
	t.Logf("Testing GetDesiredState success")

	// Create test server
	expectedState := &DesiredState{
		StackID:           "stack-123",
		Version:           42,
		Hash:              "abc123",
		PollInterval:      30,
		SecurityMode:      "none",
		ExternalProxyPort: 8080,
		Services: []Service{
			{
				ID:              "svc-1",
				Name:            "api-gateway",
				GitURL:          "https://github.com/test/api.git",
				GitRef:          "main",
				GitCommit:       "def456",
				Language:        "golang",
				ExternalPath:    "/api",
				HealthCheckPath: "/health",
				EnvironmentVars: map[string]string{
					"PORT": "3000",
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "GET" {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		if r.URL.Path != "/api/stacks/stack-123/desired-state" {
			t.Errorf("Expected path /api/stacks/stack-123/desired-state, got %s", r.URL.Path)
		}
		if r.Header.Get("X-API-Key") != "test-api-key" {
			t.Errorf("Expected API key header 'test-api-key', got '%s'", r.Header.Get("X-API-Key"))
		}

		// Send response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(expectedState)
	}))
	defer server.Close()

	// Create client
	client := NewClient(server.URL, "test-api-key")

	// Call method
	state, err := client.GetDesiredState("stack-123")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify response
	if state.StackID != expectedState.StackID {
		t.Errorf("Expected StackID '%s', got '%s'", expectedState.StackID, state.StackID)
	}
	if state.Version != expectedState.Version {
		t.Errorf("Expected Version %d, got %d", expectedState.Version, state.Version)
	}
	if state.Hash != expectedState.Hash {
		t.Errorf("Expected Hash '%s', got '%s'", expectedState.Hash, state.Hash)
	}
	if len(state.Services) != 1 {
		t.Fatalf("Expected 1 service, got %d", len(state.Services))
	}
	if state.Services[0].ID != expectedState.Services[0].ID {
		t.Errorf("Expected Service ID '%s', got '%s'", expectedState.Services[0].ID, state.Services[0].ID)
	}

	t.Logf("✓ GetDesiredState returned correct data")
}

func TestGetDesiredState_HTTPError(t *testing.T) {
	t.Logf("Testing GetDesiredState HTTP error")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")

	_, err := client.GetDesiredState("stack-123")
	if err == nil {
		t.Fatal("Expected error for HTTP 500, got nil")
	}

	t.Logf("✓ GetDesiredState correctly returned error for HTTP 500")
}

func TestGetDesiredState_InvalidJSON(t *testing.T) {
	t.Logf("Testing GetDesiredState invalid JSON")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")

	_, err := client.GetDesiredState("stack-123")
	if err == nil {
		t.Fatal("Expected error for invalid JSON, got nil")
	}

	t.Logf("✓ GetDesiredState correctly returned error for invalid JSON")
}

func TestSendHeartbeat_Success(t *testing.T) {
	t.Logf("Testing SendHeartbeat success")

	var receivedHeartbeat *HeartbeatRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if r.URL.Path != "/api/agents/heartbeat" {
			t.Errorf("Expected path /api/agents/heartbeat, got %s", r.URL.Path)
		}
		if r.Header.Get("X-API-Key") != "test-api-key" {
			t.Errorf("Expected API key header 'test-api-key', got '%s'", r.Header.Get("X-API-Key"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected Content-Type 'application/json', got '%s'", r.Header.Get("Content-Type"))
		}

		// Decode request body
		var hb HeartbeatRequest
		if err := json.NewDecoder(r.Body).Decode(&hb); err != nil {
			t.Errorf("Failed to decode heartbeat: %v", err)
		}
		receivedHeartbeat = &hb

		// Send response
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")

	req := HeartbeatRequest{
		StackVersion: 42,
		AgentStatus:  "healthy",
		ServicesStatus: []ServiceStatus{
			{
				ServiceID:    "svc-1",
				Name:         "api-gateway",
				Status:       "running",
				HealthStatus: "healthy",
			},
		},
		SecurityState: map[string]interface{}{
			"mode": "none",
		},
		SystemInfo: map[string]interface{}{
			"hostname": "test-host",
		},
	}

	err := client.SendHeartbeat(req)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify received heartbeat
	if receivedHeartbeat == nil {
		t.Fatal("Server did not receive heartbeat")
	}
	if receivedHeartbeat.StackVersion != req.StackVersion {
		t.Errorf("Expected StackVersion %d, got %d", req.StackVersion, receivedHeartbeat.StackVersion)
	}
	if receivedHeartbeat.AgentStatus != req.AgentStatus {
		t.Errorf("Expected AgentStatus '%s', got '%s'", req.AgentStatus, receivedHeartbeat.AgentStatus)
	}
	if len(receivedHeartbeat.ServicesStatus) != 1 {
		t.Errorf("Expected 1 service status, got %d", len(receivedHeartbeat.ServicesStatus))
	}

	t.Logf("✓ SendHeartbeat sent correct data")
}

func TestSendHeartbeat_HTTPError(t *testing.T) {
	t.Logf("Testing SendHeartbeat HTTP error")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("Service Unavailable"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-api-key")

	req := HeartbeatRequest{
		StackVersion: 42,
		AgentStatus:  "healthy",
	}

	err := client.SendHeartbeat(req)
	if err == nil {
		t.Fatal("Expected error for HTTP 503, got nil")
	}

	t.Logf("✓ SendHeartbeat correctly returned error for HTTP 503")
}

func TestRegister_Success(t *testing.T) {
	t.Logf("Testing Register success")

	expectedResponse := &RegistrationResponse{
		AgentID:      "agent-123",
		APIKey:       "new-api-key",
		StackID:      "stack-456",
		PollInterval: 30,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if r.URL.Path != "/api/agents/register" {
			t.Errorf("Expected path /api/agents/register, got %s", r.URL.Path)
		}

		// Decode request
		var req RegistrationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("Failed to decode request: %v", err)
		}
		if req.InstallToken != "install-token-123" {
			t.Errorf("Expected InstallToken 'install-token-123', got '%s'", req.InstallToken)
		}

		// Send response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(expectedResponse)
	}))
	defer server.Close()

	req := RegistrationRequest{
		InstallToken: "install-token-123",
		Hostname:     "test-host",
		IPAddress:    "192.168.1.1",
	}

	resp, err := Register(server.URL, req)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if resp.AgentID != expectedResponse.AgentID {
		t.Errorf("Expected AgentID '%s', got '%s'", expectedResponse.AgentID, resp.AgentID)
	}
	if resp.APIKey != expectedResponse.APIKey {
		t.Errorf("Expected APIKey '%s', got '%s'", expectedResponse.APIKey, resp.APIKey)
	}
	if resp.StackID != expectedResponse.StackID {
		t.Errorf("Expected StackID '%s', got '%s'", expectedResponse.StackID, resp.StackID)
	}

	t.Logf("✓ Register returned correct data")
}

func TestRegister_HTTPError(t *testing.T) {
	t.Logf("Testing Register HTTP error")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Invalid install token"))
	}))
	defer server.Close()

	req := RegistrationRequest{
		InstallToken: "invalid-token",
	}

	_, err := Register(server.URL, req)
	if err == nil {
		t.Fatal("Expected error for HTTP 401, got nil")
	}

	t.Logf("✓ Register correctly returned error for HTTP 401")
}
