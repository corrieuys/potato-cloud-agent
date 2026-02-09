package firewall

import (
	"fmt"
	"os/exec"
	"strings"
)

// SecurityMode represents the firewall security mode
type SecurityMode string

const (
	// SecurityModeNone - No firewall changes
	SecurityModeNone SecurityMode = "none"
	// SecurityModeDaemonPort - Allow inbound to daemon port only
	SecurityModeDaemonPort SecurityMode = "daemon-port"
	// SecurityModeBlocked - Block all inbound (tunnel mode)
	SecurityModeBlocked SecurityMode = "blocked"
)

// Manager handles firewall configuration
type Manager struct {
	mode       SecurityMode
	daemonPort int
	sshPort    int
	sshCIDR    string
}

// NewManager creates a new firewall manager
func NewManager(mode SecurityMode, daemonPort int) *Manager {
	return &Manager{
		mode:       mode,
		daemonPort: daemonPort,
		sshPort:    22,
		sshCIDR:    "", // Empty means allow from anywhere
	}
}

// SetSSHRestrictions sets SSH access restrictions
func (m *Manager) SetSSHRestrictions(port int, cidr string) {
	m.sshPort = port
	m.sshCIDR = cidr
}

// Apply applies the firewall rules based on security mode
func (m *Manager) Apply() error {
	switch m.mode {
	case SecurityModeNone:
		return m.applyNone()
	case SecurityModeDaemonPort:
		return m.applyDaemonPort()
	case SecurityModeBlocked:
		return m.applyBlocked()
	default:
		return fmt.Errorf("unknown security mode: %s", m.mode)
	}
}

// applyNone doesn't modify firewall rules
func (m *Manager) applyNone() error {
	// No changes to firewall
	return nil
}

// applyDaemonPort allows only daemon port and optional SSH
func (m *Manager) applyDaemonPort() error {
	// Reset UFW to default
	if err := m.resetUFW(); err != nil {
		return fmt.Errorf("failed to reset UFW: %w", err)
	}

	// Set default policies
	if err := m.runUFW("default", "deny", "incoming"); err != nil {
		return err
	}
	if err := m.runUFW("default", "allow", "outgoing"); err != nil {
		return err
	}

	// Allow loopback
	if err := m.runUFW("allow", "in", "on", "lo"); err != nil {
		return err
	}

	// Allow daemon port
	if err := m.runUFW("allow", fmt.Sprintf("%d/tcp", m.daemonPort)); err != nil {
		return fmt.Errorf("failed to allow daemon port: %w", err)
	}

	// Allow SSH if configured
	if m.sshPort > 0 {
		if m.sshCIDR != "" {
			if err := m.runUFW("allow", "from", m.sshCIDR, "to", "any", "port", fmt.Sprintf("%d", m.sshPort)); err != nil {
				return fmt.Errorf("failed to allow SSH: %w", err)
			}
		} else {
			if err := m.runUFW("allow", fmt.Sprintf("%d/tcp", m.sshPort)); err != nil {
				return fmt.Errorf("failed to allow SSH: %w", err)
			}
		}
	}

	// Enable UFW
	if err := m.runUFW("--force", "enable"); err != nil {
		return fmt.Errorf("failed to enable UFW: %w", err)
	}

	return nil
}

// applyBlocked blocks all inbound traffic
func (m *Manager) applyBlocked() error {
	// Reset UFW to default
	if err := m.resetUFW(); err != nil {
		return fmt.Errorf("failed to reset UFW: %w", err)
	}

	// Set default policies
	if err := m.runUFW("default", "deny", "incoming"); err != nil {
		return err
	}
	if err := m.runUFW("default", "allow", "outgoing"); err != nil {
		return err
	}

	// Allow loopback only
	if err := m.runUFW("allow", "in", "on", "lo"); err != nil {
		return err
	}

	// Allow SSH if in development (optional)
	if m.sshPort > 0 && m.sshCIDR != "" {
		if err := m.runUFW("allow", "from", m.sshCIDR, "to", "any", "port", fmt.Sprintf("%d", m.sshPort)); err != nil {
			return fmt.Errorf("failed to allow SSH: %w", err)
		}
	}

	// Enable UFW
	if err := m.runUFW("--force", "enable"); err != nil {
		return fmt.Errorf("failed to enable UFW: %w", err)
	}

	return nil
}

// Revert removes all firewall rules
func (m *Manager) Revert() error {
	return m.resetUFW()
}

// resetUFW resets UFW to default state
func (m *Manager) resetUFW() error {
	// Disable UFW first
	exec.Command("ufw", "--force", "disable").Run()

	// Reset to defaults
	if err := m.runUFW("--force", "reset"); err != nil {
		// Reset might fail if UFW is not installed, that's ok
		if !strings.Contains(err.Error(), "not found") {
			return err
		}
	}

	return nil
}

// runUFW executes a UFW command
func (m *Manager) runUFW(args ...string) error {
	cmd := exec.Command("ufw", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ufw %s failed: %w (output: %s)",
			strings.Join(args, " "), err, string(output))
	}
	return nil
}

// IsAvailable checks if UFW is available on the system
func (m *Manager) IsAvailable() bool {
	cmd := exec.Command("which", "ufw")
	err := cmd.Run()
	return err == nil
}

// GetStatus returns the current firewall status
func (m *Manager) GetStatus() (map[string]interface{}, error) {
	cmd := exec.Command("ufw", "status", "verbose")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get UFW status: %w", err)
	}

	return map[string]interface{}{
		"mode":   m.mode,
		"status": string(output),
	}, nil
}
