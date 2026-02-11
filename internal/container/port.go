package container

import (
	"fmt"
	"net"
	"strings"
	"sync"
)

// PortPair represents a pair of ports for blue/green deployment
type PortPair struct {
	BluePort  int // Production/traffic port
	GreenPort int // Deployment/staging port
}

// PortManager handles port allocation for services
type PortManager struct {
	start     int
	end       int
	allocated map[string]PortPair // service ID -> port pair
	mu        sync.RWMutex
}

// NewPortManager creates a new port manager
func NewPortManager(start, end int) *PortManager {
	return &PortManager{
		start:     start,
		end:       end,
		allocated: make(map[string]PortPair),
	}
}

// Allocate assigns a pair of ports to a service (blue and green)
// Blue port is the base port, Green port is base + 1
func (pm *PortManager) Allocate(serviceID string) (PortPair, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Check if already allocated
	if pair, exists := pm.allocated[serviceID]; exists {
		return pair, nil
	}

	// Find an available port pair
	// We need TWO consecutive available ports
	for bluePort := pm.start; bluePort <= pm.end-1; bluePort += 2 {
		greenPort := bluePort + 1
		if pm.isPortAvailable(bluePort) && pm.isPortAvailable(greenPort) {
			pair := PortPair{BluePort: bluePort, GreenPort: greenPort}
			pm.allocated[serviceID] = pair
			return pair, nil
		}
	}

	return PortPair{}, fmt.Errorf("no available port pairs in range %d-%d", pm.start, pm.end)
}

// Get retrieves the allocated port pair for a service
func (pm *PortManager) Get(serviceID string) (PortPair, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	pair, exists := pm.allocated[serviceID]
	return pair, exists
}

// Release frees a port allocation
func (pm *PortManager) Release(serviceID string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	delete(pm.allocated, serviceID)
}

// Reserve sets a specific port pair for a service, used for restart recovery.
func (pm *PortManager) Reserve(serviceID string, pair PortPair) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for existingServiceID, existingPair := range pm.allocated {
		if existingServiceID == serviceID {
			continue
		}
		if existingPair.BluePort == pair.BluePort || existingPair.GreenPort == pair.GreenPort ||
			existingPair.BluePort == pair.GreenPort || existingPair.GreenPort == pair.BluePort {
			return fmt.Errorf("port pair conflict with service %s", existingServiceID)
		}
	}

	pm.allocated[serviceID] = pair
	return nil
}

// isPortAvailable checks if a port is not in use and not allocated
func (pm *PortManager) isPortAvailable(port int) bool {
	// Check if already allocated to another service
	for _, pair := range pm.allocated {
		if pair.BluePort == port || pair.GreenPort == port {
			return false
		}
	}

	// Check if port is actually available on the system
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		// Some restricted environments disallow bind/listen entirely.
		// In that case, rely on in-memory allocation tracking.
		if strings.Contains(strings.ToLower(err.Error()), "operation not permitted") {
			return true
		}
		return false
	}
	listener.Close()

	return true
}

// FindAlternativePortPair searches for any available port pair beyond the range
func (pm *PortManager) FindAlternativePortPair() (PortPair, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Search beyond the configured range
	for bluePort := pm.end + 1; bluePort <= pm.end+1000; bluePort += 2 {
		greenPort := bluePort + 1
		if pm.isPortAvailable(bluePort) && pm.isPortAvailable(greenPort) {
			return PortPair{BluePort: bluePort, GreenPort: greenPort}, nil
		}
	}

	return PortPair{}, fmt.Errorf("no available port pairs found")
}

// GetRange returns the configured port range
func (pm *PortManager) GetRange() (int, int) {
	return pm.start, pm.end
}

// GetBluePort returns just the blue (production) port for a service
// Convenience method for backwards compatibility
func (pm *PortManager) GetBluePort(serviceID string) (int, bool) {
	pair, exists := pm.Get(serviceID)
	if !exists {
		return 0, false
	}
	return pair.BluePort, true
}

// GetGreenPort returns just the green (deployment) port for a service
// Convenience method
func (pm *PortManager) GetGreenPort(serviceID string) (int, bool) {
	pair, exists := pm.Get(serviceID)
	if !exists {
		return 0, false
	}
	return pair.GreenPort, true
}
