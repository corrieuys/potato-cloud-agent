package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Manager handles secure storage of secrets on the agent
type Manager struct {
	secretsDir string
	key        []byte
}

// Secret represents a stored secret
type Secret struct {
	Name      string `json:"name"`
	ServiceID string `json:"service_id"`
	Value     string `json:"value"`
}

// NewManager creates a new secrets manager
func NewManager(secretsDir string, agentID string) (*Manager, error) {
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create secrets directory: %w", err)
	}

	// Derive encryption key from agent ID
	// This means secrets can only be decrypted by this specific agent
	hash := sha256.Sum256([]byte(agentID + "-buildvigil-secret-key"))

	return &Manager{
		secretsDir: secretsDir,
		key:        hash[:],
	}, nil
}

// SetSecret stores a secret encrypted on disk
func (m *Manager) SetSecret(name, serviceID, value string) error {
	secret := Secret{
		Name:      name,
		ServiceID: serviceID,
		Value:     value,
	}

	data, err := json.Marshal(secret)
	if err != nil {
		return fmt.Errorf("failed to marshal secret: %w", err)
	}

	encrypted, err := m.encrypt(data)
	if err != nil {
		return fmt.Errorf("failed to encrypt secret: %w", err)
	}

	filename := m.getSecretFilename(name, serviceID)
	if err := os.WriteFile(filename, []byte(encrypted), 0600); err != nil {
		return fmt.Errorf("failed to write secret file: %w", err)
	}

	return nil
}

// GetSecret retrieves a decrypted secret
func (m *Manager) GetSecret(name, serviceID string) (string, error) {
	filename := m.getSecretFilename(name, serviceID)

	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("secret not found: %s", name)
		}
		return "", fmt.Errorf("failed to read secret file: %w", err)
	}

	decrypted, err := m.decrypt(string(data))
	if err != nil {
		return "", fmt.Errorf("failed to decrypt secret: %w", err)
	}

	var secret Secret
	if err := json.Unmarshal(decrypted, &secret); err != nil {
		return "", fmt.Errorf("failed to unmarshal secret: %w", err)
	}

	return secret.Value, nil
}

// DeleteSecret removes a secret
func (m *Manager) DeleteSecret(name, serviceID string) error {
	filename := m.getSecretFilename(name, serviceID)
	if err := os.Remove(filename); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("secret not found: %s", name)
		}
		return fmt.Errorf("failed to delete secret file: %w", err)
	}
	return nil
}

// ListSecrets returns all secret names for a service
func (m *Manager) ListSecrets(serviceID string) ([]string, error) {
	pattern := filepath.Join(m.secretsDir, fmt.Sprintf("%s.*.secret", serviceID))
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	var names []string
	for _, match := range matches {
		// Extract name from filename: {serviceID}.{name}.secret
		base := filepath.Base(match)
		// Remove serviceID prefix and .secret suffix
		name := base[len(serviceID)+1 : len(base)-7]
		names = append(names, name)
	}

	return names, nil
}

// GetAllSecretsForService returns all secrets for a service as a map
func (m *Manager) GetAllSecretsForService(serviceID string) (map[string]string, error) {
	names, err := m.ListSecrets(serviceID)
	if err != nil {
		return nil, err
	}

	secrets := make(map[string]string)
	for _, name := range names {
		value, err := m.GetSecret(name, serviceID)
		if err != nil {
			continue // Skip secrets we can't read
		}
		secrets[name] = value
	}

	return secrets, nil
}

// getSecretFilename returns the filename for a secret
func (m *Manager) getSecretFilename(name, serviceID string) string {
	return filepath.Join(m.secretsDir, fmt.Sprintf("%s.%s.secret", serviceID, name))
}

// encrypt encrypts data using AES-GCM
func (m *Manager) encrypt(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(m.key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decrypt decrypts data using AES-GCM
func (m *Manager) decrypt(ciphertext string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(m.key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]
	return gcm.Open(nil, nonce, ciphertextBytes, nil)
}
