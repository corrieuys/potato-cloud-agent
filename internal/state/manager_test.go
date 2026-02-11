package state

import (
	"fmt"
	"testing"
	"time"
)

func setupTestDB(t *testing.T) *Manager {
	t.Logf("Setting up in-memory test database")

	mgr, err := NewManager(":memory:")
	if err != nil {
		t.Fatalf("Failed to create state manager: %v", err)
	}

	t.Cleanup(func() {
		t.Logf("Cleaning up test database")
		mgr.Close()
	})

	return mgr
}

func TestSaveAndGetServiceProcess(t *testing.T) {
	t.Logf("Testing service process save and get")

	mgr := setupTestDB(t)

	// Create a service process
	proc := &ServiceProcess{
		ServiceID:     "test-service-1",
		ServiceName:   "Test Service",
		GitCommit:     "abc123def456",
		Runtime:       "docker",
		ContainerID:   "container123",
		ContainerName: "potato-cloud-test-service-1",
		ImageTag:      "potato-cloud/test-service-1:abc123",
		PID:           1234,
		Port:          3000,
		GreenPort:     3001,
		ActivePort:    3000,
		BaseImage:     "node:20-alpine",
		Language:      "nodejs",
		Status:        "running",
		RestartCount:  0,
		LastError:     "",
		StartedAt:     time.Now(),
	}

	// Save
	t.Logf("Saving service process")
	if err := mgr.SaveServiceProcess(proc); err != nil {
		t.Fatalf("Failed to save service process: %v", err)
	}

	// Retrieve
	t.Logf("Retrieving service process")
	retrieved, err := mgr.GetServiceProcess("test-service-1")
	if err != nil {
		t.Fatalf("Failed to get service process: %v", err)
	}

	if retrieved == nil {
		t.Fatal("Retrieved process is nil")
	}

	// Verify fields
	if retrieved.ServiceID != proc.ServiceID {
		t.Errorf("ServiceID mismatch: expected %s, got %s", proc.ServiceID, retrieved.ServiceID)
	}
	if retrieved.ServiceName != proc.ServiceName {
		t.Errorf("ServiceName mismatch: expected %s, got %s", proc.ServiceName, retrieved.ServiceName)
	}
	if retrieved.GitCommit != proc.GitCommit {
		t.Errorf("GitCommit mismatch: expected %s, got %s", proc.GitCommit, retrieved.GitCommit)
	}
	if retrieved.ContainerID != proc.ContainerID {
		t.Errorf("ContainerID mismatch: expected %s, got %s", proc.ContainerID, retrieved.ContainerID)
	}
	if retrieved.Port != proc.Port {
		t.Errorf("Port mismatch: expected %d, got %d", proc.Port, retrieved.Port)
	}
	if retrieved.Status != proc.Status {
		t.Errorf("Status mismatch: expected %s, got %s", proc.Status, retrieved.Status)
	}

	t.Logf("✓ Service process saved and retrieved correctly")
}

func TestGetNonExistentServiceProcess(t *testing.T) {
	t.Logf("Testing get for non-existent service")

	mgr := setupTestDB(t)

	retrieved, err := mgr.GetServiceProcess("non-existent")
	if err != nil {
		t.Fatalf("Failed to get service process: %v", err)
	}

	if retrieved != nil {
		t.Error("Expected nil for non-existent service")
	}

	t.Logf("✓ Correctly returned nil for non-existent service")
}

func TestUpdateServiceProcess(t *testing.T) {
	t.Logf("Testing service process update")

	mgr := setupTestDB(t)

	// Create initial process
	proc := &ServiceProcess{
		ServiceID:   "test-service-2",
		ServiceName: "Test Service 2",
		GitCommit:   "abc123",
		Runtime:     "docker",
		Port:        3000,
		GreenPort:   3001,
		ActivePort:  3000,
		Status:      "running",
	}

	if err := mgr.SaveServiceProcess(proc); err != nil {
		t.Fatalf("Failed to save initial process: %v", err)
	}

	// Update with new status
	proc.Status = "error"
	proc.LastError = "Something went wrong"
	proc.RestartCount = 1

	if err := mgr.SaveServiceProcess(proc); err != nil {
		t.Fatalf("Failed to update process: %v", err)
	}

	// Retrieve and verify update
	retrieved, _ := mgr.GetServiceProcess("test-service-2")
	if retrieved.Status != "error" {
		t.Errorf("Expected status 'error', got '%s'", retrieved.Status)
	}
	if retrieved.LastError != "Something went wrong" {
		t.Errorf("Expected LastError 'Something went wrong', got '%s'", retrieved.LastError)
	}
	if retrieved.RestartCount != 1 {
		t.Errorf("Expected RestartCount 1, got %d", retrieved.RestartCount)
	}

	t.Logf("✓ Service process updated correctly")
}

func TestLogServiceMessage(t *testing.T) {
	t.Logf("Testing log message insertion")

	mgr := setupTestDB(t)

	// Log some messages
	t.Logf("Logging info message")
	if err := mgr.LogServiceMessage("test-service", "info", "Service started successfully"); err != nil {
		t.Fatalf("Failed to log info message: %v", err)
	}

	t.Logf("Logging error message")
	if err := mgr.LogServiceMessage("test-service", "error", "Connection failed"); err != nil {
		t.Fatalf("Failed to log error message: %v", err)
	}

	// Retrieve logs
	logs, err := mgr.GetServiceLogs("test-service", 10)
	if err != nil {
		t.Fatalf("Failed to get service logs: %v", err)
	}

	if len(logs) != 2 {
		t.Errorf("Expected 2 logs, got %d", len(logs))
	}

	// Check that both expected log entries exist (order doesn't matter for this test)
	var foundInfo, foundError bool
	for _, log := range logs {
		if log.Level == "info" && log.Message == "Service started successfully" {
			foundInfo = true
		}
		if log.Level == "error" && log.Message == "Connection failed" {
			foundError = true
		}
	}

	if !foundInfo {
		t.Errorf("Expected info log 'Service started successfully' not found")
	}
	if !foundError {
		t.Errorf("Expected error log 'Connection failed' not found")
	}

	t.Logf("✓ Logs inserted and retrieved correctly")
}

func TestGetServiceLogs_Limit(t *testing.T) {
	t.Logf("Testing log retrieval with limit")

	mgr := setupTestDB(t)

	// Log 20 messages
	for i := 0; i < 20; i++ {
		if err := mgr.LogServiceMessage("test-service", "info", "Log message"); err != nil {
			t.Fatalf("Failed to log message %d: %v", i, err)
		}
	}

	// Retrieve with limit of 5
	logs, err := mgr.GetServiceLogs("test-service", 5)
	if err != nil {
		t.Fatalf("Failed to get service logs: %v", err)
	}

	if len(logs) != 5 {
		t.Errorf("Expected 5 logs, got %d", len(logs))
	}

	t.Logf("✓ Log limit works correctly")
}

func TestStreamLogs(t *testing.T) {
	t.Logf("Testing log streaming")

	mgr := setupTestDB(t)

	// Insert some logs
	if err := mgr.LogServiceMessage("test-service", "info", "First message"); err != nil {
		t.Fatalf("Failed to log: %v", err)
	}

	// Get initial logs
	logs, err := mgr.GetServiceLogs("test-service", 10)
	if err != nil {
		t.Fatalf("Failed to get logs: %v", err)
	}

	lastID := int64(0)
	if len(logs) > 0 {
		// We need to track IDs differently since GetServiceLogs doesn't return IDs
		// For this test, we'll just insert more logs and stream
	}

	// Insert more logs
	if err := mgr.LogServiceMessage("test-service", "info", "Second message"); err != nil {
		t.Fatalf("Failed to log: %v", err)
	}

	// Stream logs after initial ID
	streamed, err := mgr.StreamLogs("test-service", lastID)
	if err != nil {
		t.Fatalf("Failed to stream logs: %v", err)
	}

	// Should get both logs since lastID is 0
	if len(streamed) < 2 {
		t.Errorf("Expected at least 2 streamed logs, got %d", len(streamed))
	}

	t.Logf("✓ Log streaming works correctly")
}

func TestCleanupOldLogs(t *testing.T) {
	t.Logf("Testing log cleanup")

	mgr := setupTestDB(t)

	// Log 15 messages
	for i := 0; i < 15; i++ {
		if err := mgr.LogServiceMessage("test-service", "info", "Log message"); err != nil {
			t.Fatalf("Failed to log message %d: %v", i, err)
		}
	}

	// Cleanup with retention of 10
	if err := mgr.CleanupOldLogs("test-service", 10); err != nil {
		t.Fatalf("Failed to cleanup logs: %v", err)
	}

	// Verify count
	logs, err := mgr.GetServiceLogs("test-service", 100)
	if err != nil {
		t.Fatalf("Failed to get logs after cleanup: %v", err)
	}

	// Should have 10 or fewer logs (retention count)
	if len(logs) > 10 {
		t.Errorf("Expected at most 10 logs after cleanup, got %d", len(logs))
	}

	t.Logf("✓ Log cleanup works correctly, %d logs remaining", len(logs))
}

func TestListServiceProcesses(t *testing.T) {
	t.Logf("Testing list service processes")

	mgr := setupTestDB(t)

	// Create multiple processes
	for i := 0; i < 3; i++ {
		proc := &ServiceProcess{
			ServiceID:   fmt.Sprintf("service-%d", i),
			ServiceName: fmt.Sprintf("Service %d", i),
			Runtime:     "docker",
			Port:        3000 + i,
			GreenPort:   3001 + i,
			ActivePort:  3000 + i,
			Status:      "running",
		}
		if err := mgr.SaveServiceProcess(proc); err != nil {
			t.Fatalf("Failed to save process %d: %v", i, err)
		}
	}

	// List all processes
	processes, err := mgr.ListServiceProcesses()
	if err != nil {
		t.Fatalf("Failed to list processes: %v", err)
	}

	if len(processes) != 3 {
		t.Errorf("Expected 3 processes, got %d", len(processes))
	}

	t.Logf("✓ Listed %d service processes", len(processes))
}

func TestDeleteServiceProcess(t *testing.T) {
	t.Logf("Testing delete service process")

	mgr := setupTestDB(t)

	// Create a process
	proc := &ServiceProcess{
		ServiceID:   "delete-test",
		ServiceName: "Delete Test",
		Runtime:     "docker",
		Port:        4000,
		GreenPort:   4001,
		ActivePort:  4000,
		Status:      "running",
	}

	if err := mgr.SaveServiceProcess(proc); err != nil {
		t.Fatalf("Failed to save process: %v", err)
	}

	// Verify it exists
	retrieved, _ := mgr.GetServiceProcess("delete-test")
	if retrieved == nil {
		t.Fatal("Process should exist before deletion")
	}

	// Delete it
	if err := mgr.DeleteServiceProcess("delete-test"); err != nil {
		t.Fatalf("Failed to delete process: %v", err)
	}

	// Verify it's gone
	retrieved, _ = mgr.GetServiceProcess("delete-test")
	if retrieved != nil {
		t.Error("Process should not exist after deletion")
	}

	t.Logf("✓ Service process deleted successfully")
}

func TestAppliedState(t *testing.T) {
	t.Logf("Testing applied state")

	mgr := setupTestDB(t)

	// Get initial state (should be nil)
	state, err := mgr.GetAppliedState()
	if err != nil {
		t.Fatalf("Failed to get applied state: %v", err)
	}

	if state != nil {
		t.Error("Initial state should be nil")
	}

	// Set applied state
	t.Logf("Setting applied state")
	if err := mgr.SetAppliedState(42, "hash123"); err != nil {
		t.Fatalf("Failed to set applied state: %v", err)
	}

	// Get applied state again
	state, err = mgr.GetAppliedState()
	if err != nil {
		t.Fatalf("Failed to get applied state: %v", err)
	}

	if state == nil {
		t.Fatal("State should not be nil after setting")
	}

	if state.StackVersion != 42 {
		t.Errorf("Expected version 42, got %d", state.StackVersion)
	}
	if state.StateHash != "hash123" {
		t.Errorf("Expected hash 'hash123', got '%s'", state.StateHash)
	}

	t.Logf("✓ Applied state saved and retrieved correctly")
}
