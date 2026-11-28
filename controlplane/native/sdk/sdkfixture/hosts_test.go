//go:build sdk_rust_fixtures && linux && (amd64 || arm64)

package sdkfixture_test

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/sdk/sdkfixture"
)

// Test_NewHostsConfig_RefusesInvalidInput verifies that a name its field
// cannot hold, an address that is not IPv4 and an address given twice are
// refused before the agent is touched.
//
// The owner has no agent, so a build that reaches for its memory panics,
// which fails the case.
func Test_NewHostsConfig_RefusesInvalidInput(t *testing.T) {
	cases := []struct {
		name   string
		config string
		hosts  []sdkfixture.Host
		want   error
	}{
		{
			name:   "name without room for its terminator",
			config: strings.Repeat("n", len(layout.CpModule{}.Name)),
			want:   cpconfig.ErrInvalidName,
		},
		{
			name:   "IPv6 address",
			config: "hosts",
			hosts:  []sdkfixture.Host{{Addr: netip.MustParseAddr("2001:db8::1"), Counter: "a"}},
			want:   sdkfixture.ErrInvalidArgument,
		},
		{
			name:   "address given twice",
			config: "hosts",
			hosts: []sdkfixture.Host{
				{Addr: netip.MustParseAddr("10.0.0.1"), Counter: "a"},
				{Addr: netip.MustParseAddr("10.0.0.2"), Counter: "b"},
				{Addr: netip.MustParseAddr("10.0.0.1"), Counter: "c"},
			},
			want: sdkfixture.ErrInvalidArgument,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() {
				_, err = sdkfixture.NewHostsConfig(&agent.Agent{}, tc.config, tc.hosts)
			})

			require.ErrorIs(t, err, tc.want)
		})
	}
}
