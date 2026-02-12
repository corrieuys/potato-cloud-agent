package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/buildvigil/agent/internal/api"
	"github.com/buildvigil/agent/internal/config"
	"github.com/buildvigil/agent/internal/firewall"
	"github.com/buildvigil/agent/internal/git"
	"github.com/buildvigil/agent/internal/proxy"
	"github.com/buildvigil/agent/internal/secrets"
	"github.com/buildvigil/agent/internal/service"
	"github.com/buildvigil/agent/internal/state"
)

type optionalString struct {
	value string
	set   bool
}

func (o *optionalString) String() string {
	return o.value
}

func (o *optionalString) Set(value string) error {
	o.value = value
	o.set = true
	return nil
}

func main() {
	var (
		genSSHKey     = flag.Bool("gen-ssh-key", false, "Generate an SSH keypair for git access")
		sshKeyName    = flag.String("ssh-key-name", "default", "SSH key name to generate (filename under ssh dir)")
		configPath    = flag.String("config", config.ConfigPath(), "Path to config file")
		applyFirewall = flag.Bool("apply-firewall", false, "Apply firewall rules (requires root)")
		showStatus    = flag.Bool("status", false, "Show current service status")

		agentIDFlag            optionalString
		stackIDFlag            optionalString
		controlPlaneFlag       optionalString
		accessClientIDFlag     optionalString
		accessClientSecretFlag optionalString

		// Secret management flags
		addSecret     = flag.Bool("add-secret", false, "Add a new secret")
		listSecrets   = flag.Bool("list-secrets", false, "List all secrets for a service")
		deleteSecret  = flag.Bool("delete-secret", false, "Delete a secret")
		secretName    = flag.String("secret-name", "", "Name of the secret")
		secretService = flag.String("service", "", "Service ID or name for the secret")
		secretValue   = flag.String("value", "", "Secret value (if not provided, will prompt)")

		// Log management flags
		showLogs   = flag.Bool("logs", false, "Show service logs")
		followLogs = flag.Bool("f", false, "Follow logs in real-time (tail -f style)")
		logService = flag.String("log-service", "", "Service ID for log viewing")
	)

	flag.Var(&agentIDFlag, "agent-id", "Agent ID")
	flag.Var(&stackIDFlag, "stack-id", "Stack ID")
	flag.Var(&controlPlaneFlag, "control-plane", "Control plane URL")
	flag.Var(&accessClientIDFlag, "access-client-id", "Cloudflare Access client ID")
	flag.Var(&accessClientSecretFlag, "access-client-secret", "Cloudflare Access client secret")
	flag.Parse()

	if *genSSHKey {
		cfg := config.DefaultConfig()
		keysDir := cfg.SSHKeyDir()
		publicKey, privateKeyPath, err := git.GenerateSSHKeyPair(keysDir, *sshKeyName)
		if err != nil {
			log.Fatalf("SSH key generation failed: %v", err)
		}
		fmt.Printf("SSH key generated:\n- Private key: %s\n- Public key:\n%s", privateKeyPath, publicKey)
		return
	}

	if *showStatus {
		if err := printServiceStatus(*configPath); err != nil {
			log.Fatalf("Failed to get status: %v", err)
		}
		return
	}

	// Handle secret management commands
	if *addSecret {
		if err := handleAddSecret(*configPath, *secretService, *secretName, *secretValue); err != nil {
			log.Fatalf("Failed to add secret: %v", err)
		}
		return
	}

	if *listSecrets {
		if err := handleListSecrets(*configPath, *secretService); err != nil {
			log.Fatalf("Failed to list secrets: %v", err)
		}
		return
	}

	if *deleteSecret {
		if err := handleDeleteSecret(*configPath, *secretService, *secretName); err != nil {
			log.Fatalf("Failed to delete secret: %v", err)
		}
		return
	}

	// Handle log viewing
	if *showLogs {
		if err := handleShowLogs(*configPath, *logService, *followLogs); err != nil {
			log.Fatalf("Failed to show logs: %v", err)
		}
		return
	}

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if err := applyConfigOverrides(cfg, *configPath, agentIDFlag, stackIDFlag, controlPlaneFlag, accessClientIDFlag, accessClientSecretFlag); err != nil {
		log.Fatalf("Failed to apply config overrides: %v", err)
	}

	// Ensure data directories exist
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}
	if err := os.MkdirAll(cfg.ReposPath(), 0755); err != nil {
		log.Fatalf("Failed to create repos directory: %v", err)
	}

	// Initialize state manager
	stateMgr, err := state.NewManager(cfg.StateDBPath())
	if err != nil {
		log.Fatalf("Failed to initialize state: %v", err)
	}
	defer stateMgr.Close()

	// Initialize secrets manager
	secretsMgr, err := secrets.NewManager(cfg.SecretsPath(), cfg.AgentID)
	if err != nil {
		log.Fatalf("Failed to initialize secrets manager: %v", err)
	}

	// Initialize git manager
	gitMgr := git.NewManager(cfg.ReposPath(), cfg.SSHKeyDir())

	// Initialize service manager
	svcMgr := service.NewManager(cfg.ReposPath(), stateMgr, secretsMgr, cfg.PortRangeStart, cfg.PortRangeEnd, cfg.VerboseLogging)

	// Initialize proxies
	externalProxy := proxy.NewExternalProxy(cfg.ExternalProxyPort, "0.0.0.0")
	internalProxy := proxy.NewInternalProxy()

	// Initialize DNS manager
	dnsMgr := proxy.NewDNSManager()

	// Initialize API client
	apiClient := api.NewClient(cfg.ControlPlane, cfg.AgentID, cfg.AccessClientID, cfg.AccessClientSecret)

	// Initialize firewall manager (will be configured after first sync)
	var fwMgr *firewall.Manager

	// Create agent
	agent := &Agent{
		config:        cfg,
		state:         stateMgr,
		git:           gitMgr,
		services:      svcMgr,
		api:           apiClient,
		externalProxy: externalProxy,
		internalProxy: internalProxy,
		dnsMgr:        dnsMgr,
		fwMgr:         fwMgr,
		applyFirewall: *applyFirewall,
		healthPort:    9090, // Health check server port
		lifecycle:     make(map[string]api.ServiceStatus),
	}
	svcMgr.SetLifecycleReporter(agent.onServiceLifecycleEvent)

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start agent
	go agent.Run()

	// Start health check server
	// Note: Health check server functionality not yet implemented
	// go func() {
	// 	log.Printf("Starting health check server on port %d", agent.healthPort)
	// 	if err := agent.services.StartHealthCheckServer(agent.healthPort); err != nil {
	// 		log.Printf("Health check server failed: %v", err)
	// 	}
	// }()

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutting down...")
	agent.Stop()
}

func applyConfigOverrides(cfg *config.Config, configPath string, agentID, stackID, controlPlane, accessClientID, accessClientSecret optionalString) error {
	changed := false

	if agentID.set {
		cfg.AgentID = strings.TrimSpace(agentID.value)
		changed = true
	}
	if stackID.set {
		cfg.StackID = strings.TrimSpace(stackID.value)
		changed = true
	}
	if controlPlane.set {
		cfg.ControlPlane = strings.TrimSpace(controlPlane.value)
		changed = true
	}
	if accessClientID.set {
		cfg.AccessClientID = strings.TrimSpace(accessClientID.value)
		changed = true
	}
	if accessClientSecret.set {
		cfg.AccessClientSecret = strings.TrimSpace(accessClientSecret.value)
		changed = true
	}

	if !changed {
		return nil
	}

	if err := cfg.Save(configPath); err != nil {
		return err
	}

	return nil
}

// Agent is the main agent structure
type Agent struct {
	config            *config.Config
	state             *state.Manager
	git               *git.Manager
	services          *service.Manager
	api               *api.Client
	externalProxy     *proxy.ExternalProxy
	internalProxy     *proxy.InternalProxy
	dnsMgr            *proxy.DNSManager
	fwMgr             *firewall.Manager
	stopChan          chan struct{}
	applyFirewall     bool
	currentMode       string
	healthPort        int
	heartbeatMu       sync.Mutex
	heartbeatInterval int
	lifecycleMu       sync.RWMutex
	lifecycle         map[string]api.ServiceStatus
}

// Run starts the agent main loop
func (a *Agent) Run() {
	a.stopChan = make(chan struct{})
	a.heartbeatMu.Lock()
	if a.heartbeatInterval <= 0 {
		a.heartbeatInterval = 30
	}
	initialHeartbeatInterval := a.heartbeatInterval
	a.heartbeatMu.Unlock()
	log.Printf("Agent run loop started: poll_interval=%ds heartbeat_interval=%ds (initial, may update from desired state)", a.config.PollInterval, initialHeartbeatInterval)

	if a.externalProxy != nil {
		go func() {
			if err := a.externalProxy.Start(); err != nil {
				log.Printf("External proxy failed: %v", err)
			}
		}()
	}
	if a.internalProxy != nil {
		go func() {
			if err := a.internalProxy.Start(); err != nil {
				log.Printf("Internal proxy failed: %v", err)
			}
		}()
	}

	// Do initial sync
	if err := a.sync(); err != nil {
		log.Printf("Initial sync failed: %v", err)
	}

	// Send initial heartbeat
	if err := a.sendHeartbeat(); err != nil {
		log.Printf("Initial heartbeat failed: %v", err)
	}

	// Start poll loop
	ticker := time.NewTicker(time.Duration(a.config.PollInterval) * time.Second)
	defer ticker.Stop()

	// Start heartbeat loop with the current interval (possibly updated by initial sync)
	a.heartbeatMu.Lock()
	currentHeartbeatInterval := a.heartbeatInterval
	if currentHeartbeatInterval <= 0 {
		currentHeartbeatInterval = 30
		a.heartbeatInterval = currentHeartbeatInterval
	}
	a.heartbeatMu.Unlock()
	heartbeatTicker := time.NewTicker(time.Duration(currentHeartbeatInterval) * time.Second)
	lastHeartbeatInterval := currentHeartbeatInterval
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-a.stopChan:
			return
		case <-ticker.C:
			a.logVerbosef("Sync tick")
			if err := a.sync(); err != nil {
				log.Printf("Sync failed: %v", err)
			}
			// Reset heartbeat ticker only when interval actually changes.
			// If poll interval < heartbeat interval, resetting every sync would prevent heartbeats.
			a.heartbeatMu.Lock()
			currentInterval := a.heartbeatInterval
			a.heartbeatMu.Unlock()
			if currentInterval > 0 && currentInterval != lastHeartbeatInterval {
				heartbeatTicker.Reset(time.Duration(currentInterval) * time.Second)
				lastHeartbeatInterval = currentInterval
			}
		case <-heartbeatTicker.C:
			a.logVerbosef("Heartbeat tick")
			if err := a.sendHeartbeat(); err != nil {
				log.Printf("Heartbeat failed: %v", err)
			}
		}
	}
}

// Stop stops the agent
func (a *Agent) Stop() {
	close(a.stopChan)

	// Stop all running services
	// Note: ListRunningServices not yet implemented
	// running := a.services.ListRunningServices()
	// for _, serviceID := range running {
	// 	if err := a.services.StopService(serviceID); err != nil {
	// 		log.Printf("Failed to stop service %s: %v", serviceID, err)
	// 	}
	// }

	// Stop proxies
	if a.externalProxy != nil {
		a.externalProxy.Stop()
	}
	if a.internalProxy != nil {
		a.internalProxy.Stop()
	}

	// Cleanup DNS
	if a.dnsMgr != nil {
		a.dnsMgr.Cleanup()
	}

	// Revert firewall rules
	if a.fwMgr != nil && a.applyFirewall {
		if err := a.fwMgr.Revert(); err != nil {
			log.Printf("Failed to revert firewall rules: %v", err)
		}
	}
}

// sync fetches desired state and applies changes
func (a *Agent) sync() error {
	start := time.Now()
	log.Printf("Sync started: stack=%s", a.config.StackID)

	// Fetch desired state
	desired, err := a.api.GetDesiredState(a.config.StackID)
	if err != nil {
		return fmt.Errorf("failed to fetch desired state: %w", err)
	}
	a.logVerbosef("Desired state received: version=%d hash=%s services=%d mode=%s poll_interval=%d heartbeat_interval=%d", desired.Version, desired.Hash, len(desired.Services), desired.SecurityMode, desired.PollInterval, desired.HeartbeatInterval)

	// Update heartbeat interval from desired state (validate range: 30-300 seconds)
	newInterval := desired.HeartbeatInterval
	if newInterval < 30 {
		newInterval = 30
	} else if newInterval > 300 {
		newInterval = 300
	}
	a.heartbeatMu.Lock()
	if a.heartbeatInterval != newInterval {
		log.Printf("Heartbeat interval changed: %d -> %d seconds", a.heartbeatInterval, newInterval)
		a.heartbeatInterval = newInterval
	}
	a.heartbeatMu.Unlock()

	// Check if we need to apply changes
	applied, err := a.state.GetAppliedState()
	if err != nil {
		return fmt.Errorf("failed to get applied state: %w", err)
	}
	stateChanged := applied == nil || applied.StateHash != desired.Hash

	if stateChanged {
		log.Printf("Applying state version %d (hash: %s)", desired.Version, desired.Hash)
	} else {
		log.Printf("Desired state unchanged (hash: %s); reconciling runtime and routes", desired.Hash)
	}

	hadErrors := false
	desiredByID := make(map[string]api.Service)
	for _, svc := range desired.Services {
		if svc.GitRef == "" {
			svc.GitRef = "main"
		}
		desiredByID[svc.ID] = svc
	}

	// Stop services removed from desired state
	if existing, err := a.state.ListServiceProcesses(); err == nil {
		for _, proc := range existing {
			if _, ok := desiredByID[proc.ServiceID]; ok {
				continue
			}
			if err := a.services.StopService(proc.ServiceID); err != nil {
				log.Printf("Failed to stop removed service %s: %v", proc.ServiceID, err)
				hadErrors = true
			}
			if err := a.state.DeleteServiceProcess(proc.ServiceID); err != nil {
				log.Printf("Failed to delete state for service %s: %v", proc.ServiceID, err)
				hadErrors = true
			}
			if err := a.git.RemoveRepo(proc.ServiceID); err != nil {
				log.Printf("Failed to remove repo for service %s: %v", proc.ServiceID, err)
				hadErrors = true
			}
		}
	} else {
		log.Printf("Failed to list existing services: %v", err)
		hadErrors = true
	}

	// Update proxy routes
	externalRoutes := make(map[string]int)
	internalRoutes := make(map[string]int)
	var serviceNames []string

	// Get list of currently running services
	// Note: ListRunningServices not yet implemented
	// running := a.services.ListRunningServices()
	// runningMap := make(map[string]bool)
	// for _, id := range running {
	// 	runningMap[id] = true
	// }

	// Process each service in desired state
	for _, svc := range desired.Services {
		if svc.GitRef == "" {
			svc.GitRef = "main"
		}
		serviceNames = append(serviceNames, svc.Name)

		assignedPort, exists := a.services.GetServicePort(svc.ID)
		if !exists {
			recoveredPort, recovered, recoverErr := a.services.RecoverService(svc)
			if recoverErr != nil {
				log.Printf("Failed to recover service %s: %v", svc.Name, recoverErr)
				hadErrors = true
			} else if recovered {
				assignedPort = recoveredPort
				exists = true
				log.Printf("Recovered running service: name=%s service=%s port=%d", svc.Name, svc.ID, assignedPort)
			}
		}

		proc, _ := a.state.GetServiceProcess(svc.ID)
		needsDeploy := !exists || proc == nil || proc.Status != "running"
		resolvedCommit := ""
		if proc != nil {
			resolvedCommit = proc.GitCommit
		}

		if stateChanged || needsDeploy {
			if !isDockerServiceType(svc.ServiceType) {
				shouldSyncRepo := needsDeploy || proc == nil || strings.TrimSpace(svc.GitCommit) != "" || strings.TrimSpace(resolvedCommit) == ""
				if shouldSyncRepo {
					var err error
					resolvedCommit, err = a.git.CloneOrPull(svc.ID, svc.GitURL, svc.GitRef, svc.GitCommit, svc.GitSSHKey)
					if err != nil {
						a.onServiceLifecycleEvent(svc, "error", "unknown", err.Error())
						log.Printf("Failed to sync repo for service %s: %v", svc.Name, err)
						hadErrors = true
						continue
					}
				}
			} else {
				resolvedCommit = serviceRevisionSignature(svc)
			}
			svc.GitCommit = resolvedCommit

			needsDeploy = needsDeploy || proc == nil || proc.GitCommit != resolvedCommit || proc.Status != "running"

			if needsDeploy {
				a.onServiceLifecycleEvent(svc, "building", "unknown", "")
				log.Printf("Deploying service: name=%s service=%s reason=%s", svc.Name, svc.ID, deployReason(stateChanged, exists, proc, resolvedCommit))
				if err := a.services.DeployService(svc); err != nil {
					a.onServiceLifecycleEvent(svc, "error", "unknown", err.Error())
					log.Printf("Failed to deploy service %s: %v", svc.Name, err)
					hadErrors = true
					continue
				}
			} else {
				a.clearTransientLifecycleStatus(svc.ID)
			}

			assignedPort, exists = a.services.GetServicePort(svc.ID)
			if !exists {
				log.Printf("Warning: no port assigned for service %s after sync", svc.Name)
				hadErrors = true
				continue
			}
		} else {
			a.clearTransientLifecycleStatus(svc.ID)
		}

		if !exists {
			log.Printf("Warning: no port assigned for service %s", svc.Name)
			hadErrors = true
			continue
		}

		// Build routes (hostname-based routing)
		if svc.Hostname != "" {
			externalRoutes[svc.Hostname] = assignedPort
		}
		internalRoutes[svc.Name] = assignedPort
	}

	// Update security mode if changed
	if a.currentMode != desired.SecurityMode {
		a.currentMode = desired.SecurityMode
		if a.applyFirewall {
			if err := a.updateFirewall(desired.SecurityMode, desired.ExternalProxyPort); err != nil {
				log.Printf("Failed to update firewall: %v", err)
			}
		}
	}

	// Update proxy routes
	a.externalProxy.UpdateRoutes(externalRoutes)
	a.internalProxy.UpdateRoutes(internalRoutes)
	log.Printf("Routes updated: external=%d internal=%d services=%d", len(externalRoutes), len(internalRoutes), len(serviceNames))

	// Update DNS entries
	if err := a.dnsMgr.UpdateServices(serviceNames); err != nil {
		log.Printf("Failed to update DNS: %v", err)
	}

	// Record that we applied this state
	if hadErrors {
		return fmt.Errorf("state applied with errors")
	}
	if stateChanged {
		if err := a.state.SetAppliedState(desired.Version, desired.Hash); err != nil {
			return fmt.Errorf("failed to record applied state: %w", err)
		}
	}
	log.Printf("Sync completed: state_changed=%t services=%d elapsed=%s", stateChanged, len(desired.Services), time.Since(start))

	return nil
}

// updateFirewall updates firewall rules based on security mode
func (a *Agent) updateFirewall(mode string, port int) error {
	var securityMode firewall.SecurityMode
	switch mode {
	case "daemon-port":
		securityMode = firewall.SecurityModeDaemonPort
	case "blocked":
		securityMode = firewall.SecurityModeBlocked
	default:
		securityMode = firewall.SecurityModeNone
	}

	a.fwMgr = firewall.NewManager(securityMode, port)

	if securityMode == firewall.SecurityModeNone {
		return nil
	}

	if !a.fwMgr.IsAvailable() {
		log.Println("Warning: UFW not available, firewall rules not applied")
		return nil
	}

	return a.fwMgr.Apply()
}

// sendHeartbeat sends a heartbeat to the control plane
func (a *Agent) sendHeartbeat() error {
	a.heartbeatMu.Lock()
	defer a.heartbeatMu.Unlock()

	start := time.Now()
	log.Printf("Heartbeat started")

	// Get all service statuses
	processes, err := a.state.ListServiceProcesses()
	if err != nil {
		return fmt.Errorf("failed to list processes: %w", err)
	}

	statusByService := make(map[string]api.ServiceStatus)
	for _, proc := range processes {
		// Check if actually running
		status := proc.Status
		if status == "running" {
			if _, err := a.services.GetServiceStatus(proc.ServiceID); err != nil {
				status = "error"
			}
		}

		// Get health check result
		// Note: CheckServiceHealth not yet implemented
		// healthResult, err := a.services.CheckServiceHealth(proc.ServiceID)
		healthStatus := "unknown"
		// if err == nil && healthResult != nil {
		// 	if healthResult.Healthy {
		// 		healthStatus = "healthy"
		// 	} else {
		// 		healthStatus = "unhealthy"
		// 	}
		// }

		statusByService[proc.ServiceID] = api.ServiceStatus{
			ServiceID:    proc.ServiceID,
			Name:         proc.ServiceName,
			Status:       status,
			PID:          proc.PID,
			RestartCount: proc.RestartCount,
			LastError:    proc.LastError,
			HealthStatus: healthStatus,
		}
	}

	a.lifecycleMu.RLock()
	for serviceID, lifecycleStatus := range a.lifecycle {
		if existing, ok := statusByService[serviceID]; !ok || shouldPreferLifecycleStatus(existing.Status, lifecycleStatus.Status) {
			statusByService[serviceID] = lifecycleStatus
		}
	}
	a.lifecycleMu.RUnlock()

	keys := make([]string, 0, len(statusByService))
	for key := range statusByService {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	servicesStatus := make([]api.ServiceStatus, 0, len(keys))
	for _, key := range keys {
		servicesStatus = append(servicesStatus, statusByService[key])
	}

	// Get applied state
	applied, _ := a.state.GetAppliedState()
	stackVersion := 0
	if applied != nil {
		stackVersion = applied.StackVersion
	}

	// Get firewall status
	var fwStatus map[string]interface{}
	if a.fwMgr != nil {
		fwStatus, _ = a.fwMgr.GetStatus()
	}

	req := api.HeartbeatRequest{
		StackVersion:   stackVersion,
		AgentStatus:    "healthy",
		ServicesStatus: servicesStatus,
		SecurityState: map[string]interface{}{
			"mode":              a.currentMode,
			"external_exposure": a.getExternalExposure(),
			"firewall_status":   fwStatus,
		},
		SystemInfo: map[string]interface{}{
			"hostname": getHostname(),
		},
	}

	if err := a.api.SendHeartbeat(req); err != nil {
		return err
	}
	log.Printf("Heartbeat sent: stack_version=%d services=%d elapsed=%s", stackVersion, len(servicesStatus), time.Since(start))
	return nil
}

func (a *Agent) logVerbosef(format string, args ...interface{}) {
	if a.config != nil && a.config.VerboseLogging {
		log.Printf(format, args...)
	}
}

func shouldPreferLifecycleStatus(processStatus, lifecycleStatus string) bool {
	switch strings.TrimSpace(lifecycleStatus) {
	case "building", "deploying", "health_check", "error", "crashed", "stopped":
		return true
	case "running":
		return strings.TrimSpace(processStatus) != "running"
	default:
		return strings.TrimSpace(processStatus) == "" || strings.TrimSpace(processStatus) == "unknown"
	}
}

func (a *Agent) clearTransientLifecycleStatus(serviceID string) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()

	current, exists := a.lifecycle[serviceID]
	if !exists {
		return
	}
	switch strings.TrimSpace(current.Status) {
	case "building", "deploying", "health_check":
		delete(a.lifecycle, serviceID)
	}
}

func (a *Agent) onServiceLifecycleEvent(service api.Service, status, healthStatus, lastError string) {
	a.lifecycleMu.Lock()
	a.lifecycle[service.ID] = api.ServiceStatus{
		ServiceID:    service.ID,
		Name:         service.Name,
		Status:       status,
		RestartCount: 0,
		LastError:    lastError,
		HealthStatus: healthStatus,
	}
	a.lifecycleMu.Unlock()

	log.Printf("Lifecycle update: service=%s name=%s status=%s health=%s", service.ID, service.Name, status, healthStatus)

	go func() {
		if err := a.sendHeartbeat(); err != nil {
			log.Printf("Lifecycle heartbeat failed: service=%s status=%s err=%v", service.ID, status, err)
		} else {
			log.Printf("Lifecycle heartbeat sent: service=%s status=%s", service.ID, status)
		}
	}()
}

func deployReason(stateChanged bool, serviceFound bool, proc *state.ServiceProcess, resolvedCommit string) string {
	if !serviceFound {
		return "not_tracked_in_memory"
	}
	if proc == nil {
		return "no_persisted_process"
	}
	if proc.GitCommit != resolvedCommit {
		return "git_commit_changed"
	}
	if proc.Status != "running" {
		return "persisted_status_not_running"
	}
	if stateChanged {
		return "state_changed"
	}
	return "unknown"
}

func isDockerServiceType(serviceType string) bool {
	return strings.EqualFold(strings.TrimSpace(serviceType), "docker")
}

func serviceRevisionSignature(svc api.Service) string {
	if isDockerServiceType(svc.ServiceType) {
		return fmt.Sprintf("docker:%s|args:%s|cmd:%s", strings.TrimSpace(svc.DockerImage), strings.TrimSpace(svc.DockerRunArgs), strings.TrimSpace(svc.RunCommand))
	}
	return svc.GitCommit
}

func (a *Agent) getExternalExposure() string {
	switch a.currentMode {
	case "blocked":
		return "none"
	case "daemon-port":
		return "daemon-port"
	default:
		return "unrestricted"
	}
}

func getHostname() string {
	hostname, _ := os.Hostname()
	return hostname
}

// printServiceStatus displays the current status of all services from the state database
func printServiceStatus(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	stateMgr, err := state.NewManager(cfg.StateDBPath())
	if err != nil {
		return fmt.Errorf("failed to initialize state: %w", err)
	}
	defer stateMgr.Close()

	processes, err := stateMgr.ListServiceProcesses()
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	if len(processes) == 0 {
		fmt.Println("No services configured")
		return nil
	}

	fmt.Printf("%-20s %-12s %-8s %-15s %-20s\n", "SERVICE", "STATUS", "PID", "RUNTIME", "COMMIT")
	fmt.Println("-------------------------------------------------------------------------------------------")

	for _, proc := range processes {
		pid := "-"
		if proc.PID > 0 {
			pid = fmt.Sprintf("%d", proc.PID)
		}
		commit := proc.GitCommit
		if len(commit) > 8 {
			commit = commit[:8]
		}
		fmt.Printf("%-20s %-12s %-8s %-15s %-20s\n",
			truncate(proc.ServiceName, 20),
			proc.Status,
			pid,
			proc.Runtime,
			commit)
	}

	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// handleAddSecret adds a new secret for a service
func handleAddSecret(configPath, serviceID, name, value string) error {
	if serviceID == "" {
		return fmt.Errorf("service ID is required (use -service flag)")
	}
	if name == "" {
		return fmt.Errorf("secret name is required (use -secret-name flag)")
	}

	// If value not provided via flag, prompt for it
	if value == "" {
		fmt.Print("Enter secret value: ")
		reader := bufio.NewReader(os.Stdin)
		val, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read secret value: %w", err)
		}
		value = strings.TrimSpace(val)
	}

	if value == "" {
		return fmt.Errorf("secret value cannot be empty")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	secretsMgr, err := secrets.NewManager(cfg.SecretsPath(), cfg.AgentID)
	if err != nil {
		return fmt.Errorf("failed to initialize secrets manager: %w", err)
	}

	if err := secretsMgr.SetSecret(name, serviceID, value); err != nil {
		return fmt.Errorf("failed to store secret: %w", err)
	}

	fmt.Printf("✓ Secret '%s' added for service '%s'\n", name, serviceID)
	fmt.Println("Note: The service will need to be restarted to use the new secret.")
	return nil
}

// handleListSecrets lists all secrets for a service
func handleListSecrets(configPath, serviceID string) error {
	if serviceID == "" {
		return fmt.Errorf("service ID is required (use -service flag)")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	secretsMgr, err := secrets.NewManager(cfg.SecretsPath(), cfg.AgentID)
	if err != nil {
		return fmt.Errorf("failed to initialize secrets manager: %w", err)
	}

	secretNames, err := secretsMgr.ListSecrets(serviceID)
	if err != nil {
		return fmt.Errorf("failed to list secrets: %w", err)
	}

	if len(secretNames) == 0 {
		fmt.Printf("No secrets configured for service '%s'\n", serviceID)
		return nil
	}

	fmt.Printf("Secrets for service '%s':\n", serviceID)
	for _, name := range secretNames {
		fmt.Printf("  - %s\n", name)
	}
	fmt.Println("\nNote: Secret values are encrypted and cannot be displayed.")
	return nil
}

// handleDeleteSecret removes a secret
func handleDeleteSecret(configPath, serviceID, name string) error {
	if serviceID == "" {
		return fmt.Errorf("service ID is required (use -service flag)")
	}
	if name == "" {
		return fmt.Errorf("secret name is required (use -secret-name flag)")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	secretsMgr, err := secrets.NewManager(cfg.SecretsPath(), cfg.AgentID)
	if err != nil {
		return fmt.Errorf("failed to initialize secrets manager: %w", err)
	}

	if err := secretsMgr.DeleteSecret(name, serviceID); err != nil {
		return fmt.Errorf("failed to delete secret: %w", err)
	}

	fmt.Printf("✓ Secret '%s' deleted for service '%s'\n", name, serviceID)
	return nil
}

// handleShowLogs shows logs for a service
func handleShowLogs(configPath, serviceID string, follow bool) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	stateMgr, err := state.NewManager(cfg.StateDBPath())
	if err != nil {
		return fmt.Errorf("failed to initialize state: %w", err)
	}
	defer stateMgr.Close()

	// If no service specified, show all services
	if serviceID == "" {
		processes, err := stateMgr.ListServiceProcesses()
		if err != nil {
			return fmt.Errorf("failed to list services: %w", err)
		}

		if len(processes) == 0 {
			fmt.Println("No services configured")
			return nil
		}

		fmt.Println("Available services:")
		for _, proc := range processes {
			fmt.Printf("  - %s (%s)\n", proc.ServiceName, proc.ServiceID)
		}
		fmt.Println("\nUse -log-service <service-id> to view logs for a specific service")
		return nil
	}

	// Show logs for specific service
	if follow {
		fmt.Printf("Following logs for service '%s' (Ctrl+C to exit)...\n", serviceID)
		var lastID int64 = 0
		for {
			logs, err := stateMgr.StreamLogs(serviceID, lastID)
			if err != nil {
				return fmt.Errorf("failed to stream logs: %w", err)
			}

			for _, log := range logs {
				fmt.Printf("[%s] %s: %s\n", log.CreatedAt.Format("2006-01-02 15:04:05"), log.Level, log.Message)
				lastID = log.ID
			}

			time.Sleep(1 * time.Second)
		}
	} else {
		logs, err := stateMgr.GetServiceLogs(serviceID, 100)
		if err != nil {
			return fmt.Errorf("failed to get logs: %w", err)
		}

		if len(logs) == 0 {
			fmt.Printf("No logs found for service '%s'\n", serviceID)
			return nil
		}

		// Reverse to show oldest first
		for i := len(logs) - 1; i >= 0; i-- {
			log := logs[i]
			fmt.Printf("[%s] %s: %s\n", log.CreatedAt.Format("2006-01-02 15:04:05"), log.Level, log.Message)
		}
	}

	return nil
}
