package sshkey

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

// NewFromConfig creates an SSH key Authenticator from a raw YAML config node.
func NewFromConfig(
	rawCfg *yaml.Node,
	options ...Option,
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

	opts := make([]Option, 0, len(options)+1)
	opts = append(opts, options...)
	if cfg.TimeWindow > 0 {
		opts = append(opts, WithTimeWindow(cfg.TimeWindow))
	}

	return NewAuthenticator(keyStore, opts...), nil
}
