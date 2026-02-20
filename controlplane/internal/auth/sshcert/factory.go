package sshcert

import (
	"fmt"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

// NewFromConfig creates an SSH certificate Authenticator from a raw YAML.
func NewFromConfig(
	rawCfg *yaml.Node,
	log *zap.Logger,
) (core.Authenticator, error) {
	var cfg Config
	if err := rawCfg.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode sshcert config: %w", err)
	}

	if len(cfg.CASources) == 0 {
		return nil, fmt.Errorf("ca_sources is required")
	}

	caStores := make([]*CAStore, len(cfg.CASources))
	for idx, src := range cfg.CASources {
		store, err := NewCAStoreFromLoader(NewLoader(src))
		if err != nil {
			return nil, fmt.Errorf(
				"create CA store from %q: %w", src, err,
			)
		}

		caStores[idx] = store
	}

	caStore := NewCompositeCAStore(caStores)

	var revChecker RevocationChecker = NewNopRevocationChecker()
	if cfg.KRLSource != "" {
		krlLoader := NewLoader(cfg.KRLSource)
		var err error
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

	return NewAuthenticator(caStore, revChecker, opts...), nil
}
