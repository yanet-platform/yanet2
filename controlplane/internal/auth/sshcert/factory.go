package sshcert

import (
	"fmt"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/identity"
)

// NewFromConfig creates an SSH certificate Authenticator from a raw YAML
// config node and shared dependencies.
func NewFromConfig(
	rawCfg *yaml.Node,
	idp identity.Provider,
	log *zap.Logger,
) (core.Authenticator, error) {
	var cfg Config
	if err := rawCfg.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode sshcert config: %w", err)
	}

	if cfg.CASource == "" {
		return nil, fmt.Errorf("ca_source is required")
	}

	caLoader := NewLoader(cfg.CASource)
	caStore, err := NewCAStoreFromLoader(caLoader)
	if err != nil {
		return nil, fmt.Errorf("create SSH cert CA store: %w", err)
	}

	var revChecker RevocationChecker = NewNopRevocationChecker()
	if cfg.KRLSource != "" {
		krlLoader := NewLoader(cfg.KRLSource)
		revChecker, err = NewKRLRevocationCheckerFromLoader(krlLoader)
		if err != nil {
			return nil, fmt.Errorf(
				"create SSH cert revocation checker: %w", err,
			)
		}
	}

	opts := []Option{
		WithLog(log),
	}
	if cfg.TimeWindow > 0 {
		opts = append(opts, WithTimeWindow(cfg.TimeWindow))
	}
	if cfg.RefreshInterval > 0 {
		opts = append(opts, WithRefreshInterval(cfg.RefreshInterval))
	}

	return NewAuthenticator(
		caStore, revChecker, idp, opts...,
	), nil
}
