package container

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// NetworkResource is a lightweight network model returned by ListStackNetworks.
type NetworkResource struct {
	ID     string
	Name   string
	Driver string
	Scope  string
}

// StackNetworkManager handles Docker network management for stack isolation.
type StackNetworkManager struct{}

// NewStackNetworkManager creates a new StackNetworkManager.
func NewStackNetworkManager() (*StackNetworkManager, error) {
	return &StackNetworkManager{}, nil
}

// CreateStackNetwork creates a dedicated Docker network for a stack.
func (m *StackNetworkManager) CreateStackNetwork(stackID string) error {
	networkName := getStackNetworkName(stackID)

	if m.networkExists(networkName) {
		return nil
	}

	hash := hashStackID(stackID) % 256
	subnet := fmt.Sprintf("172.%d.0.0/16", hash)
	gateway := fmt.Sprintf("172.%d.0.1", hash)

	_, err := runDockerWithTimeout(
		30*time.Second,
		"network", "create",
		"--driver", "bridge",
		"--subnet", subnet,
		"--gateway", gateway,
		networkName,
	)
	if err != nil {
		return fmt.Errorf("failed to create network %s: %w", networkName, err)
	}

	log.Printf("Created network %s for stack %s", networkName, stackID)
	return nil
}

// DeleteStackNetwork deletes the dedicated Docker network for a stack.
func (m *StackNetworkManager) DeleteStackNetwork(stackID string) error {
	networkName := getStackNetworkName(stackID)
	if !m.networkExists(networkName) {
		return nil
	}

	if err := m.DisconnectAllFromStackNetwork(stackID); err != nil {
		return fmt.Errorf("failed to disconnect containers before deleting network: %w", err)
	}

	_, err := runDockerWithTimeout(30*time.Second, "network", "rm", networkName)
	if err != nil {
		return fmt.Errorf("failed to delete network %s: %w", networkName, err)
	}

	log.Printf("Deleted network %s for stack %s", networkName, stackID)
	return nil
}

// ConnectContainerToStackNetwork connects a container to the stack's dedicated network.
func (m *StackNetworkManager) ConnectContainerToStackNetwork(stackID, containerID string) error {
	networkName := getStackNetworkName(stackID)
	if !m.networkExists(networkName) {
		return fmt.Errorf("network %s not found", networkName)
	}

	_, err := runDockerWithTimeout(30*time.Second, "network", "connect", networkName, containerID)
	if err != nil {
		return fmt.Errorf("failed to connect container %s to network %s: %w", containerID, networkName, err)
	}

	log.Printf("Connected container %s to network %s", containerID, networkName)
	return nil
}

// DisconnectContainerFromStackNetwork disconnects a container from the stack's network.
func (m *StackNetworkManager) DisconnectContainerFromStackNetwork(stackID, containerID string) error {
	networkName := getStackNetworkName(stackID)
	if !m.networkExists(networkName) {
		return nil
	}

	_, err := runDockerWithTimeout(30*time.Second, "network", "disconnect", networkName, containerID)
	if err != nil {
		// Treat "not connected" and "not found" as idempotent.
		errMsg := err.Error()
		if strings.Contains(errMsg, "not connected") || strings.Contains(errMsg, "No such") {
			return nil
		}
		return fmt.Errorf("failed to disconnect container %s from network %s: %w", containerID, networkName, err)
	}

	log.Printf("Disconnected container %s from network %s", containerID, networkName)
	return nil
}

// DisconnectAllFromStackNetwork disconnects all containers from a stack's network.
func (m *StackNetworkManager) DisconnectAllFromStackNetwork(stackID string) error {
	networkName := getStackNetworkName(stackID)
	if !m.networkExists(networkName) {
		return nil
	}

	output, err := runDockerWithTimeout(
		30*time.Second,
		"network", "inspect",
		"--format", "{{range $id, $_ := .Containers}}{{println $id}}{{end}}",
		networkName,
	)
	if err != nil {
		return fmt.Errorf("failed to inspect network %s: %w", networkName, err)
	}

	for _, containerID := range strings.Split(strings.TrimSpace(output), "\n") {
		containerID = strings.TrimSpace(containerID)
		if containerID == "" {
			continue
		}
		if derr := m.DisconnectContainerFromStackNetwork(stackID, containerID); derr != nil {
			log.Printf("Warning: failed to disconnect container %s from network %s: %v", containerID, networkName, derr)
		}
	}

	return nil
}

// ListStackNetworks returns all networks created for stacks.
func (m *StackNetworkManager) ListStackNetworks() ([]NetworkResource, error) {
	output, err := runDockerWithTimeout(
		30*time.Second,
		"network", "ls",
		"--format", "{{.ID}}|{{.Name}}|{{.Driver}}|{{.Scope}}",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list networks: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	networks := make([]NetworkResource, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 4)
		if len(parts) != 4 {
			continue
		}
		if !strings.HasPrefix(parts[1], "stack-") || !strings.HasSuffix(parts[1], "-network") {
			continue
		}

		networks = append(networks, NetworkResource{
			ID:     parts[0],
			Name:   parts[1],
			Driver: parts[2],
			Scope:  parts[3],
		})
	}

	return networks, nil
}

func (m *StackNetworkManager) networkExists(name string) bool {
	_, err := runDockerWithTimeout(10*time.Second, "network", "inspect", name)
	return err == nil
}

func getStackNetworkName(stackID string) string {
	return fmt.Sprintf("stack-%s-network", stackID)
}

func runDockerWithTimeout(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	if err != nil {
		if output == "" {
			return "", err
		}
		return "", fmt.Errorf("%w: %s", err, output)
	}

	return output, nil
}

// hashStackID generates a consistent hash for stack IDs to create unique subnets.
func hashStackID(stackID string) uint32 {
	hash := uint32(0)
	for _, c := range stackID {
		hash = hash*31 + uint32(c)
	}
	return hash
}
