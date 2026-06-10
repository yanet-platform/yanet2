package operator

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

func TestDefaultConfig_ServerEndpoint(t *testing.T) {
	cfg := DefaultConfig()
	require.Equal(t, "[::1]:50004", cfg.Server.Endpoint.Unwrap())
}

func TestFunctionConfig_IgnorePdump_DefaultsTrue(t *testing.T) {
	// When ignore_pdump is absent from the YAML, it must default to true.
	raw := `
name: fn:decap
chain: default
weight: 1
module: decap0
prefixes_file: /etc/yanet2/decap.d/default.yaml
`
	var fn FunctionConfig
	require.NoError(t, yaml.Unmarshal([]byte(raw), &fn))
	require.True(t, fn.IgnorePdump)
}

func TestFunctionConfig_IgnorePdump_ExplicitFalse(t *testing.T) {
	// An explicit ignore_pdump: false must be preserved.
	raw := `
name: fn:decap
chain: default
weight: 1
module: decap0
prefixes_file: /etc/yanet2/decap.d/default.yaml
ignore_pdump: false
`
	var fn FunctionConfig
	require.NoError(t, yaml.Unmarshal([]byte(raw), &fn))
	require.False(t, fn.IgnorePdump)
}

// TestDecode_FunctionMissingRequiredField verifies that xcfg.Decode catches a
// functions entry that omits a required NonEmptyString field, surfacing it as a
// *xcfg.PathError with a path rooted at the slice element.
func TestDecode_FunctionMissingRequiredField(t *testing.T) {
	// Minimal valid YAML except the single functions entry omits prefixes_file.
	input := `
logging:
  level: info
server:
  endpoint: "[::1]:50004"
gateways:
  - name: numa0
    endpoint: "[::1]:8080"
reconcile:
  interval: 5s
  initial_backoff: 1s
  max_backoff: 30s
functions:
  - name: fn:decap
    chain: default
    weight: 1
    module: decap0
`
	cfg := DefaultConfig()
	err := xcfg.Decode([]byte(input), cfg)

	var pathErr *xcfg.PathError
	require.ErrorAs(t, err, &pathErr)
	require.Contains(t, pathErr.Path, "functions[0]")
}

func validConfig() *Config {
	return &Config{
		Gateways: []operator.GatewayConfig{
			{Name: "numa0", Endpoint: xcfg.MustNonEmptyString("[::1]:8080")},
		},
		Reconcile: operator.ReconcileConfig{
			Interval:       xcfg.MustNonZero(operator.DefaultReconcileInterval),
			InitialBackoff: xcfg.MustNonZero(operator.DefaultReconcileInitialBackoff),
			MaxBackoff:     xcfg.MustNonZero(operator.DefaultReconcileMaxBackoff),
		},
		Functions: []FunctionConfig{
			{
				Name:         xcfg.MustNonEmptyString("fn:decap"),
				Chain:        xcfg.MustNonEmptyString("default"),
				Weight:       1,
				Module:       xcfg.MustNonEmptyString("decap0"),
				PrefixesFile: xcfg.MustNonEmptyString("/etc/yanet2/decap.d/default.yaml"),
			},
		},
	}
}

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		build   func() *Config
		wantErr bool
	}{
		{
			name:    "valid config",
			build:   validConfig,
			wantErr: false,
		},
		{
			name: "zero gateways",
			build: func() *Config {
				cfg := validConfig()
				cfg.Gateways = nil
				return cfg
			},
			wantErr: true,
		},
		{
			name: "zero functions",
			build: func() *Config {
				cfg := validConfig()
				cfg.Functions = nil
				return cfg
			},
			wantErr: true,
		},
		{
			name: "duplicate function name",
			build: func() *Config {
				cfg := validConfig()
				cfg.Functions = append(cfg.Functions, FunctionConfig{
					Name:         xcfg.MustNonEmptyString("fn:decap"),
					Chain:        xcfg.MustNonEmptyString("default"),
					Weight:       1,
					Module:       xcfg.MustNonEmptyString("other-module"),
					PrefixesFile: xcfg.MustNonEmptyString("/etc/yanet2/decap.d/other.yaml"),
				})
				return cfg
			},
			wantErr: true,
		},
		{
			name: "duplicate module name",
			build: func() *Config {
				cfg := validConfig()
				cfg.Functions = append(cfg.Functions, FunctionConfig{
					Name:         xcfg.MustNonEmptyString("fn:decap-other"),
					Chain:        xcfg.MustNonEmptyString("default"),
					Weight:       1,
					Module:       xcfg.MustNonEmptyString("decap0"),
					PrefixesFile: xcfg.MustNonEmptyString("/etc/yanet2/decap.d/other.yaml"),
				})
				return cfg
			},
			wantErr: true,
		},
		{
			name: "duplicate gateway name",
			build: func() *Config {
				cfg := validConfig()
				cfg.Gateways = append(cfg.Gateways, operator.GatewayConfig{
					Name:     "numa0",
					Endpoint: xcfg.MustNonEmptyString("[::1]:8081"),
				})
				return cfg
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.build().Validate()
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
