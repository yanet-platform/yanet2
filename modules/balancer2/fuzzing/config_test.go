package fuzzing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validYAML is the canonical runtime config used by happy-path tests. It
// matches the schema documented in the plan.
const validYAML = `endpoint: "127.0.0.1:8080"
corpus_path: "modules/balancer2/fuzzing/taxi.services.conf"
config_name: "balancer2-fuzz"
operation_interval: "10ms"
update_vs_every: 10
stats_interval: "30s"
request_timeout: "5s"
seed: 12345
`

// withSeedSource swaps the package-private seed source for the duration of a
// test and restores it on cleanup. It exists so seed-generation tests do not
// depend on wall-clock entropy.
func withSeedSource(t *testing.T, fn func() int64) {
	t.Helper()
	prev := seedSource
	seedSource = fn
	t.Cleanup(func() { seedSource = prev })
}

func TestLoadRuntimeConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.yaml")
	require.NoError(t, os.WriteFile(path, []byte(validYAML), 0o600))

	cfg, err := LoadRuntimeConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "127.0.0.1:8080", cfg.Endpoint)
	assert.Equal(t, "modules/balancer2/fuzzing/taxi.services.conf", cfg.CorpusPath)
	assert.Equal(t, "balancer2-fuzz", cfg.ConfigName)
	assert.Equal(t, 10*time.Millisecond, cfg.OperationInterval)
	assert.Equal(t, uint64(10), cfg.UpdateVsEvery)
	assert.Equal(t, 30*time.Second, cfg.StatsInterval)
	assert.Equal(t, 5*time.Second, cfg.RequestTimeout)
	assert.Equal(t, int64(12345), cfg.Seed)
}

func TestLoadRuntimeConfigMissingFile(t *testing.T) {
	_, err := LoadRuntimeConfig(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	require.Error(t, err)
}

func TestLoadRuntimeConfigValid(t *testing.T) {
	cfg, err := DecodeRuntimeConfig([]byte(validYAML))
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "127.0.0.1:8080", cfg.Endpoint)
	assert.Equal(t, int64(12345), cfg.Seed)
}

func TestLoadRuntimeConfigRejectsMissingEndpoint(t *testing.T) {
	in := strings.Replace(validYAML, `endpoint: "127.0.0.1:8080"`, `endpoint: ""`, 1)
	_, err := DecodeRuntimeConfig([]byte(in))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint")
}

func TestLoadRuntimeConfigRejectsInvalidCadence(t *testing.T) {
	in := strings.Replace(validYAML, "update_vs_every: 10", "update_vs_every: 0", 1)
	_, err := DecodeRuntimeConfig([]byte(in))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update_vs_every")
}

func TestLoadRuntimeConfigGeneratesSeedWhenZero(t *testing.T) {
	withSeedSource(t, func() int64 { return 0xC0FFEE })

	in := strings.Replace(validYAML, "seed: 12345", "seed: 0", 1)
	cfg, err := DecodeRuntimeConfig([]byte(in))
	require.NoError(t, err)
	assert.Equal(
		t,
		int64(0xC0FFEE),
		cfg.Seed,
		"seed: 0 must be replaced with a non-zero effective seed",
	)
}

func TestLoadRuntimeConfigGeneratesSeedWhenMissing(t *testing.T) {
	withSeedSource(t, func() int64 { return 42 })

	in := strings.Replace(validYAML, "seed: 12345\n", "", 1)
	cfg, err := DecodeRuntimeConfig([]byte(in))
	require.NoError(t, err)
	assert.Equal(
		t,
		int64(42),
		cfg.Seed,
		"missing seed must be replaced with a non-zero effective seed",
	)
}

func TestLoadRuntimeConfigPreservesNonZeroSeed(t *testing.T) {
	withSeedSource(t, func() int64 {
		t.Fatal("seed source must not be consulted when YAML seed is non-zero")
		return 0
	})

	cfg, err := DecodeRuntimeConfig([]byte(validYAML))
	require.NoError(t, err)
	assert.Equal(t, int64(12345), cfg.Seed)
}

func TestDefaultSeedSourceIsNonZero(t *testing.T) {
	assert.NotZero(t, defaultSeedSource())
}

func TestRuntimeConfigValidate(t *testing.T) {
	base := func() RuntimeConfig {
		return RuntimeConfig{
			Endpoint:          "127.0.0.1:8080",
			CorpusPath:        "taxi.services.conf",
			ConfigName:        "balancer2-fuzz",
			OperationInterval: 10 * time.Millisecond,
			UpdateVsEvery:     5,
			StatsInterval:     30 * time.Second,
			RequestTimeout:    5 * time.Second,
		}
	}

	tests := []struct {
		name       string
		mutate     func(*RuntimeConfig)
		wantSubstr string
	}{
		{
			name:       "missing endpoint",
			mutate:     func(c *RuntimeConfig) { c.Endpoint = "" },
			wantSubstr: "endpoint",
		},
		{
			name:       "empty corpus paths",
			mutate:     func(c *RuntimeConfig) { c.CorpusPath = "" },
			wantSubstr: "corpus_path",
		},
		{
			name:       "missing config name",
			mutate:     func(c *RuntimeConfig) { c.ConfigName = "" },
			wantSubstr: "config_name",
		},
		{
			name:       "zero operation interval",
			mutate:     func(c *RuntimeConfig) { c.OperationInterval = 0 },
			wantSubstr: "operation_interval",
		},
		{
			name:       "zero update_vs_every",
			mutate:     func(c *RuntimeConfig) { c.UpdateVsEvery = 0 },
			wantSubstr: "update_vs_every",
		},
		{
			name:       "zero stats interval",
			mutate:     func(c *RuntimeConfig) { c.StatsInterval = 0 },
			wantSubstr: "stats_interval",
		},
		{
			name:       "zero request timeout",
			mutate:     func(c *RuntimeConfig) { c.RequestTimeout = 0 },
			wantSubstr: "request_timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base()
			tt.mutate(&cfg)
			err := cfg.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantSubstr)
		})
	}

	t.Run("valid", func(t *testing.T) {
		cfg := base()
		require.NoError(t, cfg.Validate())
	})
}
