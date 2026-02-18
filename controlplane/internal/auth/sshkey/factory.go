package sshkey

import (
	"fmt"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/identity"
)

// NewFromConfig creates an SSH key Authenticator from a raw YAML config node
// and shared dependencies.
func NewFromConfig(
	rawCfg *yaml.Node,
	idp identity.Provider,
	log *zap.Logger,
) (core.Authenticator, error) {
	var cfg Config
	if err := rawCfg.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode sshkey config: %w", err)
	}

	if cfg.KeysPath == "" {
		return nil, fmt.Errorf("keys_path is required")
	}

	keyStore, err := NewKeyStoreFromFile(cfg.KeysPath)
	if err != nil {
		return nil, fmt.Errorf("create SSH key store: %w", err)
	}

	opts := []Option{
		WithLog(log),
	}
	if cfg.TimeWindow > 0 {
		opts = append(opts, WithTimeWindow(cfg.TimeWindow))
	}

	return NewAuthenticator(keyStore, idp, opts...), nil
}
