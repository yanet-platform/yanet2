//go:build sdk_rust_fixtures && linux && (amd64 || arm64)

package sdkfixture_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/sdk/sdkfixture"
)

// Test_NewAssignConfig_RefusesInvalidInput verifies that a name its field
// cannot hold, a rule of an unknown family and a reversed range are refused
// before the agent is touched.
//
// The owner has no agent, so a build that reaches for its memory panics,
// which fails the case.
func Test_NewAssignConfig_RefusesInvalidInput(t *testing.T) {
	cases := []struct {
		name   string
		config string
		rule   sdkfixture.AssignRule
		want   error
	}{
		{
			name:   "name without room for its terminator",
			config: strings.Repeat("n", len(layout.CpModule{}.Name)),
			want:   cpconfig.ErrInvalidName,
		},
		{
			name:   "unknown family",
			config: "config",
			rule:   sdkfixture.AssignRule{Family: sdkfixture.AssignIPv6 + 1},
			want:   classify.ErrInvalidRule,
		},
		{
			name:   "protocol range that ends before it starts",
			config: "config",
			rule:   sdkfixture.AssignRule{Protos: []classify.LineRange{{From: 17, To: 6}}},
			want:   classify.ErrInvalidRule,
		},
		{
			name:   "port range that ends before it starts",
			config: "config",
			rule:   sdkfixture.AssignRule{Ports: []classify.PortRange{{From: 80, To: 53}}},
			want:   classify.ErrInvalidRule,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() {
				_, err = sdkfixture.NewAssignConfig(&agent.Agent{}, tc.config, []sdkfixture.AssignRule{tc.rule})
			})

			require.ErrorIs(t, err, tc.want)
		})
	}
}
