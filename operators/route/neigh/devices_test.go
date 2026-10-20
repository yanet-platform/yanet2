package neigh_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/route/neigh"
)

// Test_DeviceScope_Eligibility verifies that empty restrictions accept every
// device while nonempty restrictions match names exactly without trimming.
func Test_DeviceScope_Eligibility(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scope    neigh.DeviceScope
		accepted []string
		rejected []string
	}{
		{
			name: "zero scope is unrestricted", scope: neigh.DeviceScope{},
			accepted: []string{"", "eth0", "eth1", " eth0 "},
		},
		{
			name: "nil list is unrestricted", scope: neigh.NewDeviceScope(nil),
			accepted: []string{"", "eth0", "eth1", " eth0 "},
		},
		{
			name: "empty list is unrestricted", scope: neigh.NewDeviceScope([]string{}),
			accepted: []string{"", "eth0", "eth1", " eth0 "},
		},
		{
			name: "only empty names are unrestricted", scope: neigh.NewDeviceScope([]string{"", ""}),
			accepted: []string{"", "eth0", "eth1", " eth0 "},
		},
		{
			name:     "duplicates and empty names preserve exact restrictions",
			scope:    neigh.NewDeviceScope([]string{"", "eth0", "eth0", "eth2", ""}),
			accepted: []string{"eth0", "eth2"}, rejected: []string{"", "eth1", " eth0 ", "ETH0"},
		},
		{
			name: "whitespace in a name is significant", scope: neigh.NewDeviceScope([]string{" eth0 "}),
			accepted: []string{" eth0 "}, rejected: []string{"", "eth0", "eth1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, device := range tc.accepted {
				require.True(t, tc.scope.Allows(device), "device %q must be accepted", device)
			}
			for _, device := range tc.rejected {
				require.False(t, tc.scope.Allows(device), "device %q must be rejected", device)
			}
		})
	}
}

// Test_DeviceScope_OwnsDeviceNames verifies that caller mutations cannot change
// an already constructed restriction.
func Test_DeviceScope_OwnsDeviceNames(t *testing.T) {
	devices := []string{"", "eth0", "eth0"}
	scope := neigh.NewDeviceScope(devices)
	devices[0], devices[1], devices[2] = "eth1", "eth1", ""
	require.True(t, scope.Allows("eth0"))
	require.False(t, scope.Allows("eth1"))
	require.False(t, scope.Allows(""))
}
