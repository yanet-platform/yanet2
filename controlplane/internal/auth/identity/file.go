package identity

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/rcucache"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/loader"
)

type userName = string

// FileProvider keeps identities indexed by username.
type FileProvider struct {
	identities *rcucache.Cache[userName, Identity]
	source     *loader.FileLoader
}

// NewFileProvider loads the initial identities before accepting lookups.
func NewFileProvider(path string) (*FileProvider, error) {
	m := &FileProvider{
		identities: rcucache.NewEmptyCache[userName, Identity](),
		source:     loader.NewFileLoader(path),
	}
	if err := m.Reload(); err != nil {
		return nil, fmt.Errorf("failed to load identities: %w", err)
	}
	return m, nil
}

// Name returns the provider name for logging.
func (m *FileProvider) Name() string {
	return "file"
}

// ResolveIdentity retrieves an identity by the subject's account lookup name.
func (m *FileProvider) ResolveIdentity(
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

// Reload applies a complete valid file, preserving prior identities on error.
//
// A single writer may reload while lookups proceed concurrently.
func (m *FileProvider) Reload() error {
	buf, err := m.source.Load()
	if err != nil {
		return err
	}
	identities, err := parseIdentities(buf)
	if err != nil {
		return err
	}
	m.identities.Swap(identities)
	return nil
}

func parseIdentities(buf []byte) (map[string]Identity, error) {
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
