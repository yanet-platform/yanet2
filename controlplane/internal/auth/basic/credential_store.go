package basic

import (
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/rcucache"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

type UserName = string
type PasswordHash = string

// CredentialStore provides access to credentials for authentication.
type CredentialStore interface {
	VerifyCredentials(username, password string) error
}

// FileCredentialStore loads credentials from a YAML file.
type FileCredentialStore struct {
	path string

	credentials *rcucache.Cache[UserName, PasswordHash] // username -> bcrypt hash
}

// NewFileCredentialStore creates a new FileCredentialStore.
func NewFileCredentialStore(path string) (*FileCredentialStore, error) {
	m := &FileCredentialStore{
		path:        path,
		credentials: rcucache.NewEmptyCache[UserName, PasswordHash](),
	}
	if err := m.load(); err != nil {
		return nil, fmt.Errorf("failed to load credentials: %w", err)
	}
	return m, nil
}

// VerifyCredentials checks if the password matches the stored hash.
func (m *FileCredentialStore) VerifyCredentials(username, password string) error {
	view := m.credentials.View()
	hash, ok := view.Lookup(username)
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

	m.credentials.Swap(newCreds)

	return nil
}
