package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// GenerateSSHKeyPair creates an ed25519 keypair using ssh-keygen and returns the public key.
func GenerateSSHKeyPair(keysDir string, keyName string) (string, string, error) {
	if keyName == "" {
		return "", "", fmt.Errorf("key name is required")
	}

	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		return "", "", fmt.Errorf("ssh-keygen not found in PATH")
	}

	if err := os.MkdirAll(keysDir, 0700); err != nil {
		return "", "", fmt.Errorf("failed to create keys directory: %w", err)
	}

	privateKeyPath := filepath.Join(keysDir, keyName)
	publicKeyPath := privateKeyPath + ".pub"

	if _, err := os.Stat(privateKeyPath); err == nil {
		return "", "", fmt.Errorf("key already exists: %s", privateKeyPath)
	}

	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", privateKeyPath, "-N", "", "-C", "buildvigil")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("ssh-keygen failed: %w", err)
	}

	pubBytes, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to read public key: %w", err)
	}

	return string(pubBytes), privateKeyPath, nil
}
