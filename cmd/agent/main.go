package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
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
	"github.com/buildvigil/agent/internal/tunnel"
)

func main() {
	var (
		registerToken = flag.String("register", "", "Install token for initial registration")
		genSSHKey     = flag.Bool("gen-ssh-key", false, "Generate an SSH keypair for git access")
		sshKeyName    = flag.String("ssh-key-name", "default", "SSH key name to generate (filename under ssh dir)")
		configPath    = flag.String("config", config.ConfigPath(), "Path to config file")
		controlPlane  = flag.String("control-plane", "http://localhost:8787", "Control plane URL")
		stackID       = flag.String("stack-id", "", "Stack ID (required for registration)")
		setAPIKey     = flag.String("set-api-key", "", "Update stored API key in agent config")
		applyFirewall = flag.Bool("apply-firewall", false, "Apply firewall rules (requires root)")
		showStatus    = flag.Bool("status", false, "Show current service status")

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
	flag.Parse()

	if *registerToken != "" {
		if err := doRegistration(*controlPlane, *stackID, *registerToken, *configPath); err != nil {
			log.Fatalf("Registration failed: %v", err)
		}
		fmt.Println("Registration successful. Agent is configured.")
		return
	}

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

	if *setAPIKey != "" {
		if err := handleSetAPIKey(*configPath, *setAPIKey); err != nil {
			log.Fatalf("Failed to update API key: %v", err)
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
	apiClient := api.NewClient(cfg.ControlPlane, cfg.APIKey)

	// Initialize firewall manager (will be configured after first sync)
	var fwMgr *firewall.Manager

	// Initialize tunnel manager if Cloudflare config is available
	var tunnelMgr *tunnel.CloudflareTunnel
	if cfg.HasCloudflareConfig() {
		tunnelMgr = tunnel.NewCloudflareTunnel(cfg.TunnelConfigPath(), cfg.GetCloudflareCredentials())
		log.Printf("Cloudflare tunnel support enabled")
	} else {
		log.Printf("Cloudflare tunnel support disabled (no configuration)")
	}

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
		tunnelMgr:     tunnelMgr,
		applyFirewall: *applyFirewall,
		healthPort:    9090, // Health check server port
	}

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

// doRegistration handles the initial agent registration
func doRegistration(controlPlane, stackID, installToken, configPath string) error {
	if strings.TrimSpace(stackID) == "" {
		return fmt.Errorf("stack ID is required (use -stack-id)")
	}

	// Get system info
	hostname, _ := os.Hostname()
	ipAddress := getLocalIP()

	// Register with control plane
	req := api.RegistrationRequest{
		InstallToken: installToken,
		Hostname:     hostname,
		IPAddress:    ipAddress,
	}

	resp, err := api.Register(controlPlane, stackID, req)
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	// Save configuration
	cfg := &config.Config{
		AgentID:           resp.AgentID,
		APIKey:            resp.APIKey,
		StackID:           resp.StackID,
		ControlPlane:      controlPlane,
		PollInterval:      resp.PollInterval,
		DataDir:           "/var/lib/potato-cloud",
		ExternalProxyPort: 8080,
		SecurityMode:      "none",
	}

	if err := cfg.Save(configPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}

// getLocalIP returns the local IP address
func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}

	return ""
}

// Agent is the main agent structure
type Agent struct {
	config        *config.Config
	state         *state.Manager
	git           *git.Manager
	services      *service.Manager
	api           *api.Client
	externalProxy *proxy.ExternalProxy
	internalProxy *proxy.InternalProxy
	dnsMgr        *proxy.DNSManager
	fwMgr         *firewall.Manager
	tunnelMgr     *tunnel.CloudflareTunnel
	stopChan      chan struct{}
	applyFirewall bool
	currentMode   string
	healthPort    int
}

// Run starts the agent main loop
func (a *Agent) Run() {
	a.stopChan = make(chan struct{})

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

	// Start heartbeat loop (every 30 seconds)
	heartbeatTicker := time.NewTicker(30 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-a.stopChan:
			return
		case <-ticker.C:
			if err := a.sync(); err != nil {
				log.Printf("Sync failed: %v", err)
			}
		case <-heartbeatTicker.C:
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

	// Stop tunnel
	if a.tunnelMgr != nil {
		if err := a.tunnelMgr.Stop(); err != nil {
			log.Printf("Failed to stop tunnel: %v", err)
		}
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
	// Fetch desired state
	desired, err := a.api.GetDesiredState(a.config.StackID)
	if err != nil {
		return fmt.Errorf("failed to fetch desired state: %w", err)
	}

	// Check if we need to apply changes
	applied, err := a.state.GetAppliedState()
	if err != nil {
		return fmt.Errorf("failed to get applied state: %w", err)
	}

	if applied != nil && applied.StateHash == desired.Hash {
		// No changes needed
		return nil
	}

	log.Printf("Applying state version %d (hash: %s)", desired.Version, desired.Hash)

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

		resolvedCommit, err := a.git.CloneOrPull(svc.ID, svc.GitURL, svc.GitRef, svc.GitCommit, svc.GitSSHKey)
		if err != nil {
			log.Printf("Failed to sync repo for service %s: %v", svc.Name, err)
			hadErrors = true
			continue
		}
		svc.GitCommit = resolvedCommit

		proc, _ := a.state.GetServiceProcess(svc.ID)
		needsDeploy := true
		if proc != nil && proc.GitCommit == resolvedCommit && proc.Status == "running" {
			needsDeploy = false
		}

		if needsDeploy {
			// repoPath := a.git.GetRepoPath(svc.ID)
			if err := a.services.DeployService(svc); err != nil {
				log.Printf("Failed to deploy service %s: %v", svc.Name, err)
				hadErrors = true
				continue
			}
		}

		serviceNames = append(serviceNames, svc.Name)

		// Get assigned port for routing
		assignedPort, exists := a.services.GetServicePort(svc.ID)
		if !exists {
			log.Printf("Warning: no port assigned for service %s", svc.Name)
			continue
		}

		// Build routes
		if svc.ExternalPath != "" {
			externalRoutes[svc.ExternalPath] = assignedPort
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

	// Update DNS entries
	if err := a.dnsMgr.UpdateServices(serviceNames); err != nil {
		log.Printf("Failed to update DNS: %v", err)
	}

	// Update tunnel configuration if available
	if a.tunnelMgr != nil {
		if err := a.updateTunnel(desired.Services); err != nil {
			log.Printf("Failed to update tunnel: %v", err)
		}
	}

	// Record that we applied this state
	if !hadErrors {
		if err := a.state.SetAppliedState(desired.Version, desired.Hash); err != nil {
			return fmt.Errorf("failed to record applied state: %w", err)
		}
	} else {
		return fmt.Errorf("state applied with errors")
	}

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
	// Get all service statuses
	processes, err := a.state.ListServiceProcesses()
	if err != nil {
		return fmt.Errorf("failed to list processes: %w", err)
	}

	var servicesStatus []api.ServiceStatus
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

		servicesStatus = append(servicesStatus, api.ServiceStatus{
			ServiceID:    proc.ServiceID,
			Name:         proc.ServiceName,
			Status:       status,
			PID:          proc.PID,
			RestartCount: proc.RestartCount,
			LastError:    proc.LastError,
			HealthStatus: healthStatus,
		})
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

	// Get tunnel status
	tunnelConnected := false
	if a.tunnelMgr != nil {
		tunnelConnected = a.tunnelMgr.IsConnected()
	}

	req := api.HeartbeatRequest{
		StackVersion:   stackVersion,
		AgentStatus:    "healthy",
		ServicesStatus: servicesStatus,
		SecurityState: map[string]interface{}{
			"mode":              a.currentMode,
			"external_exposure": a.getExternalExposure(),
			"tunnel_connected":  tunnelConnected,
			"firewall_status":   fwStatus,
		},
		SystemInfo: map[string]interface{}{
			"hostname": getHostname(),
		},
	}

	return a.api.SendHeartbeat(req)
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

// updateTunnel updates the Cloudflare tunnel configuration
func (a *Agent) updateTunnel(services []api.Service) error {
	if a.tunnelMgr == nil {
		return nil
	}

	// Check if cloudflared is available
	if !tunnel.IsCloudflaredAvailable() {
		log.Printf("Cloudflared not found, tunnel management disabled")
		return nil
	}

	// Create tunnel if needed
	if a.tunnelMgr.GetStatus()["tunnel_id"] == "" {
		log.Printf("Creating Cloudflare tunnel...")
		if err := a.tunnelMgr.CreateTunnel("buildvigil-agent-"+a.config.AgentID, ""); err != nil {
			return fmt.Errorf("failed to create tunnel: %w", err)
		}
	}

	// Prepare service configs for tunnel
	var tunnelServices []tunnel.ServiceConfig
	for _, svc := range services {
		// Get the assigned port for this service
		assignedPort, exists := a.services.GetServicePort(svc.ID)
		if !exists {
			continue
		}

		if svc.ExternalPath != "" && assignedPort > 0 {
			// For now, use service name as hostname
			// In a real implementation, this would come from desired state
			hostname := fmt.Sprintf("%s.%s.tunnel.buildvigil.dev", svc.Name, a.config.AgentID)
			tunnelServices = append(tunnelServices, tunnel.ServiceConfig{
				Name:     svc.Name,
				Port:     assignedPort,
				Hostname: hostname,
			})
		}
	}

	// Write tunnel configuration
	if len(tunnelServices) > 0 {
		log.Printf("Updating tunnel configuration with %d services", len(tunnelServices))
		if err := a.tunnelMgr.WriteConfig(tunnelServices); err != nil {
			return fmt.Errorf("failed to write tunnel config: %w", err)
		}

		// Start tunnel if not running
		if !a.tunnelMgr.IsConnected() {
			log.Printf("Starting Cloudflare tunnel...")
			if err := a.tunnelMgr.Start(); err != nil {
				return fmt.Errorf("failed to start tunnel: %w", err)
			}
		}
	} else {
		// Stop tunnel if no services need exposure
		if a.tunnelMgr.IsConnected() {
			log.Printf("Stopping Cloudflare tunnel (no services to expose)")
			if err := a.tunnelMgr.Stop(); err != nil {
				return fmt.Errorf("failed to stop tunnel: %w", err)
			}
		}
	}

	return nil
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

// handleSetAPIKey updates the stored API key in the agent config file.
func handleSetAPIKey(configPath, apiKey string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return fmt.Errorf("API key cannot be empty")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	cfg.APIKey = apiKey
	if err := cfg.Save(configPath); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Println("✓ API key updated in config")
	fmt.Println("Note: Restart the agent service to apply the new key.")
	return nil
}
