package service

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/buildvigil/agent/internal/api"
	"github.com/buildvigil/agent/internal/secrets"
	"github.com/buildvigil/agent/internal/state"
)

// Manager handles running and supervising services
type Manager struct {
	reposPath  string
	state      *state.Manager
	secretsMgr *secrets.Manager
	processes  map[string]*processInfo
	mu         sync.RWMutex
}

type processInfo struct {
	cmd           *exec.Cmd
	service       api.Service
	cancel        chan struct{}
	runtime       string
	imageTag      string
	containerID   string
	containerName string
}

// NewManager creates a new service manager
func NewManager(reposPath string, stateMgr *state.Manager, secretsMgr *secrets.Manager) *Manager {
	return &Manager{
		reposPath:  reposPath,
		state:      stateMgr,
		secretsMgr: secretsMgr,
		processes:  make(map[string]*processInfo),
	}
}

// StartService starts a service process
func (m *Manager) StartService(service api.Service, repoPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.isDocker(service) {
		return m.startDockerServiceLocked(service, repoPath)
	}

	// Check if already running
	if info, exists := m.processes[service.ID]; exists {
		if info.cmd != nil && info.cmd.Process != nil {
			// Check if process is still alive
			if err := info.cmd.Process.Signal(syscall.Signal(0)); err == nil {
				return fmt.Errorf("service already running")
			}
		}
	}

	// Update status to building first
	if service.BuildCommand != "" {
		m.state.SaveServiceProcess(&state.ServiceProcess{
			ServiceID:   service.ID,
			ServiceName: service.Name,
			GitCommit:   service.GitCommit,
			Runtime:     "process",
			Status:      "building",
		})

		if err := m.runBuild(service, repoPath); err != nil {
			m.state.SaveServiceProcess(&state.ServiceProcess{
				ServiceID:   service.ID,
				ServiceName: service.Name,
				GitCommit:   service.GitCommit,
				Runtime:     "process",
				Status:      "error",
				LastError:   fmt.Sprintf("build failed: %v", err),
			})
			return fmt.Errorf("build failed: %w", err)
		}
	}

	// Start the service process
	cancel := make(chan struct{})
	info := &processInfo{
		service: service,
		cancel:  cancel,
		runtime: "process",
	}

	if err := m.runService(info, repoPath); err != nil {
		m.state.SaveServiceProcess(&state.ServiceProcess{
			ServiceID:   service.ID,
			ServiceName: service.Name,
			GitCommit:   service.GitCommit,
			Runtime:     "process",
			Status:      "error",
			LastError:   fmt.Sprintf("start failed: %v", err),
		})
		return fmt.Errorf("failed to start service: %w", err)
	}

	m.processes[service.ID] = info

	// Update state
	m.state.SaveServiceProcess(&state.ServiceProcess{
		ServiceID:   service.ID,
		ServiceName: service.Name,
		GitCommit:   service.GitCommit,
		Runtime:     "process",
		PID:         info.cmd.Process.Pid,
		Status:      "running",
		StartedAt:   time.Now(),
	})

	// Start supervision goroutine
	go m.supervise(info)

	return nil
}

func (m *Manager) isDocker(service api.Service) bool {
	return strings.EqualFold(service.Runtime, "docker")
}

func (m *Manager) startDockerServiceLocked(service api.Service, repoPath string) error {
	containerName := m.dockerContainerName(service.ID)

	if info, exists := m.processes[service.ID]; exists {
		if info.containerName != "" {
			if status, err := m.getDockerContainerStatus(info.containerName); err == nil && status == "running" {
				return fmt.Errorf("service already running")
			}
		}
	}

	m.state.SaveServiceProcess(&state.ServiceProcess{
		ServiceID:     service.ID,
		ServiceName:   service.Name,
		GitCommit:     service.GitCommit,
		Runtime:       "docker",
		Status:        "building",
		ContainerName: containerName,
	})

	imageTag := m.dockerImageTag(service.ID, service.GitCommit)
	if err := m.buildDockerImage(service, repoPath, imageTag); err != nil {
		m.state.SaveServiceProcess(&state.ServiceProcess{
			ServiceID:     service.ID,
			ServiceName:   service.Name,
			GitCommit:     service.GitCommit,
			Runtime:       "docker",
			Status:        "error",
			LastError:     fmt.Sprintf("docker build failed: %v", err),
			ContainerName: containerName,
		})
		return fmt.Errorf("docker build failed: %w", err)
	}

	_ = m.removeDockerContainer(containerName)

	containerID, err := m.runDockerContainer(service, imageTag, containerName)
	if err != nil {
		m.state.SaveServiceProcess(&state.ServiceProcess{
			ServiceID:     service.ID,
			ServiceName:   service.Name,
			GitCommit:     service.GitCommit,
			Runtime:       "docker",
			Status:        "error",
			LastError:     fmt.Sprintf("docker run failed: %v", err),
			ContainerName: containerName,
			ImageTag:      imageTag,
		})
		return fmt.Errorf("failed to start docker container: %w", err)
	}

	info := &processInfo{
		service:       service,
		runtime:       "docker",
		containerID:   containerID,
		containerName: containerName,
		imageTag:      imageTag,
	}
	m.processes[service.ID] = info

	m.state.SaveServiceProcess(&state.ServiceProcess{
		ServiceID:     service.ID,
		ServiceName:   service.Name,
		GitCommit:     service.GitCommit,
		Runtime:       "docker",
		ContainerID:   containerID,
		ContainerName: containerName,
		ImageTag:      imageTag,
		Status:        "running",
		StartedAt:     time.Now(),
	})

	if err := m.cleanupDockerImages(service, imageTag); err != nil {
		m.state.LogServiceMessage(service.ID, "error", fmt.Sprintf("Docker cleanup failed: %v", err))
	}

	return nil
}

// runBuild executes the build command for a service
func (m *Manager) runBuild(service api.Service, repoPath string) error {
	if strings.TrimSpace(service.BuildCommand) == "" {
		return fmt.Errorf("empty build command")
	}

	cmd := exec.Command("/bin/sh", "-c", service.BuildCommand)
	cmd.Dir = repoPath
	cmd.Env = m.buildEnv(service, repoPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build command failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// runService starts the service process
func (m *Manager) runService(info *processInfo, repoPath string) error {
	if strings.TrimSpace(info.service.RunCommand) == "" {
		return fmt.Errorf("empty run command")
	}

	cmd := exec.Command("/bin/sh", "-c", info.service.RunCommand)
	cmd.Dir = repoPath
	cmd.Env = m.buildEnv(info.service, repoPath)

	// Set up process group so we can kill children too
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Capture stdout/stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	info.cmd = cmd

	// Start log capture goroutines
	go m.captureLogs(info.service.ID, "stdout", stdout)
	go m.captureLogs(info.service.ID, "stderr", stderr)

	return nil
}

// captureLogs captures output from a service and logs it
func (m *Manager) captureLogs(serviceID, stream string, pipe interface{}) {
	var scanner *bufio.Scanner

	switch p := pipe.(type) {
	case *os.File:
		scanner = bufio.NewScanner(p)
	default:
		return
	}

	for scanner.Scan() {
		line := scanner.Text()
		level := "info"
		if stream == "stderr" {
			level = "error"
		}
		m.state.LogServiceMessage(serviceID, level, line)
	}
}

// supervise monitors a running service and restarts it if it crashes
func (m *Manager) supervise(info *processInfo) {
	for {
		select {
		case <-info.cancel:
			return
		default:
		}

		if info.cmd == nil || info.cmd.Process == nil {
			return
		}

		// Wait for process to exit
		err := info.cmd.Wait()

		select {
		case <-info.cancel:
			return
		default:
		}

		// Process crashed - restart it
		if err != nil {
			m.state.LogServiceMessage(info.service.ID, "error", fmt.Sprintf("Process exited with error: %v", err))
		}

		// Update restart count
		proc, _ := m.state.GetServiceProcess(info.service.ID)
		if proc != nil {
			proc.RestartCount++
			proc.Status = "restarting"
			m.state.SaveServiceProcess(proc)
		}

		// Wait a bit before restarting
		time.Sleep(5 * time.Second)

		// Restart the service
		repoPath := filepath.Join(m.reposPath, info.service.ID)
		if err := m.runService(info, repoPath); err != nil {
			m.state.LogServiceMessage(info.service.ID, "error", fmt.Sprintf("Failed to restart: %v", err))

			if proc != nil {
				proc.Status = "error"
				proc.LastError = fmt.Sprintf("restart failed: %v", err)
				m.state.SaveServiceProcess(proc)
			}
			return
		}

		if proc != nil {
			proc.PID = info.cmd.Process.Pid
			proc.Status = "running"
			m.state.SaveServiceProcess(proc)
		}
	}
}

// StopService stops a running service
func (m *Manager) StopService(serviceID string) error {
	m.mu.Lock()
	info, exists := m.processes[serviceID]
	if exists {
		delete(m.processes, serviceID)
	}
	m.mu.Unlock()

	if exists && info.runtime == "docker" {
		if err := m.removeDockerContainer(info.containerName); err != nil {
			return err
		}
		m.state.SaveServiceProcess(&state.ServiceProcess{
			ServiceID: serviceID,
			Status:    "stopped",
			Runtime:   "docker",
		})
		return nil
	}

	if exists {
		// Signal supervision to stop
		close(info.cancel)

		// Kill the process
		if info.cmd != nil && info.cmd.Process != nil {
			// Kill the entire process group
			syscall.Kill(-info.cmd.Process.Pid, syscall.SIGTERM)

			// Wait a bit for graceful shutdown
			done := make(chan error, 1)
			go func() {
				done <- info.cmd.Wait()
			}()

			select {
			case <-done:
				// Process exited gracefully
			case <-time.After(10 * time.Second):
				// Force kill
				syscall.Kill(-info.cmd.Process.Pid, syscall.SIGKILL)
			}
		}

		// Update state
		m.state.SaveServiceProcess(&state.ServiceProcess{
			ServiceID: serviceID,
			Status:    "stopped",
			Runtime:   "process",
		})

		return nil
	}

	proc, err := m.state.GetServiceProcess(serviceID)
	if err != nil {
		return err
	}
	if proc != nil && proc.Runtime == "docker" {
		if err := m.removeDockerContainer(proc.ContainerName); err != nil {
			return err
		}
		m.state.SaveServiceProcess(&state.ServiceProcess{
			ServiceID: serviceID,
			Status:    "stopped",
			Runtime:   "docker",
		})
		return nil
	}

	return fmt.Errorf("service not running")
}

// GetServiceStatus returns the current status of a service
func (m *Manager) GetServiceStatus(serviceID string) (string, error) {
	proc, err := m.state.GetServiceProcess(serviceID)
	if err != nil {
		return "", err
	}
	if proc == nil {
		return "stopped", nil
	}

	if proc.Runtime == "docker" {
		status, err := m.getDockerContainerStatus(proc.ContainerName)
		if err != nil {
			return "error", nil
		}
		return status, nil
	}

	// Check if process is still alive
	m.mu.RLock()
	info, running := m.processes[serviceID]
	m.mu.RUnlock()

	if running && info.cmd != nil && info.cmd.Process != nil {
		if err := info.cmd.Process.Signal(syscall.Signal(0)); err != nil {
			// Process is dead but state says running
			if proc.Status == "running" {
				return "crashed", nil
			}
		}
	}

	return proc.Status, nil
}

// buildEnv builds the environment variables for a service
func (m *Manager) buildEnv(service api.Service, repoPath string) []string {
	// Start with current environment
	env := os.Environ()

	// Add service-specific env vars (non-sensitive config)
	for key, value := range service.EnvironmentVars {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}

	// Add secrets from local encrypted storage
	if m.secretsMgr != nil {
		secretVars, err := m.secretsMgr.GetAllSecretsForService(service.ID)
		if err == nil {
			for key, value := range secretVars {
				env = append(env, fmt.Sprintf("%s=%s", key, value))
			}
		}
	}

	// Add internal service discovery URLs
	// This would be populated from all services in the stack
	env = append(env, fmt.Sprintf("SERVICE_%s_URL=http://%s.svc.internal",
		strings.ToUpper(service.Name), service.Name))

	return env
}

// ListRunningServices returns all currently running services
func (m *Manager) ListRunningServices() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var ids []string
	for id := range m.processes {
		ids = append(ids, id)
	}
	return ids
}

// HealthCheckResult represents the result of a service health check
type HealthCheckResult struct {
	ServiceID   string            `json:"service_id"`
	ServiceName string            `json:"service_name"`
	Status      string            `json:"status"`
	Healthy     bool              `json:"healthy"`
	LastCheck   time.Time         `json:"last_check"`
	Response    string            `json:"response,omitempty"`
	CheckType   string            `json:"check_type"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// CheckServiceHealth performs a health check on a specific service
func (m *Manager) CheckServiceHealth(serviceID string) (*HealthCheckResult, error) {
	proc, err := m.state.GetServiceProcess(serviceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get service process: %w", err)
	}
	if proc == nil {
		return &HealthCheckResult{
			ServiceID:   serviceID,
			ServiceName: "unknown",
			Status:      "stopped",
			Healthy:     false,
			LastCheck:   time.Now(),
			CheckType:   "process",
		}, nil
	}

	if proc.Runtime == "docker" {
		return m.checkDockerHealth(serviceID, proc)
	}

	// Check if process is running
	m.mu.RLock()
	info, running := m.processes[serviceID]
	m.mu.RUnlock()

	result := &HealthCheckResult{
		ServiceID:   serviceID,
		ServiceName: proc.ServiceName,
		LastCheck:   time.Now(),
		CheckType:   "process",
	}

	if !running || info.cmd == nil || info.cmd.Process == nil {
		result.Status = "stopped"
		result.Healthy = false
		return result, nil
	}

	// Check if process is alive
	if err := info.cmd.Process.Signal(syscall.Signal(0)); err != nil {
		result.Status = "crashed"
		result.Healthy = false
		return result, nil
	}

	result.Status = "running"
	result.Healthy = true

	// If service has a health check endpoint, perform HTTP check
	if info.service.HealthCheckPath != "" {
		httpResult := m.performHTTPHealthCheck(info.service)
		if httpResult != nil {
			result.CheckType = "http"
			result.Healthy = result.Healthy && httpResult.Healthy
			result.Response = httpResult.Response
			if httpResult.Metadata != nil {
				if result.Metadata == nil {
					result.Metadata = make(map[string]string)
				}
				for k, v := range httpResult.Metadata {
					result.Metadata[k] = v
				}
			}
		}
	}

	// Check memory usage
	if info.cmd.Process != nil {
		if memInfo, err := m.getProcessMemory(info.cmd.Process.Pid); err == nil {
			if result.Metadata == nil {
				result.Metadata = make(map[string]string)
			}
			result.Metadata["memory_rss"] = memInfo.RSS
			result.Metadata["memory_vms"] = memInfo.VMS
		}
	}

	return result, nil
}

// CheckAllServicesHealth performs health checks on all services
func (m *Manager) CheckAllServicesHealth() []*HealthCheckResult {
	var results []*HealthCheckResult

	// Get all services from state
	services := m.state.GetAllServiceProcesses()
	for _, proc := range services {
		result, err := m.CheckServiceHealth(proc.ServiceID)
		if err != nil {
			result = &HealthCheckResult{
				ServiceID:   proc.ServiceID,
				ServiceName: proc.ServiceName,
				Status:      "error",
				Healthy:     false,
				LastCheck:   time.Now(),
				CheckType:   "state",
				Response:    err.Error(),
			}
		}
		results = append(results, result)
	}

	return results
}

// StartHealthCheckServer starts an HTTP server for health checks
func (m *Manager) StartHealthCheckServer(port int) error {
	mux := http.NewServeMux()

	// Health check endpoint for all services
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		results := m.CheckAllServicesHealth()

		// Determine overall health
		allHealthy := true
		for _, result := range results {
			if !result.Healthy {
				allHealthy = false
				break
			}
		}

		statusCode := http.StatusOK
		if !allHealthy {
			statusCode = http.StatusServiceUnavailable
		}

		w.WriteHeader(statusCode)

		response := map[string]interface{}{
			"status":    "ok",
			"healthy":   allHealthy,
			"timestamp": time.Now(),
			"services":  results,
		}

		json.NewEncoder(w).Encode(response)
	})

	// Health check endpoint for specific service
	mux.HandleFunc("/health/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Extract service ID from URL path
		path := strings.TrimPrefix(r.URL.Path, "/health/")
		if path == "" {
			http.Error(w, "service ID required", http.StatusBadRequest)
			return
		}

		result, err := m.CheckServiceHealth(path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		statusCode := http.StatusOK
		if !result.Healthy {
			statusCode = http.StatusServiceUnavailable
		}

		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(result)
	})

	// Service info endpoint
	mux.HandleFunc("/services", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		results := m.CheckAllServicesHealth()
		json.NewEncoder(w).Encode(results)
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	return server.ListenAndServe()
}

// performHTTPHealthCheck performs an HTTP health check on a service
func (m *Manager) performHTTPHealthCheck(service api.Service) *HealthCheckResult {
	if service.HealthCheckPath == "" {
		return nil
	}

	// Construct health check URL using the configured service port.
	basePort := service.Port
	if basePort <= 0 {
		basePort = 3000
	}
	// Try to parse port from run command if it contains port info
	if strings.Contains(service.RunCommand, "PORT=") {
		parts := strings.Fields(service.RunCommand)
		for _, part := range parts {
			if strings.HasPrefix(part, "PORT=") {
				if portStr := strings.TrimPrefix(part, "PORT="); portStr != "" {
					if p, err := strconv.Atoi(portStr); err == nil {
						basePort = p
					}
				}
			}
		}
	}

	url := fmt.Sprintf("http://localhost:%d%s", basePort, service.HealthCheckPath)

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return &HealthCheckResult{
			ServiceID: service.ID,
			Healthy:   false,
			Response:  fmt.Sprintf("HTTP check failed: %v", err),
		}
	}
	defer resp.Body.Close()

	result := &HealthCheckResult{
		ServiceID: service.ID,
		Healthy:   resp.StatusCode >= 200 && resp.StatusCode < 300,
		Response:  fmt.Sprintf("HTTP %d", resp.StatusCode),
		Metadata: map[string]string{
			"http_status": strconv.Itoa(resp.StatusCode),
			"http_url":    url,
		},
	}

	return result
}

// MemoryInfo represents process memory information
type MemoryInfo struct {
	RSS string `json:"rss"` // Resident Set Size
	VMS string `json:"vms"` // Virtual Memory Size
}

// getProcessMemory gets memory usage for a process
func (m *Manager) getProcessMemory(pid int) (*MemoryInfo, error) {
	// Read from /proc/[pid]/stat on Linux
	statFile := fmt.Sprintf("/proc/%d/stat", pid)
	data, err := os.ReadFile(statFile)
	if err != nil {
		return nil, err
	}

	fields := strings.Fields(string(data))
	if len(fields) < 24 {
		return nil, fmt.Errorf("invalid stat file format")
	}

	// Parse RSS and VMS from stat file
	// RSS is field 24 (in pages)
	// VMS is field 23 (in bytes)
	rssPages, err := strconv.ParseInt(fields[23], 10, 64)
	if err != nil {
		return nil, err
	}

	vmsBytes, err := strconv.ParseInt(fields[22], 10, 64)
	if err != nil {
		return nil, err
	}

	// Convert RSS pages to bytes (assuming 4KB pages)
	rssBytes := rssPages * 4096

	return &MemoryInfo{
		RSS: fmt.Sprintf("%.2f MB", float64(rssBytes)/1024/1024),
		VMS: fmt.Sprintf("%.2f MB", float64(vmsBytes)/1024/1024),
	}, nil
}

func (m *Manager) dockerContainerName(serviceID string) string {
	return fmt.Sprintf("buildvigil-%s", serviceID)
}

func (m *Manager) dockerImageTag(serviceID, gitCommit string) string {
	commit := strings.TrimSpace(gitCommit)
	if commit == "" {
		commit = "latest"
	}
	return fmt.Sprintf("buildvigil/%s:%s", serviceID, commit)
}

func (m *Manager) buildDockerImage(service api.Service, repoPath, imageTag string) error {
	dockerfilePath := strings.TrimSpace(service.DockerfilePath)
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}
	if !filepath.IsAbs(dockerfilePath) {
		dockerfilePath = filepath.Join(repoPath, dockerfilePath)
	}

	contextPath := strings.TrimSpace(service.DockerContext)
	if contextPath == "" {
		contextPath = "."
	}
	if !filepath.IsAbs(contextPath) {
		contextPath = filepath.Join(repoPath, contextPath)
	}

	cmd := exec.Command("docker", "build", "-f", dockerfilePath, "-t", imageTag, contextPath)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker build failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

func (m *Manager) runDockerContainer(service api.Service, imageTag, containerName string) (string, error) {
	args := []string{"run", "-d", "--name", containerName, "--restart=unless-stopped"}

	for key, value := range service.EnvironmentVars {
		args = append(args, "-e", fmt.Sprintf("%s=%s", key, value))
	}

	hostPort := service.Port
	containerPort := service.DockerContainerPort
	if containerPort <= 0 {
		containerPort = hostPort
	}
	if hostPort > 0 && containerPort > 0 {
		args = append(args, "-p", fmt.Sprintf("%d:%d", hostPort, containerPort))
	}

	if strings.TrimSpace(service.RunCommand) != "" {
		args = append(args, strings.Fields(service.RunCommand)...)
	}

	args = append(args, imageTag)

	cmd := exec.Command("docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker run failed: %w\nOutput: %s", err, string(output))
	}

	containerID := strings.TrimSpace(string(output))
	if containerID == "" {
		return "", fmt.Errorf("docker run returned empty container id")
	}

	return containerID, nil
}

func (m *Manager) removeDockerContainer(containerName string) error {
	if strings.TrimSpace(containerName) == "" {
		return nil
	}
	cmd := exec.Command("docker", "rm", "-f", containerName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "No such container") || strings.Contains(string(output), "No such object") {
			return nil
		}
		return fmt.Errorf("docker rm failed: %w\nOutput: %s", err, string(output))
	}
	return nil
}

func (m *Manager) getDockerContainerStatus(containerName string) (string, error) {
	if strings.TrimSpace(containerName) == "" {
		return "stopped", nil
	}
	cmd := exec.Command("docker", "inspect", "--format", "{{.State.Status}}", containerName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "No such object") || strings.Contains(string(output), "No such container") {
			return "stopped", nil
		}
		return "error", fmt.Errorf("docker inspect failed: %w\nOutput: %s", err, string(output))
	}
	status := strings.TrimSpace(string(output))
	if status == "" {
		status = "unknown"
	}
	return status, nil
}

func (m *Manager) getDockerHealthStatus(containerName string) (string, error) {
	cmd := exec.Command("docker", "inspect", "--format", "{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}", containerName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "unknown", fmt.Errorf("docker inspect health failed: %w\nOutput: %s", err, string(output))
	}
	return strings.TrimSpace(string(output)), nil
}

func (m *Manager) checkDockerHealth(serviceID string, proc *state.ServiceProcess) (*HealthCheckResult, error) {
	result := &HealthCheckResult{
		ServiceID:   serviceID,
		ServiceName: proc.ServiceName,
		LastCheck:   time.Now(),
		CheckType:   "docker",
	}

	status, err := m.getDockerContainerStatus(proc.ContainerName)
	if err != nil {
		result.Status = "error"
		result.Healthy = false
		result.Response = err.Error()
		return result, nil
	}
	result.Status = status
	result.Healthy = status == "running"

	m.mu.RLock()
	info, running := m.processes[serviceID]
	m.mu.RUnlock()
	if running && info.service.HealthCheckPath != "" {
		httpResult := m.performHTTPHealthCheck(info.service)
		if httpResult != nil {
			result.CheckType = "http"
			result.Healthy = result.Healthy && httpResult.Healthy
			result.Response = httpResult.Response
			result.Metadata = httpResult.Metadata
			return result, nil
		}
	}

	healthStatus, err := m.getDockerHealthStatus(proc.ContainerName)
	if err == nil {
		if result.Metadata == nil {
			result.Metadata = make(map[string]string)
		}
		result.Metadata["docker_health"] = healthStatus
		switch healthStatus {
		case "healthy":
			result.Healthy = result.Healthy && true
		case "unhealthy":
			result.Healthy = false
		case "none":
			// No health configured, keep current status
		}
	}

	return result, nil
}

func (m *Manager) cleanupDockerImages(service api.Service, keepTag string) error {
	retain := service.ImageRetainCount
	if retain <= 0 {
		retain = 5
	}

	cmd := exec.Command("docker", "images", "--format", "{{.Repository}}:{{.Tag}}|{{.ID}}|{{.CreatedAt}}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker images failed: %w\nOutput: %s", err, string(output))
	}

	prefix := fmt.Sprintf("buildvigil/%s:", service.ID)
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var images []struct {
		Tag       string
		ID        string
		CreatedAt time.Time
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		tag := parts[0]
		if !strings.HasPrefix(tag, prefix) {
			continue
		}
		createdAt, _ := time.Parse("2006-01-02 15:04:05 -0700 MST", parts[2])
		images = append(images, struct {
			Tag       string
			ID        string
			CreatedAt time.Time
		}{Tag: tag, ID: parts[1], CreatedAt: createdAt})
	}

	if len(images) <= retain {
		return nil
	}

	sort.Slice(images, func(i, j int) bool {
		return images[i].CreatedAt.After(images[j].CreatedAt)
	})

	for _, image := range images[retain:] {
		if image.Tag == keepTag {
			continue
		}
		remove := exec.Command("docker", "rmi", "-f", image.ID)
		if output, err := remove.CombinedOutput(); err != nil {
			return fmt.Errorf("docker rmi failed: %w\nOutput: %s", err, string(output))
		}
	}

	return nil
}
