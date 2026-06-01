package operator

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

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
