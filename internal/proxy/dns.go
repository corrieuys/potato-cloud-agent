package proxy

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DNSManager manages local DNS entries for svc.internal domains
// For MVP Phase 0, this updates /etc/hosts instead of running a DNS server
type DNSManager struct {
	hostsFile string
	services  map[string]bool
}

// NewDNSManager creates a new DNS manager
func NewDNSManager() *DNSManager {
	return &DNSManager{
		hostsFile: "/etc/hosts",
		services:  make(map[string]bool),
	}
}

// atomicWriteFile writes content to a temp file then renames it into place
// for crash-safe updates.
func atomicWriteFile(path string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("failed to chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}
	return nil
}

// UpdateServices updates the DNS entries for services
func (d *DNSManager) UpdateServices(serviceNames []string) error {
	// Read current hosts file
	content, err := os.ReadFile(d.hostsFile)
	if err != nil {
		return fmt.Errorf("failed to read hosts file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	var newLines []string

	// Keep lines that aren't managed by us
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			newLines = append(newLines, line)
			continue
		}

		// Check if this is one of our svc.internal entries
		if !strings.Contains(line, ".svc.internal") {
			newLines = append(newLines, line)
		}
	}

	// Add our marker and entries
	newLines = append(newLines, "")
	newLines = append(newLines, "# BuildVigil svc.internal entries")
	for _, name := range serviceNames {
		newLines = append(newLines, fmt.Sprintf("127.0.0.1 %s.svc.internal", name))
	}

	// Atomic write
	newContent := strings.Join(newLines, "\n")
	if err := atomicWriteFile(d.hostsFile, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write hosts file: %w", err)
	}

	return nil
}

// Cleanup removes all BuildVigil DNS entries
func (d *DNSManager) Cleanup() error {
	content, err := os.ReadFile(d.hostsFile)
	if err != nil {
		return fmt.Errorf("failed to read hosts file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	var newLines []string
	skipSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.Contains(trimmed, "# BuildVigil svc.internal entries") {
			skipSection = true
			continue
		}

		if skipSection {
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				skipSection = false
			}
			continue
		}

		newLines = append(newLines, line)
	}

	newContent := strings.Join(newLines, "\n")
	return atomicWriteFile(d.hostsFile, []byte(newContent), 0644)
}

// ReadHosts reads and parses the hosts file
func (d *DNSManager) ReadHosts() (map[string]string, error) {
	file, err := os.Open(d.hostsFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	entries := make(map[string]string)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) >= 2 {
			ip := fields[0]
			for i := 1; i < len(fields); i++ {
				entries[fields[i]] = ip
			}
		}
	}

	return entries, scanner.Err()
}
