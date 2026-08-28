package operator

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// Test_ShippedDefaultConfig_NoUnknownKeys guards the shipped default config
// against a key that matches no field in Config, including one nested
// inside a functions entry whose FunctionConfig.UnmarshalYAML re-decodes
// through a fresh yaml.v3 decoder.
func Test_ShippedDefaultConfig_NoUnknownKeys(t *testing.T) {
	data, err := os.ReadFile("../../etc/yanet/yanet-decap-operator-default.yaml")
	require.NoError(t, err)
	require.NoError(t, xcfg.CheckKnownKeys[Config](data))
}

// Test_ShippedPrefixesDefault_Loads verifies that the shipped decap module
// config loads with the function's name bound and both prefix lists empty.
func Test_ShippedPrefixesDefault_Loads(t *testing.T) {
	request, err := LoadModuleConfig("../../etc/yanet/decap.d/default.yaml", "decap0")
	require.NoError(t, err)
	require.Equal(t, "decap0", request.GetName())
	require.Empty(t, request.GetPrefixes4())
	require.Empty(t, request.GetPrefixes6())
}

// Test_ShippedDefaultConfig_OmittedServerEndpointUsesEphemeralPort verifies
// that the shipped file inherits the loopback port-zero listener endpoint.
func Test_ShippedDefaultConfig_OmittedServerEndpointUsesEphemeralPort(t *testing.T) {
	config, err := xcfg.LoadConfig[Config](
		"../../etc/yanet/yanet-decap-operator-default.yaml",
	)
	require.NoError(t, err)
	require.Equal(t, "[::1]:0", config.Server.Endpoint.Unwrap())
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

// verifies that gateway scopes map by name to the nominal reconcile interval,
// independent of retry backoff and declaration order.
func Test_ReadinessScopeSpecs_GatewaysUseNominalReconcileInterval(t *testing.T) {
	config := DefaultConfig()
	config.Gateways = []operator.GatewayConfig{{Name: "gw0"}, {Name: "gw1"}}
	config.Reconcile.Interval = xcfg.MustNonZero(10 * time.Second)
	config.Reconcile.MaxBackoff = xcfg.MustNonZero(2 * time.Minute)

	intervalsByName := map[string]time.Duration{}
	for _, scopeSpec := range readinessScopeSpecs(config) {
		intervalsByName[scopeSpec.Name] = scopeSpec.ExpectedObservationInterval
	}
	require.Equal(t, map[string]time.Duration{
		"config:gw0": 10 * time.Second,
		"config:gw1": 10 * time.Second,
	}, intervalsByName)
}
