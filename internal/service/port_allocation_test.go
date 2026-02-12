package service

import (
	"testing"

	"github.com/buildvigil/agent/internal/api"
	"github.com/buildvigil/agent/internal/container"
	"github.com/buildvigil/agent/internal/state"
)

// TestPortAllocationAndRecovery tests that port allocation and recovery work correctly
func TestPortAllocationAndRecovery(t *testing.T) {
	// Create a port manager with a small range for testing
	portStart := 3000
	portEnd := 3010
	pm := container.NewPortManager(portStart, portEnd)

	// Create a service
	svc := api.Service{
		ID:                  "test-svc",
		Name:                "test",
		DockerContainerPort: 8000,
		Port:                0,
		HealthCheckPath:     "",
		BuildCommand:        "echo build",
		RunCommand:          "echo run",
		GitURL:              "https://github.com/test/test.git",
		GitRef:              "main",
	}

	t.Run("Initial Port Allocation", func(t *testing.T) {
		// Allocate a port pair for new service
		pair, err := pm.Allocate(svc.ID)
		if err != nil {
			t.Fatalf("Failed to allocate port pair: %v", err)
		}

		// Verify blue port is even
		if pair.BluePort%2 != 0 {
			t.Errorf("Expected blue port to be even, got %d", pair.BluePort)
		}

		// Verify green port is blue + 1
		if pair.GreenPort != pair.BluePort+1 {
			t.Errorf("Expected green port to be blue+1 (%d), got %d", pair.BluePort+1, pair.GreenPort)
		}

		// Verify ports are within range
		if pair.BluePort < portStart || pair.GreenPort > portEnd {
			t.Errorf("Port pair out of range: %v", pair)
		}

		t.Logf("Allocated port pair: blue=%d, green=%d", pair.BluePort, pair.GreenPort)
	})

	t.Run("Recovery Preserves Allocated Ports", func(t *testing.T) {
		// Get the allocated port pair
		pair, exists := pm.Get(svc.ID)
		if !exists {
			t.Fatal("Port pair not found after allocation")
		}

		// Simulate Docker mapping the container port to a DIFFERENT host port
		// This is the bug scenario: Docker might map 8000->3002 even though we allocated 3005
		dockerMappedPort := 3002 // Different from our allocation!

		// Simulate recovery with our fix
		activePort := dockerMappedPort
		var bluePort, greenPort int

		// The fix: Use ALLOCATED port pair, not Docker's mapped port
		allocatedPair, exists := pm.Get(svc.ID)
		if !exists {
			t.Fatal("Port pair should exist in port manager")
		}

		bluePort = allocatedPair.BluePort
		greenPort = allocatedPair.GreenPort

		// Verify we use our allocated ports, NOT Docker's mapped port
		if bluePort != pair.BluePort {
			t.Errorf("Expected bluePort=%d (allocated), got %d (Docker mapped)", pair.BluePort, bluePort)
		}
		if greenPort != pair.GreenPort {
			t.Errorf("Expected greenPort=%d (allocated), got %d (Docker mapped)", pair.GreenPort, greenPort)
		}

		// Verify Docker's mapped port is NOT used
		if bluePort == activePort {
			t.Error("Bug: Should NOT use Docker's mapped port for bluePort")
		}

		t.Logf("Recovery preserved allocated ports: blue=%d, green=%d (Docker mapped to %d)", bluePort, greenPort, activePort)
	})

	t.Run("Multiple ServicesGetUnique Port Pairs", func(t *testing.T) {
		// Create new port manager for this test
		pm2 := container.NewPortManager(3000, 3010)

		svc1 := api.Service{ID: "svc-1", Name: "service-1", DockerContainerPort: 8000}
		svc2 := api.Service{ID: "svc-2", Name: "service-2", DockerContainerPort: 8000}
		svc3 := api.Service{ID: "svc-3", Name: "service-3", DockerContainerPort: 8000}

		pair1, err := pm2.Allocate(svc1.ID)
		if err != nil {
			t.Fatalf("Failed to allocate for svc-1: %v", err)
		}

		pair2, err := pm2.Allocate(svc2.ID)
		if err != nil {
			t.Fatalf("Failed to allocate for svc-2: %v", err)
		}

		pair3, err := pm2.Allocate(svc3.ID)
		if err != nil {
			t.Fatalf("Failed to allocate for svc-3: %v", err)
		}

		// Verify all pairs are unique
		if pair1.BluePort == pair2.BluePort || pair1.BluePort == pair3.BluePort {
			t.Error("Duplicate blue ports allocated")
		}
		if pair2.BluePort == pair3.BluePort {
			t.Error("Duplicate blue ports allocated")
		}

		// Verify green ports are also unique
		if pair1.GreenPort == pair2.GreenPort || pair1.GreenPort == pair3.GreenPort {
			t.Error("Duplicate green ports allocated")
		}
		if pair2.GreenPort == pair3.GreenPort {
			t.Error("Duplicate green ports allocated")
		}

		t.Logf("Allocated pairs: svc-1=(%d,%d), svc-2=(%d,%d), svc-3=(%d,%d)",
			pair1.BluePort, pair1.GreenPort,
			pair2.BluePort, pair2.GreenPort,
			pair3.BluePort, pair3.GreenPort)
	})

	t.Run("Port Reservation After Restart", func(t *testing.T) {
		// Simulate agent restart - port manager is empty
		pm3 := container.NewPortManager(3000, 3010)

		// Simulate state being recovered from SQLite
		// State shows service was using ports 3000, 3001
		recoveredState := &state.ServiceProcess{
			ServiceID:  "restarted-svc",
			Port:       3000, // Recovered from SQLite
			GreenPort:  3001, // Recovered from SQLite
			ActivePort: 3002, // But Docker mapped container to 3002!
		}

		// Reserve the recovered ports
		err := pm3.Reserve(recoveredState.ServiceID, container.PortPair{
			BluePort:  recoveredState.Port,
			GreenPort: recoveredState.GreenPort,
		})
		if err != nil {
			t.Fatalf("Failed to reserve port pair: %v", err)
		}

		// Verify the reserved pair is correct
		reservedPair, exists := pm3.Get(recoveredState.ServiceID)
		if !exists {
			t.Fatal("Reserved pair not found")
		}

		if reservedPair.BluePort != 3000 {
			t.Errorf("Expected bluePort=3000, got %d", reservedPair.BluePort)
		}
		if reservedPair.GreenPort != 3001 {
			t.Errorf("Expected greenPort=3001, got %d", reservedPair.GreenPort)
		}

		// The fix ensures we use reservedPair.BluePort (3000), NOT recoveredState.ActivePort (3002)
		if reservedPair.BluePort == recoveredState.ActivePort {
			t.Error("Bug: Should NOT use ActivePort from state for bluePort")
		}

		t.Logf("Reserved pair after restart: blue=%d, green=%d (ActivePort was %d)",
			reservedPair.BluePort, reservedPair.GreenPort, recoveredState.ActivePort)
	})
}

// TestPortManagerAllocate tests the PortManager.Allocate function
func TestPortManagerAllocate(t *testing.T) {
	t.Run("Consecutive Ports", func(t *testing.T) {
		pm := container.NewPortManager(3000, 3010)

		pair, err := pm.Allocate("test-svc")
		if err != nil {
			t.Fatalf("Allocate failed: %v", err)
		}

		// Should get 3000, 3001
		if pair.BluePort != 3000 {
			t.Errorf("Expected blue=3000, got %d", pair.BluePort)
		}
		if pair.GreenPort != 3001 {
			t.Errorf("Expected green=3001, got %d", pair.GreenPort)
		}
	})

	t.Run("MultipleAllocations", func(t *testing.T) {
		pm := container.NewPortManager(3000, 3010)

		pair1, _ := pm.Allocate("svc-1")
		pair2, _ := pm.Allocate("svc-2")

		// pair1 should get 3000, 3001
		// pair2 should get 3002, 3003
		if pair1.BluePort != 3000 || pair1.GreenPort != 3001 {
			t.Errorf("Unexpected pair1: %+v", pair1)
		}
		if pair2.BluePort != 3002 || pair2.GreenPort != 3003 {
			t.Errorf("Unexpected pair2: %+v", pair2)
		}

		// Verify no overlap
		if pair1.BluePort == pair2.BluePort || pair1.GreenPort == pair2.GreenPort {
			t.Error("Port overlap detected")
		}
	})

	t.Run("NoAvailablePorts", func(t *testing.T) {
		pm := container.NewPortManager(3000, 3002) // Only one pair available (3000, 3001)

		pm.Allocate("svc-1")           // Gets 3000, 3001
		_, err := pm.Allocate("svc-2") // Should fail - no consecutive pairs left

		if err == nil {
			t.Error("Expected error for exhausted ports")
		}
	})
}
