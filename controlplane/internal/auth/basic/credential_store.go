package basic

import (
	"fmt"
	"os"
	"sync"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

// CredentialStore provides access to credentials for authentication.
type CredentialStore interface {
	VerifyCredentials(username, password string) error
}

// FileCredentialStore loads credentials from a YAML file.
type FileCredentialStore struct {
	path string

	mu          sync.RWMutex
	credentials map[string]string // username -> bcrypt hash
}

// NewFileCredentialStore creates a new FileCredentialStore.
func NewFileCredentialStore(path string) (*FileCredentialStore, error) {
	m := &FileCredentialStore{
		path:        path,
		credentials: map[string]string{},
	}
	if err := m.load(); err != nil {
		return nil, fmt.Errorf("failed to load credentials: %w", err)
	}
	return m, nil
}

// VerifyCredentials checks if the password matches the stored hash.
func (m *FileCredentialStore) VerifyCredentials(username, password string) error {
	m.mu.RLock()
	hash, ok := m.credentials[username]
	m.mu.RUnlock()

	if !ok {
		return core.ErrInvalidCredentials
	}

	// Bcrypt comparison (constant-time).
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return core.ErrInvalidCredentials
	}

	return nil
}

// load reads and parses the credentials file.
func (m *FileCredentialStore) load() error {
	buf, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	var file struct {
		Credentials []struct {
			Username     string `yaml:"username"`
			PasswordHash string `yaml:"password_hash"`
		} `yaml:"credentials"`
	}

	if err := yaml.Unmarshal(buf, &file); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Build username index.
	newCreds := map[string]string{}
	for _, entry := range file.Credentials {
		if entry.Username == "" {
			return fmt.Errorf("credential entry with empty username")
		}
		// Validate bcrypt hash format.
		if _, err := bcrypt.Cost([]byte(entry.PasswordHash)); err != nil {
			return fmt.Errorf("invalid bcrypt hash for user %q: %w", entry.Username, err)
		}
		newCreds[entry.Username] = entry.PasswordHash
	}

	m.mu.Lock()
	m.credentials = newCreds
	m.mu.Unlock()

	return nil
}
