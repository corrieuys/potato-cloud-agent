package container

import (
	"fmt"
	"sync"
	"testing"
)

func TestPortManager_Allocate(t *testing.T) {
	t.Logf("Testing port allocation")

	pm := NewPortManager(3000, 3005)

	// Allocate first port pair
	pair1, err := pm.Allocate("service-1")
	if err != nil {
		t.Fatalf("Failed to allocate first port pair: %v", err)
	}
	if pair1.BluePort != 3000 {
		t.Errorf("Expected blue port 3000, got %d", pair1.BluePort)
	}
	if pair1.GreenPort != 3001 {
		t.Errorf("Expected green port 3001, got %d", pair1.GreenPort)
	}
	t.Logf("✓ Allocated port pair: blue=%d, green=%d", pair1.BluePort, pair1.GreenPort)

	// Allocate second port pair
	pair2, err := pm.Allocate("service-2")
	if err != nil {
		t.Fatalf("Failed to allocate second port pair: %v", err)
	}
	if pair2.BluePort != 3002 {
		t.Errorf("Expected blue port 3002, got %d", pair2.BluePort)
	}
	if pair2.GreenPort != 3003 {
		t.Errorf("Expected green port 3003, got %d", pair2.GreenPort)
	}
	t.Logf("✓ Allocated port pair: blue=%d, green=%d", pair2.BluePort, pair2.GreenPort)

	// Try to allocate same service again - should return same pair
	pair1Again, err := pm.Allocate("service-1")
	if err != nil {
		t.Fatalf("Failed to reallocate port pair: %v", err)
	}
	if pair1Again.BluePort != pair1.BluePort {
		t.Errorf("Expected same blue port %d, got %d", pair1.BluePort, pair1Again.BluePort)
	}
	if pair1Again.GreenPort != pair1.GreenPort {
		t.Errorf("Expected same green port %d, got %d", pair1.GreenPort, pair1Again.GreenPort)
	}
	t.Logf("✓ Reallocation returns same port pair")
}

func TestPortManager_Allocate_ExhaustRange(t *testing.T) {
	t.Logf("Testing port exhaustion")

	// Small range - can only fit one port pair (3000, 3001)
	pm := NewPortManager(3000, 3001)

	// Allocate first pair
	_, err := pm.Allocate("service-1")
	if err != nil {
		t.Fatalf("Failed to allocate port pair 1: %v", err)
	}

	// Try to allocate second pair - should fail (no room for 3002, 3003)
	_, err = pm.Allocate("service-2")
	if err == nil {
		t.Error("Expected error when range exhausted, got nil")
	}

	t.Logf("✓ Correctly returned error when range exhausted")
}

func TestPortManager_Release(t *testing.T) {
	t.Logf("Testing port release")

	pm := NewPortManager(3000, 3005)

	// Allocate and release
	pair, err := pm.Allocate("service-1")
	if err != nil {
		t.Fatalf("Failed to allocate port pair: %v", err)
	}

	pm.Release("service-1")

	// Verify port was released by checking Get
	_, exists := pm.Get("service-1")
	if exists {
		t.Error("Port pair should have been released")
	}

	// Allocate again - should get same pair (3000, 3001) since it's first available
	pair2, err := pm.Allocate("service-2")
	if err != nil {
		t.Fatalf("Failed to allocate after release: %v", err)
	}
	if pair2.BluePort != pair.BluePort {
		t.Errorf("Expected released blue port %d, got %d", pair.BluePort, pair2.BluePort)
	}
	if pair2.GreenPort != pair.GreenPort {
		t.Errorf("Expected released green port %d, got %d", pair.GreenPort, pair2.GreenPort)
	}

	t.Logf("✓ Port pair released and reused correctly")
}

func TestPortManager_Get(t *testing.T) {
	t.Logf("Testing port getter")

	pm := NewPortManager(3000, 3100)

	// Get non-existent service
	_, exists := pm.Get("non-existent")
	if exists {
		t.Error("Should not find non-existent service")
	}

	// Allocate and get
	allocatedPair, _ := pm.Allocate("test-service")
	retrievedPair, exists := pm.Get("test-service")

	if !exists {
		t.Error("Should find allocated service")
	}
	if retrievedPair.BluePort != allocatedPair.BluePort {
		t.Errorf("Expected blue port %d, got %d", allocatedPair.BluePort, retrievedPair.BluePort)
	}
	if retrievedPair.GreenPort != allocatedPair.GreenPort {
		t.Errorf("Expected green port %d, got %d", allocatedPair.GreenPort, retrievedPair.GreenPort)
	}

	t.Logf("✓ Get returns correct port pair")
}

func TestPortManager_GetRange(t *testing.T) {
	t.Logf("Testing port range getter")

	pm := NewPortManager(5000, 5100)

	start, end := pm.GetRange()
	if start != 5000 {
		t.Errorf("Expected start 5000, got %d", start)
	}
	if end != 5100 {
		t.Errorf("Expected end 5100, got %d", end)
	}

	t.Logf("✓ Range correctly returned")
}

func TestPortManager_FindAlternativePortPair(t *testing.T) {
	t.Logf("Testing alternative port pair finding")

	pm := NewPortManager(3000, 3001)

	// Use the only available pair
	pm.Allocate("svc-1")

	// Find alternative port pair beyond range
	altPair, err := pm.FindAlternativePortPair()
	if err != nil {
		t.Fatalf("Failed to find alternative port pair: %v", err)
	}

	if altPair.BluePort <= 3001 {
		t.Errorf("Alternative blue port %d should be > 3001", altPair.BluePort)
	}
	if altPair.GreenPort != altPair.BluePort+1 {
		t.Errorf("Alternative green port %d should be blue port + 1", altPair.GreenPort)
	}

	t.Logf("✓ Found alternative port pair: blue=%d, green=%d", altPair.BluePort, altPair.GreenPort)
}

func TestPortManager_ConcurrentAllocation(t *testing.T) {
	t.Logf("Testing concurrent port allocation")

	pm := NewPortManager(3000, 3100)

	var wg sync.WaitGroup
	errors := make(chan error, 10)
	ports := make(chan PortPair, 10)

	// Try to allocate 5 port pairs concurrently
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			pair, err := pm.Allocate(fmt.Sprintf("service-%d", id))
			if err != nil {
				errors <- err
				return
			}
			ports <- pair
		}(i)
	}

	wg.Wait()
	close(errors)
	close(ports)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent allocation error: %v", err)
	}

	// Collect all port pairs and verify no overlap
	portSet := make(map[int]bool)
	for pair := range ports {
		if portSet[pair.BluePort] {
			t.Errorf("Blue port %d allocated twice (duplicate)", pair.BluePort)
		}
		if portSet[pair.GreenPort] {
			t.Errorf("Green port %d allocated twice (duplicate)", pair.GreenPort)
		}
		portSet[pair.BluePort] = true
		portSet[pair.GreenPort] = true
	}

	if len(portSet) != 10 { // 5 services * 2 ports each
		t.Errorf("Expected 10 unique ports, got %d", len(portSet))
	}

	t.Logf("✓ Concurrent allocation successful, %d unique ports allocated", len(portSet))
}

func TestPortManager_GetBluePort(t *testing.T) {
	t.Logf("Testing GetBluePort helper")

	pm := NewPortManager(3000, 3100)
	pm.Allocate("test-service")

	bluePort, exists := pm.GetBluePort("test-service")
	if !exists {
		t.Error("Should find blue port")
	}
	if bluePort != 3000 {
		t.Errorf("Expected blue port 3000, got %d", bluePort)
	}

	t.Logf("✓ GetBluePort returns correct port")
}

func TestPortManager_GetGreenPort(t *testing.T) {
	t.Logf("Testing GetGreenPort helper")

	pm := NewPortManager(3000, 3100)
	pm.Allocate("test-service")

	greenPort, exists := pm.GetGreenPort("test-service")
	if !exists {
		t.Error("Should find green port")
	}
	if greenPort != 3001 {
		t.Errorf("Expected green port 3001, got %d", greenPort)
	}

	t.Logf("✓ GetGreenPort returns correct port")
}
