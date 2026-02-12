package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const (
	testAgentID            = "agent-123"
	testAccessClientID     = "cf-client-id"
	testAccessClientSecret = "cf-client-secret"
)

func TestGetDesiredState_Success(t *testing.T) {
	t.Logf("Testing GetDesiredState success")

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
				Hostname:        "api.example.com",
				HealthCheckPath: "/health",
				EnvironmentVars: map[string]string{
					"PORT": "3000",
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		if r.URL.Path != "/api/stacks/stack-123/desired-state" {
			t.Errorf("Expected path /api/stacks/stack-123/desired-state, got %s", r.URL.Path)
		}
		if r.Header.Get("X-Agent-Id") != testAgentID {
			t.Errorf("Expected agent header '%s', got '%s'", testAgentID, r.Header.Get("X-Agent-Id"))
		}
		if r.Header.Get("CF-Access-Client-Id") != testAccessClientID {
			t.Errorf("Expected CF Access client ID '%s', got '%s'", testAccessClientID, r.Header.Get("CF-Access-Client-Id"))
		}
		if r.Header.Get("CF-Access-Client-Secret") != testAccessClientSecret {
			t.Errorf("Expected CF Access client secret '%s', got '%s'", testAccessClientSecret, r.Header.Get("CF-Access-Client-Secret"))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(expectedState)
	}))
	defer server.Close()

	client := NewClient(server.URL, testAgentID, testAccessClientID, testAccessClientSecret)

	state, err := client.GetDesiredState("stack-123")
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

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

	client := NewClient(server.URL, testAgentID, testAccessClientID, testAccessClientSecret)

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

	client := NewClient(server.URL, testAgentID, testAccessClientID, testAccessClientSecret)

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
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if r.URL.Path != "/api/agents/heartbeat" {
			t.Errorf("Expected path /api/agents/heartbeat, got %s", r.URL.Path)
		}
		if r.Header.Get("X-Agent-Id") != testAgentID {
			t.Errorf("Expected agent header '%s', got '%s'", testAgentID, r.Header.Get("X-Agent-Id"))
		}
		if r.Header.Get("CF-Access-Client-Id") != testAccessClientID {
			t.Errorf("Expected CF Access client ID '%s', got '%s'", testAccessClientID, r.Header.Get("CF-Access-Client-Id"))
		}
		if r.Header.Get("CF-Access-Client-Secret") != testAccessClientSecret {
			t.Errorf("Expected CF Access client secret '%s', got '%s'", testAccessClientSecret, r.Header.Get("CF-Access-Client-Secret"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected Content-Type 'application/json', got '%s'", r.Header.Get("Content-Type"))
		}

		var hb HeartbeatRequest
		if err := json.NewDecoder(r.Body).Decode(&hb); err != nil {
			t.Errorf("Failed to decode heartbeat: %v", err)
		}
		receivedHeartbeat = &hb

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, testAgentID, testAccessClientID, testAccessClientSecret)

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

	client := NewClient(server.URL, testAgentID, testAccessClientID, testAccessClientSecret)

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
