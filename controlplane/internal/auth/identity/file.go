package identity

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/rcucache"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

type userName = string

// IdentityProvider keeps identities indexed by username.
type IdentityProvider struct {
	identities *rcucache.Cache[userName, Identity]
}

// NewIdentityProvider creates a IdentityProvider from an in-memory map of
// identities.
func NewIdentityProvider(identities map[userName]Identity) *IdentityProvider {
	return &IdentityProvider{
		identities: rcucache.NewCache(identities),
	}
}

// NewIdentityProviderFromFile creates a IdentityProvider by loading identities
// from a YAML file.
func NewIdentityProviderFromFile(path string) (*IdentityProvider, error) {
	identities, err := loadIdentitiesFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load identities: %w", err)
	}

	return NewIdentityProvider(identities), nil
}

// Name returns the provider name for logging.
func (m *IdentityProvider) Name() string {
	return "file"
}

// ResolveIdentity retrieves an identity by the subject's account lookup name.
func (m *IdentityProvider) ResolveIdentity(
	ctx context.Context,
	subject core.Subject,
) (Identity, error) {
	if subject.Login == "" {
		return Identity{}, ErrSubjectUnsupported
	}

	view := m.identities.View()
	identity, ok := view.Lookup(subject.Login)
	if !ok {
		return Identity{}, ErrIdentityNotFound
	}

	return identity.Clone(), nil
}

// loadIdentitiesFile reads and parses the identities YAML file into a map.
func loadIdentitiesFile(path string) (map[string]Identity, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var file struct {
		Identities []Identity `yaml:"identities"`
	}

	if err := yaml.Unmarshal(buf, &file); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Build username index.
	identities := map[string]Identity{}
	for _, identity := range file.Identities {
		if identity.Username == "" {
			return nil, fmt.Errorf("identity with empty username")
		}

		identities[identity.Username] = identity
	}

	return identities, nil
}
