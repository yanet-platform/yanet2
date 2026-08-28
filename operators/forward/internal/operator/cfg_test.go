package operator

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// Test_ShippedDefaultConfig_NoUnknownKeys guards the shipped default config
// against a key that matches no field in Config.
func Test_ShippedDefaultConfig_NoUnknownKeys(t *testing.T) {
	data, err := os.ReadFile("../../etc/yanet/yanet-forward-operator-default.yaml")
	require.NoError(t, err)
	require.NoError(t, xcfg.CheckKnownKeys[Config](data))
}

// Test_ShippedDefaultConfig_OmittedServerEndpointUsesEphemeralPort verifies
// that the shipped file inherits the loopback port-zero listener endpoint.
func Test_ShippedDefaultConfig_OmittedServerEndpointUsesEphemeralPort(t *testing.T) {
	config, err := xcfg.LoadConfig[Config](
		"../../etc/yanet/yanet-forward-operator-default.yaml",
	)
	require.NoError(t, err)
	require.Equal(t, "[::1]:0", config.Server.Endpoint.Unwrap())
}

// Test_ShippedRulesDefault_NoUnknownKeys guards the shipped forward rules
// files against a key that matches no field in yamlForwardConfig.
func Test_ShippedRulesDefault_NoUnknownKeys(t *testing.T) {
	paths := []string{
		"../../etc/yanet/forward.d/vlan-phy-default.yaml",
		"../../etc/yanet/forward.d/phy-vlan-default.yaml",
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, xcfg.CheckKnownKeys[yamlForwardConfig](data))
	}
}

// TestDecode_FunctionMissingRequiredField verifies that xcfg.Decode catches a
// functions entry that omits a required NonEmptyString field, surfacing it as a
// *xcfg.PathError with a path rooted at the slice element.
func TestDecode_FunctionMissingRequiredField(t *testing.T) {
	// Minimal valid YAML except the single functions entry omits rules_file.
	yaml := `
logging:
  level: info
server:
  endpoint: "[::1]:50003"
gateways:
  - name: numa0
    endpoint: "[::1]:8080"
reconcile:
  interval: 5s
  initial_backoff: 1s
  max_backoff: 30s
functions:
  - name: fn:forward-vlan-phy
    chain: default
    weight: 1
    module: vlan-phy
`
	cfg := DefaultConfig()
	err := xcfg.Decode([]byte(yaml), cfg)

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
				Name:      xcfg.MustNonEmptyString("fn:forward-vlan-phy"),
				Chain:     xcfg.MustNonEmptyString("default"),
				Weight:    xcfg.NewRequired(uint64(1)),
				Module:    xcfg.MustNonEmptyString("vlan-phy"),
				RulesFile: xcfg.MustNonEmptyString("/etc/yanet2/forward.d/vlan-phy-default.yaml"),
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
					Name:      xcfg.MustNonEmptyString("fn:forward-vlan-phy"),
					Chain:     xcfg.MustNonEmptyString("default"),
					Weight:    xcfg.NewRequired(uint64(1)),
					Module:    xcfg.MustNonEmptyString("other-module"),
					RulesFile: xcfg.MustNonEmptyString("/etc/yanet2/forward.d/other.yaml"),
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
					Name:      xcfg.MustNonEmptyString("fn:forward-other"),
					Chain:     xcfg.MustNonEmptyString("default"),
					Weight:    xcfg.NewRequired(uint64(1)),
					Module:    xcfg.MustNonEmptyString("vlan-phy"),
					RulesFile: xcfg.MustNonEmptyString("/etc/yanet2/forward.d/other.yaml"),
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

// Test_Decode_WeightMustBeSpelled verifies that an omitted weight is refused
// at load time, while an explicit zero, a disabled chain, is accepted.
func Test_Decode_WeightMustBeSpelled(t *testing.T) {
	cases := []struct {
		name    string
		weight  string
		wantErr bool
	}{
		{name: "omitted weight", weight: "", wantErr: true},
		{name: "explicit zero", weight: "    weight: 0\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := `
gateways:
  - name: numa0
    endpoint: "[::1]:8080"
functions:
  - name: fn:forward-vlan-phy
    chain: default
    module: vlan-phy
    rules_file: /etc/yanet2/forward.d/vlan-phy-default.yaml
` + tc.weight
			cfg := DefaultConfig()
			err := xcfg.Decode([]byte(input), cfg)

			if tc.wantErr {
				var pathErr *xcfg.PathError
				require.ErrorAs(t, err, &pathErr)
				require.Contains(t, pathErr.Path, "functions[0].weight")
				return
			}
			require.NoError(t, err)
			require.Equal(t, uint64(0), cfg.Functions[0].Weight.Unwrap())
		})
	}
}
