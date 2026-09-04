package x509

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/loader"
)

// NewFromConfig creates an Authenticator from a raw YAML config node.
func NewFromConfig(rawCfg *yaml.Node, options ...Option) (*Authenticator, error) {
	var cfg Config
	if err := decodeStrict(rawCfg, &cfg); err != nil {
		return nil, fmt.Errorf("decode x509 config: %w", err)
	}

	if len(cfg.CASources) == 0 {
		return nil, fmt.Errorf("ca_sources is required")
	}

	caSources := make([]loader.Loader, 0, len(cfg.CASources))
	for _, source := range cfg.CASources {
		caSources = append(caSources, loader.NewLoader(source))
	}

	crlSources := make([]loader.Loader, 0, len(cfg.CRLSources))
	for _, source := range cfg.CRLSources {
		crlSources = append(crlSources, loader.NewLoader(source))
	}

	store, err := NewStore(caSources, crlSources)
	if err != nil {
		return nil, err
	}

	opts := make([]Option, 0, len(options)+1)
	opts = append(opts, options...)
	if cfg.RefreshInterval > 0 {
		opts = append(opts, WithRefreshInterval(cfg.RefreshInterval))
	}

	return NewAuthenticator(store, opts...), nil
}

// decodeStrict decodes the node into cfg, refusing keys the config does not
// declare, so a misspelled revocation list key cannot silently disable
// revocation.
func decodeStrict(node *yaml.Node, cfg *Config) error {
	if node.Kind == 0 {
		return nil
	}

	data, err := yaml.Marshal(node)
	if err != nil {
		return err
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return err
	}

	return nil
}
