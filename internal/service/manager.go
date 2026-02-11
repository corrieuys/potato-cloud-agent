package service

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/buildvigil/agent/internal/api"
	containerpkg "github.com/buildvigil/agent/internal/container"
	"github.com/buildvigil/agent/internal/secrets"
	"github.com/buildvigil/agent/internal/state"
)

const (
	HealthCheckTimeout     = 60 * time.Second
	HealthCheckInterval    = 30 * time.Second
	ConnectionDrainTimeout = 30 * time.Second
	MaxConcurrentBuilds    = 3
	ContainerPrefix        = "potato-cloud"
	ImagePrefix            = "potato-cloud"
)

// ProxyUpdater is a callback function to update proxy routes.
type ProxyUpdater func(serviceID string, activePort int) error

type containerInfo struct {
	service       api.Service
	containerName string
	imageTag      string
	port          int
}

// Manager handles Docker container lifecycle for services.
type Manager struct {
	reposPath    string
	state        *state.Manager
	secretsMgr   *secrets.Manager
	containers   map[string]*containerInfo
	portMgr      *containerpkg.PortManager
	generator    *containerpkg.Generator
	proxyUpdater ProxyUpdater
	verbose      bool
	mu           sync.RWMutex
}

// NewManager creates a new service manager.
func NewManager(reposPath string, stateMgr *state.Manager, secretsMgr *secrets.Manager, portStart, portEnd int, verbose bool) *Manager {
	return &Manager{
		reposPath:  reposPath,
		state:      stateMgr,
		secretsMgr: secretsMgr,
		containers: make(map[string]*containerInfo),
		portMgr:    containerpkg.NewPortManager(portStart, portEnd),
		generator:  containerpkg.NewGenerator(portStart, portEnd),
		verbose:    verbose,
	}
}

// SetProxyUpdater sets a callback for proxy route updates.
func (m *Manager) SetProxyUpdater(updater ProxyUpdater) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.proxyUpdater = updater
}

// DeployService deploys a service using Docker containers with zero-downtime.
func (m *Manager) DeployService(service api.Service) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	containerName := fmt.Sprintf("%s-%s", ContainerPrefix, service.ID)
	imageTag := fmt.Sprintf("%s-%s:latest", ImagePrefix, service.ID)

	currentInfo, exists := m.containers[service.ID]
	if exists && currentInfo.port != 0 {
		return m.blueGreenDeploy(service, currentInfo, containerName, imageTag)
	}
	return m.initialDeploy(service, containerName, imageTag)
}

func (m *Manager) initialDeploy(service api.Service, containerName, imageTag string) error {
	imageID, err := m.buildServiceImage(service, imageTag)
	if err != nil {
		return fmt.Errorf("failed to build image: %w", err)
	}

	portPair, err := m.portMgr.Allocate(service.ID)
	if err != nil {
		return fmt.Errorf("failed to allocate port: %w", err)
	}
	port := portPair.BluePort

	env := m.prepareEnvironment(service)

	containerID, err := m.startContainer(containerName, imageID, port, env, nil)
	if err != nil {
		m.portMgr.Release(service.ID)
		return fmt.Errorf("failed to start container: %w", err)
	}

	if err := ConnectContainerToStackNetwork(containerID, service.ID); err != nil {
		_ = m.stopContainer(containerName)
		m.portMgr.Release(service.ID)
		return fmt.Errorf("failed to connect container to stack network: %w", err)
	}

	if err := m.healthCheck(service, containerName, port); err != nil {
		_ = m.stopContainer(containerName)
		_ = DisconnectContainerFromStackNetwork(containerID, service.ID)
		m.portMgr.Release(service.ID)
		return fmt.Errorf("health check failed: %w", err)
	}

	if m.proxyUpdater != nil {
		if err := m.proxyUpdater(service.ID, port); err != nil {
			m.logVerbose("Failed to update proxy routes: %v", err)
		}
	}

	m.containers[service.ID] = &containerInfo{
		service:       service,
		containerName: containerName,
		imageTag:      imageTag,
		port:          port,
	}

	m.logVerbose("Service %s deployed successfully on port %d", service.ID, port)
	return nil
}

func (m *Manager) blueGreenDeploy(service api.Service, currentInfo *containerInfo, containerName, imageTag string) error {
	imageID, err := m.buildServiceImage(service, imageTag)
	if err != nil {
		return fmt.Errorf("failed to build new image: %w", err)
	}

	portPair, exists := m.portMgr.Get(service.ID)
	if !exists {
		return fmt.Errorf("service port pair not found")
	}
	greenPort := portPair.GreenPort

	env := m.prepareEnvironment(service)
	greenContainerName := containerName + "-green"
	greenContainerID, err := m.startContainer(greenContainerName, imageID, greenPort, env, nil)
	if err != nil {
		return fmt.Errorf("failed to start green container: %w", err)
	}

	if err := ConnectContainerToStackNetwork(greenContainerID, service.ID); err != nil {
		_ = m.stopContainer(greenContainerName)
		return fmt.Errorf("failed to connect green container to stack network: %w", err)
	}

	if err := m.healthCheck(service, greenContainerName, greenPort); err != nil {
		_ = m.stopContainer(greenContainerName)
		_ = DisconnectContainerFromStackNetwork(greenContainerID, service.ID)
		return fmt.Errorf("green container health check failed: %w", err)
	}

	if m.proxyUpdater != nil {
		if err := m.proxyUpdater(service.ID, greenPort); err != nil {
			m.logVerbose("Failed to update proxy routes to green: %v", err)
		}
	}

	time.Sleep(ConnectionDrainTimeout)

	if err := m.stopContainer(currentInfo.containerName); err != nil {
		m.logVerbose("Failed to stop blue container: %v", err)
	}
	_ = DisconnectContainerFromStackNetwork(currentInfo.containerName, service.ID)

	activeContainerName := greenContainerName
	if err := renameContainer(greenContainerName, containerName); err != nil {
		m.logVerbose("Failed to rename green container %s to %s: %v", greenContainerName, containerName, err)
	} else {
		activeContainerName = containerName
	}

	m.containers[service.ID] = &containerInfo{
		service:       service,
		containerName: activeContainerName,
		imageTag:      imageTag,
		port:          greenPort,
	}

	m.logVerbose("Blue/green deployment completed for service %s, now on port %d", service.ID, greenPort)
	return nil
}

func (m *Manager) buildServiceImage(service api.Service, imageTag string) (string, error) {
	repoPath := filepath.Join(m.reposPath, service.ID)
	dockerfilePath, exists := m.generator.CheckDockerfileExists(repoPath)
	generatedDockerfile := false
	if !exists {
		dockerfileContent, err := m.generator.GenerateDockerfile(
			service.Language,
			service.BaseImage,
			8000,
			service.EnvironmentVars,
			repoPath,
		)
		if err != nil {
			return "", fmt.Errorf("failed to generate Dockerfile: %w", err)
		}

		dockerfilePath, err = m.generator.WriteDockerfile(dockerfileContent, repoPath)
		if err != nil {
			return "", fmt.Errorf("failed to write Dockerfile: %w", err)
		}
		generatedDockerfile = true
	}
	if generatedDockerfile {
		defer func() {
			_ = os.Remove(dockerfilePath)
		}()
	}

	buildCmd := exec.Command("docker", "build", "-t", imageTag, "-f", dockerfilePath, repoPath)
	if m.verbose {
		buildCmd.Stdout = os.Stdout
		buildCmd.Stderr = os.Stderr
	}
	if err := buildCmd.Run(); err != nil {
		return "", fmt.Errorf("docker build failed: %w", err)
	}

	inspectCmd := exec.Command("docker", "inspect", "--format={{.Id}}", imageTag)
	output, err := inspectCmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to inspect image: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func (m *Manager) prepareEnvironment(service api.Service) []string {
	env := make([]string, 0, len(service.EnvironmentVars))
	for key, value := range service.EnvironmentVars {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}
	return env
}

func (m *Manager) startContainer(name, imageID string, port int, env []string, command []string) (string, error) {
	portBinding := fmt.Sprintf("%d:%d", port, 8000)
	args := []string{"run", "-d", "--name", name, "-p", portBinding}

	for _, e := range env {
		args = append(args, "-e", e)
	}

	args = append(args, imageID)
	args = append(args, command...)

	cmd := exec.Command("docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to start container: %w (output: %s)", err, strings.TrimSpace(string(output)))
	}

	return strings.TrimSpace(string(output)), nil
}

func (m *Manager) stopContainer(name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}

	stopOut, stopErr := exec.Command("docker", "stop", "-t", "10", name).CombinedOutput()
	if stopErr != nil {
		msg := strings.ToLower(string(stopOut))
		if !strings.Contains(msg, "no such container") && !strings.Contains(msg, "no such object") && !strings.Contains(msg, "is not running") {
			return fmt.Errorf("failed to stop container: %w (output: %s)", stopErr, strings.TrimSpace(string(stopOut)))
		}
	}

	rmOut, rmErr := exec.Command("docker", "rm", "-f", name).CombinedOutput()
	if rmErr != nil {
		msg := strings.ToLower(string(rmOut))
		if !strings.Contains(msg, "no such container") && !strings.Contains(msg, "no such object") {
			return fmt.Errorf("failed to remove container: %w (output: %s)", rmErr, strings.TrimSpace(string(rmOut)))
		}
	}

	return nil
}

func (m *Manager) healthCheck(service api.Service, containerName string, port int) error {
	healthPath := strings.TrimSpace(service.HealthCheckPath)
	if healthPath == "" {
		status, err := getContainerStatus(containerName)
		if err != nil {
			return fmt.Errorf("failed to read container status: %w", err)
		}
		if status == "running" {
			return nil
		}
		return fmt.Errorf("container is not running (status: %s)", status)
	}

	if !strings.HasPrefix(healthPath, "/") {
		healthPath = "/" + healthPath
	}

	interval := HealthCheckInterval
	if service.HealthCheckInterval > 0 {
		interval = time.Duration(service.HealthCheckInterval) * time.Second
	}
	if interval < time.Second {
		interval = time.Second
	}

	client := &http.Client{Timeout: 5 * time.Second}
	url := fmt.Sprintf("http://localhost:%d%s", port, healthPath)
	deadline := time.Now().Add(HealthCheckTimeout)

	for {
		resp, err := client.Get(url)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			resp.Body.Close()
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("health check timeout for %s", url)
		}
		time.Sleep(interval)
	}
}

// GetServicePort returns the current port for a service.
func (m *Manager) GetServicePort(serviceID string) (int, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	info, exists := m.containers[serviceID]
	if !exists {
		return 0, false
	}
	return info.port, true
}

// StopService stops a service and cleans up resources.
func (m *Manager) StopService(serviceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	info, exists := m.containers[serviceID]
	if !exists {
		return fmt.Errorf("service %s not found", serviceID)
	}

	if err := m.stopContainer(info.containerName); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}

	_ = DisconnectContainerFromStackNetwork(info.containerName, serviceID)
	m.portMgr.Release(serviceID)
	delete(m.containers, serviceID)

	m.logVerbose("Service %s stopped successfully", serviceID)
	return nil
}

// GetServiceStatus returns the status of a service.
func (m *Manager) GetServiceStatus(serviceID string) (map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	info, exists := m.containers[serviceID]
	if !exists {
		return map[string]interface{}{
			"running": false,
			"error":   "Service not found",
		}, nil
	}

	cmd := exec.Command("docker", "inspect", "--format={{.State.Status}}", info.containerName)
	output, err := cmd.Output()
	if err != nil {
		return map[string]interface{}{
			"running": false,
			"error":   fmt.Sprintf("Failed to check container status: %v", err),
		}, nil
	}

	status := strings.TrimSpace(string(output))
	running := status == "running"

	return map[string]interface{}{
		"running":        running,
		"container_name": info.containerName,
		"image_tag":      info.imageTag,
		"port":           info.port,
		"status":         status,
	}, nil
}

// DeleteStackNetwork deletes a stack's network.
func (m *Manager) DeleteStackNetwork(stackID string) error {
	return DeleteStackNetwork(stackID)
}

// ListStackNetworks returns all stack networks.
func (m *Manager) ListStackNetworks() ([]containerpkg.NetworkResource, error) {
	return ListStackNetworks()
}

// IsStackNetworkCreated checks if a stack network exists.
func (m *Manager) IsStackNetworkCreated(stackID string) bool {
	return IsStackNetworkCreated(stackID)
}

// ConnectContainerToStackNetwork connects a container to its stack's network.
func (m *Manager) ConnectContainerToStackNetwork(containerID, stackID string) error {
	return ConnectContainerToStackNetwork(containerID, stackID)
}

// DisconnectContainerFromStackNetwork disconnects a container from its stack's network.
func (m *Manager) DisconnectContainerFromStackNetwork(containerID, stackID string) error {
	return DisconnectContainerFromStackNetwork(containerID, stackID)
}

// GetStackNetworkName returns the network name for a stack.
func (m *Manager) GetStackNetworkName(stackID string) string {
	return GetStackNetworkName(stackID)
}

// CleanupStack deletes a stack and all its resources.
func (m *Manager) CleanupStack(stackID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for serviceID, info := range m.containers {
		if strings.HasPrefix(serviceID, stackID) {
			if err := m.stopContainer(info.containerName); err != nil {
				m.logVerbose("Failed to stop container %s: %v", info.containerName, err)
			}
			_ = DisconnectContainerFromStackNetwork(info.containerName, stackID)
			delete(m.containers, serviceID)
		}
	}

	for serviceID := range m.containers {
		if strings.HasPrefix(serviceID, stackID) {
			m.portMgr.Release(serviceID)
		}
	}
	if err := DeleteStackNetwork(stackID); err != nil {
		m.logVerbose("Failed to delete stack network: %v", err)
	}

	m.logVerbose("Stack %s cleaned up successfully", stackID)
	return nil
}

// ListStackServices returns all services in a stack.
func (m *Manager) ListStackServices(stackID string) []api.Service {
	m.mu.RLock()
	defer m.mu.RUnlock()

	services := make([]api.Service, 0)
	for _, info := range m.containers {
		if strings.HasPrefix(info.service.ID, stackID) {
			services = append(services, info.service)
		}
	}
	return services
}

// GetServiceCount returns the number of services in a stack.
func (m *Manager) GetServiceCount(stackID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for serviceID := range m.containers {
		if strings.HasPrefix(serviceID, stackID) {
			count++
		}
	}
	return count
}

// IsStackEmpty checks if a stack has no services.
func (m *Manager) IsStackEmpty(stackID string) bool {
	return m.GetServiceCount(stackID) == 0
}

func (m *Manager) logVerbose(format string, args ...interface{}) {
	if m.verbose {
		fmt.Printf("[ServiceManager] "+format+"\n", args...)
	}
}
