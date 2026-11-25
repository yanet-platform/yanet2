package identity

import (
	"context"
	"fmt"
	"os"

	"github.com/yanet-platform/yanet2/common/go/rcucache"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/loader"
)

type userName = string

// IdentityProvider keeps identities indexed by username.
type IdentityProvider struct {
	identities *rcucache.Cache[userName, Identity]
}

// NewIdentityProvider creates an IdentityProvider from an in-memory map of
// identities.
func NewIdentityProvider(identities map[userName]Identity) *IdentityProvider {
	return &IdentityProvider{
		identities: rcucache.NewCache(identities),
	}
}

// NewIdentityProviderFromFile creates an IdentityProvider by loading identities
// from a YAML file.
func NewIdentityProviderFromFile(path string) (*IdentityProvider, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load identities: failed to read file: %w", err)
	}
	provider, err := decodeIdentities(data, false)
	if err != nil {
		return nil, fmt.Errorf("failed to load identities: %w", err)
	}
	return provider, nil
}

// ParsePolicyIdentities builds the managed identity index from verified bytes.
func ParsePolicyIdentities(data []byte) (*IdentityProvider, error) {
	return decodeIdentities(data, true)
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

func decodeIdentities(data []byte, strict bool) (*IdentityProvider, error) {
	var file struct {
		Identities []Identity `yaml:"identities"`
	}
	if err := loader.DecodeYAML(data, &file, strict); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}
	if strict && file.Identities == nil {
		return nil, fmt.Errorf("missing identities list")
	}

	identities := map[string]Identity{}
	for _, account := range file.Identities {
		if account.Username == "" {
			return nil, fmt.Errorf("identity with empty username")
		}
		if _, exists := identities[account.Username]; strict && exists {
			return nil, fmt.Errorf("duplicate identity %q", account.Username)
		}
		identities[account.Username] = account
	}

	return NewIdentityProvider(identities), nil
}
