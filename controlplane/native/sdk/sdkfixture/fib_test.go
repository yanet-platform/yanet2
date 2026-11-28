//go:build sdk_rust_fixtures && linux && (amd64 || arm64)

package sdkfixture_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/sdk/sdkfixture"
)

// Test_NewFibConfig_RefusesInvalidInput verifies that a name its field
// cannot hold, a route naming a device past the list and a range that ends
// before it starts are refused before the agent is touched.
//
// The owner has no agent, so a build that reaches for its memory panics,
// which fails the case.
func Test_NewFibConfig_RefusesInvalidInput(t *testing.T) {
	cases := []struct {
		name   string
		config string
		fib    sdkfixture.Fib
		want   error
	}{
		{
			name:   "name without room for its terminator",
			config: strings.Repeat("n", len(layout.CpModule{}.Name)),
			want:   cpconfig.ErrInvalidName,
		},
		{
			name:   "route of a device past the list",
			config: "fib",
			fib: sdkfixture.Fib{
				Devices: []string{"eth0"},
				Routes:  []sdkfixture.FibRoute{{Device: 1, Counter: "route"}},
			},
			want: sdkfixture.ErrInvalidArgument,
		},
		{
			name:   "route of a negative device",
			config: "fib",
			fib: sdkfixture.Fib{
				Devices: []string{"eth0"},
				Routes:  []sdkfixture.FibRoute{{Device: -1, Counter: "route"}},
			},
			want: sdkfixture.ErrInvalidArgument,
		},
		{
			name:   "IPv6 range that ends before it starts",
			config: "fib",
			fib:    sdkfixture.Fib{V6: []sdkfixture.Range6{{From: [16]byte{0x20, 1}, To: [16]byte{0x20}}}},
			want:   sdkfixture.ErrInvalidArgument,
		},
		{
			name:   "IPv4 range that ends before it starts",
			config: "fib",
			fib:    sdkfixture.Fib{V4: []sdkfixture.Range4{{From: [4]byte{10, 1}, To: [4]byte{10}}}},
			want:   sdkfixture.ErrInvalidArgument,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() {
				_, err = sdkfixture.NewFibConfig(&agent.Agent{}, tc.config, tc.fib)
			})

			require.ErrorIs(t, err, tc.want)
		})
	}
}
