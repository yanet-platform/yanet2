package identity

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/rcucache"
)

type UserName = string

// FileIdentityProvider loads identities from a YAML file.
type FileIdentityProvider struct {
	path string

	identities *rcucache.Cache[UserName, Identity]
}

// NewFileIdentityProvider creates a new FileIdentityProvider.
func NewFileIdentityProvider(path string) (*FileIdentityProvider, error) {
	m := &FileIdentityProvider{
		path:       path,
		identities: rcucache.NewEmptyCache[UserName, Identity](),
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
	view := m.identities.View()
	identity, ok := view.Lookup(username)
	if !ok {
		return Identity{}, ErrIdentityNotFound
	}

	return identity.Clone(), nil
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
	identities := map[string]Identity{}
	for _, identity := range file.Identities {
		if identity.Username == "" {
			return fmt.Errorf("identity with empty username")
		}

		identities[identity.Username] = identity
	}

	m.identities.Swap(identities)

	return nil
}
