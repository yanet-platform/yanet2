package auth_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth"
)

// Test_Config_IdentityRefreshInterval verifies that production decoding keeps
// polling opt-in and rejects negative intervals even outside enabled mode.
func Test_Config_IdentityRefreshInterval(t *testing.T) {
	for _, mode := range []string{"mode: enabled", "mode: audit", "mode: disabled", "disabled: false"} {
		t.Run(mode, func(t *testing.T) {
			for _, tc := range []struct {
				name     string
				setting  string
				interval time.Duration
				wantErr  bool
			}{
				{name: "omitted interval"},
				{name: "zero interval", setting: "refresh_interval: 0s"},
				{name: "enabled polling", setting: "refresh_interval: 5s", interval: 5 * time.Second},
				{name: "negative interval", setting: "refresh_interval: -1s", wantErr: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					text := mode + "\nidentity_providers:\n  - type: file\n    path: identities.yaml\n    " + tc.setting + "\n"
					var config auth.Config
					err := xcfg.Decode([]byte(text), &config, xcfg.WithEnv())
					if tc.wantErr {
						require.ErrorContains(t, err, "identity_providers[0].refresh_interval")
						return
					}
					require.NoError(t, err)
					require.Equal(t, tc.interval, config.IdentityProviders[0].RefreshInterval)
				})
			}
		})
	}
}

// Test_Config_PermissionsRefreshInterval verifies that existing configuration
// stays static and negative polling intervals are rejected in every mode.
func Test_Config_PermissionsRefreshInterval(t *testing.T) {
	for _, mode := range []string{"mode: enabled", "mode: audit", "mode: disabled", "disabled: false"} {
		t.Run(mode, func(t *testing.T) {
			for _, tc := range []struct {
				name     string
				setting  string
				interval time.Duration
				wantErr  bool
			}{
				{name: "omitted interval"},
				{name: "zero interval", setting: "permissions_refresh_interval: 0s"},
				{name: "enabled polling", setting: "permissions_refresh_interval: 5s", interval: 5 * time.Second},
				{name: "negative interval", setting: "permissions_refresh_interval: -1s", wantErr: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					text := mode + "\npermissions_path: permissions.yaml\n" + tc.setting + "\n"
					var config auth.Config
					err := xcfg.Decode([]byte(text), &config, xcfg.WithEnv())
					if tc.wantErr {
						require.ErrorContains(t, err, "permissions_refresh_interval")
						return
					}
					require.NoError(t, err)
					require.Equal(t, "permissions.yaml", config.PermissionsPath)
					require.Equal(t, tc.interval, config.PermissionsRefreshInterval)
				})
			}
		})
	}
}
