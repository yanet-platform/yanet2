package operator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

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
				Weight:    1,
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
					Weight:    1,
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
					Weight:    1,
					Module:    xcfg.MustNonEmptyString("vlan-phy"),
					RulesFile: xcfg.MustNonEmptyString("/etc/yanet2/forward.d/other.yaml"),
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
