// Package fuzzing implements a standalone load/fuzzing application for the
// balancer2 controlplane. This file defines the sidecar YAML runtime
// configuration and its validation.
package fuzzing

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// RuntimeConfig is the sidecar YAML runtime configuration loaded by the
// fuzzing CLI. Field names and YAML tags match the schema documented in the
// plan; any change here is a breaking change for example configs.
type RuntimeConfig struct {
	// Endpoint is the balancer2 controlplane gRPC address, e.g.
	// "127.0.0.1:8080".
	Endpoint string `yaml:"endpoint"`
	// CorpusPath lists keepalived-style services configuration files used
	// to seed the initial expected model.
	CorpusPath string `yaml:"corpus_path"`
	// ConfigName is the balancer2 named config the fuzzer creates and
	// mutates.
	ConfigName string `yaml:"config_name"`
	// OperationInterval is the wall-clock delay between consecutive
	// operations.
	OperationInterval time.Duration `yaml:"operation_interval"`
	// UpdateVsEvery is cadence N: UpdateVS fires every N operations and
	// DeleteVS fires every 2*N operations.
	UpdateVsEvery uint64 `yaml:"update_vs_every"`
	// StatsInterval controls how often the runner emits periodic latency
	// reports.
	StatsInterval time.Duration `yaml:"stats_interval"`
	// RequestTimeout is the per-RPC context timeout applied to every
	// controlplane call.
	RequestTimeout time.Duration `yaml:"request_timeout"`
	// Seed seeds the deterministic RNG used by operation generation. A
	// zero or missing value causes a non-zero seed to be generated on
	// load and surfaced through EffectiveSeed for replay logging.
	Seed int64 `yaml:"seed"`
}

// seedSource is the source of non-zero seeds used when the YAML seed is zero
// or missing. It is package-private so tests can replace it with a
// deterministic source.
var seedSource = defaultSeedSource

// defaultSeedSource returns a non-zero seed derived from the current monotonic
// time. The retry loop guarantees a non-zero return even in the unlikely event
// that the clock reading aliases to zero.
func defaultSeedSource() int64 {
	for {
		v := time.Now().UnixNano()
		if v != 0 {
			return v
		}
	}
}

// LoadRuntimeConfig reads a YAML runtime config from path, decodes it,
// validates it, and applies the effective seed.
func LoadRuntimeConfig(path string) (*RuntimeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read runtime config %q: %w", path, err)
	}

	cfg, err := DecodeRuntimeConfig(data)
	if err != nil {
		return nil, fmt.Errorf("failed to load runtime config %q: %w", path, err)
	}

	return cfg, nil
}

// DecodeRuntimeConfig decodes a YAML runtime config from raw bytes, validates
// it, and applies the effective seed. It exists so tests can exercise the
// loader without touching the filesystem.
func DecodeRuntimeConfig(data []byte) (*RuntimeConfig, error) {
	cfg := &RuntimeConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse runtime config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	cfg.applyEffectiveSeed()
	return cfg, nil
}

// Validate checks that all required fields are populated with usable values.
// Error messages always contain the exact YAML field name so operators can
// correlate failures with their config file.
func (m *RuntimeConfig) Validate() error {
	if m.Endpoint == "" {
		return fmt.Errorf("endpoint: must be set to a non-empty controlplane address")
	}
	if m.CorpusPath == "" {
		return fmt.Errorf("corpus_path required")
	}
	if m.ConfigName == "" {
		return fmt.Errorf("config_name: must be set to a non-empty balancer2 config name")
	}
	if m.OperationInterval <= 0 {
		return fmt.Errorf("operation_interval: must be a positive duration")
	}
	if m.UpdateVsEvery == 0 {
		return fmt.Errorf("update_vs_every: must be greater than zero")
	}
	if m.StatsInterval <= 0 {
		return fmt.Errorf("stats_interval: must be a positive duration")
	}
	if m.RequestTimeout <= 0 {
		return fmt.Errorf("request_timeout: must be a positive duration")
	}
	return nil
}

// applyEffectiveSeed assigns a non-zero seed when the YAML seed was zero or
// missing. The chosen value is written back to Seed so callers can log it for
// replay.
func (m *RuntimeConfig) applyEffectiveSeed() {
	if m.Seed != 0 {
		return
	}
	m.Seed = seedSource()
}
