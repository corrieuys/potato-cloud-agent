package service

import (
	"fmt"
	"sync"
	"time"
)

// MockDockerClient is a mock implementation of DockerClient for testing
type MockDockerClient struct {
	mu                sync.RWMutex
	containers        map[string]bool // container name -> is running
	images            map[string][]ImageInfo
	BuildImageFunc    func(repoPath, dockerfilePath, imageTag string) error
	RunContainerFunc  func(imageTag, containerName string, port int, envVars, secrets map[string]string) (string, error)
	HealthCheckResult bool // Controls whether health checks pass or fail
	BuildShouldFail   bool
	RunShouldFail     bool
	RenameShouldFail  bool
}

// NewMockDockerClient creates a new mock Docker client
func NewMockDockerClient() *MockDockerClient {
	return &MockDockerClient{
		containers:        make(map[string]bool),
		images:            make(map[string][]ImageInfo),
		HealthCheckResult: true,
	}
}

func (m *MockDockerClient) BuildImage(repoPath, dockerfilePath, imageTag string) error {
	if m.BuildShouldFail {
		return fmt.Errorf("mock build failed")
	}
	if m.BuildImageFunc != nil {
		return m.BuildImageFunc(repoPath, dockerfilePath, imageTag)
	}

	// Simulate build time
	time.Sleep(10 * time.Millisecond)

	// Store the image
	m.mu.Lock()
	defer m.mu.Unlock()
	parts := splitImageTag(imageTag)
	if parts != nil {
		m.images[parts.serviceID] = append(m.images[parts.serviceID], ImageInfo{
			Tag:       imageTag,
			ID:        fmt.Sprintf("mock-id-%d", time.Now().UnixNano()),
			CreatedAt: time.Now().Format("2006-01-02 15:04:05 -0700 MST"),
		})
	}

	return nil
}

func (m *MockDockerClient) RunContainer(imageTag, containerName string, port int, envVars, secrets map[string]string) (string, error) {
	if m.RunShouldFail {
		return "", fmt.Errorf("mock run failed")
	}
	if m.RunContainerFunc != nil {
		return m.RunContainerFunc(imageTag, containerName, port, envVars, secrets)
	}

	// Simulate start time
	time.Sleep(5 * time.Millisecond)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.containers[containerName] = true

	return fmt.Sprintf("mock-container-id-%d", time.Now().UnixNano()), nil
}

func (m *MockDockerClient) StopContainer(containerName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.containers, containerName)
	return nil
}

func (m *MockDockerClient) RenameContainer(oldName, newName string) error {
	if m.RenameShouldFail {
		return fmt.Errorf("mock rename failed")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop new name if exists
	delete(m.containers, newName)

	// Transfer running state
	if running, exists := m.containers[oldName]; exists {
		delete(m.containers, oldName)
		m.containers[newName] = running
	}

	return nil
}

func (m *MockDockerClient) GetContainerStatus(containerName string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if running, exists := m.containers[containerName]; exists {
		if running {
			return "running", nil
		}
		return "stopped", nil
	}
	return "stopped", nil
}

func (m *MockDockerClient) ContainerExists(containerName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.containers[containerName]
	return exists
}

func (m *MockDockerClient) ListImages(serviceID string) ([]ImageInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.images[serviceID], nil
}

func (m *MockDockerClient) RemoveImage(imageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find and remove image
	for serviceID, images := range m.images {
		for i, img := range images {
			if img.ID == imageID {
				m.images[serviceID] = append(images[:i], images[i+1:]...)
				return nil
			}
		}
	}

	return nil
}

// IsContainerRunning returns true if the container is marked as running
func (m *MockDockerClient) IsContainerRunning(containerName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.containers[containerName]
}

// SetContainerRunning sets the running state of a container (for simulating health checks)
func (m *MockDockerClient) SetContainerRunning(containerName string, running bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.containers[containerName] = running
}

// Helper to split image tag
type imageTagParts struct {
	prefix    string
	serviceID string
	commit    string
}

func splitImageTag(imageTag string) *imageTagParts {
	// Format: potato-cloud/service-id:commit
	parts := make([]string, 0)
	current := ""
	for _, char := range imageTag {
		if char == '/' || char == ':' {
			parts = append(parts, current)
			current = ""
		} else {
			current += string(char)
		}
	}
	parts = append(parts, current)

	if len(parts) >= 3 {
		return &imageTagParts{
			prefix:    parts[0],
			serviceID: parts[1],
			commit:    parts[2],
		}
	}
	return nil
}
