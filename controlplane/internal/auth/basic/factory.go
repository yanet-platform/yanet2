package basic

import (
	"fmt"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/identity"
)

// NewFromConfig creates a BasicAuthenticator from a raw YAML config node and
// shared dependencies.
func NewFromConfig(
	rawCfg *yaml.Node,
	idp identity.Provider,
	log *zap.Logger,
) (core.Authenticator, error) {
	var cfg Config
	if err := rawCfg.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode basic config: %w", err)
	}

	if cfg.CredentialsPath == "" {
		return nil, fmt.Errorf("credentials_path is required")
	}

	credentialStore, err := NewFileCredentialStore(cfg.CredentialsPath)
	if err != nil {
		return nil, fmt.Errorf("create credential store: %w", err)
	}

	return NewBasicAuthenticator(credentialStore, idp), nil
}
