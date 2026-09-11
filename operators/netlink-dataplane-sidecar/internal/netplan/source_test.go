package netplan_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/desired"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// Test_Source_Load verifies that the interface adapter preserves parser output
// and propagates missing files and malformed startup documents.
func Test_Source_Load(t *testing.T) {
	for _, tc := range []struct {
		name    string
		data    string
		missing bool
		valid   bool
	}{
		{name: "valid file", data: "network: {version: 2, ethernets: {kni0: {addresses: ['192.0.2.7/24']}}}", valid: true},
		{name: "missing file", missing: true},
		{name: "invalid YAML", data: "network: ["},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "netplan.yaml")
			if !tc.missing {
				require.NoError(t, os.WriteFile(path, []byte(tc.data), 0o600))
			}
			var source desired.Source = &netplan.Source{Path: path}
			state, err := source.Load()
			if tc.valid {
				require.NoError(t, err)
				expected, err := netplan.ParseFile(path)
				require.NoError(t, err)
				require.Equal(t, expected, state)
			} else {
				require.Error(t, err)
				require.Equal(t, desired.State{}, state)
				if tc.missing {
					require.ErrorIs(t, err, os.ErrNotExist)
				}
			}
		})
	}
}
