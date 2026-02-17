package identity

import (
	"context"
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// FileIdentityProvider loads identities from a YAML file.
type FileIdentityProvider struct {
	path string

	mu         sync.RWMutex
	identities map[string]*Identity // username -> Identity
}

// NewFileIdentityProvider creates a new FileIdentityProvider.
func NewFileIdentityProvider(path string) (*FileIdentityProvider, error) {
	m := &FileIdentityProvider{
		path:       path,
		identities: map[string]*Identity{},
	}
	if err := m.load(); err != nil {
		return nil, fmt.Errorf("failed to load identities: %w", err)
	}
	return m, nil
}

// Name returns the provider name for logging.
func (m *FileIdentityProvider) Name() string {
	return "file"
}

// GetIdentity retrieves an identity by username.
func (m *FileIdentityProvider) GetIdentity(ctx context.Context, username string) (Identity, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	identity, ok := m.identities[username]
	if !ok {
		return Identity{}, ErrIdentityNotFound
	}

	return Identity{
		Username: identity.Username,
		Groups:   append([]string{}, identity.Groups...),
		Disabled: identity.Disabled,
	}, nil
}

// load reads and parses the identities file.
func (m *FileIdentityProvider) load() error {
	buf, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	var file struct {
		Identities []Identity `yaml:"identities"`
	}

	if err := yaml.Unmarshal(buf, &file); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Build username index.
	newIdentities := map[string]*Identity{}
	for i := range file.Identities {
		identity := &file.Identities[i]
		if identity.Username == "" {
			return fmt.Errorf("identity with empty username")
		}
		newIdentities[identity.Username] = identity
	}

	m.mu.Lock()
	m.identities = newIdentities
	m.mu.Unlock()

	return nil
}
