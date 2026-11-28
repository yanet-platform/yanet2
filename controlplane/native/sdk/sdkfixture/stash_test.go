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

// Test_NewStashConfig_RefusesInvalidInput verifies that a name its field
// cannot hold, the linked state's included, is refused before the agent is
// touched.
//
// The owner has no agent, so a build that reaches for its memory panics,
// which fails the case.
func Test_NewStashConfig_RefusesInvalidInput(t *testing.T) {
	cases := []struct {
		name   string
		config string
		state  string
	}{
		{
			name:   "name without room for its terminator",
			config: strings.Repeat("n", len(layout.CpModule{}.Name)),
			state:  "state",
		},
		{
			name:   "state name without room for its terminator",
			config: "stash",
			state:  strings.Repeat("s", len(layout.CpObject{}.Name)),
		},
		{
			name:   "state name with a NUL",
			config: "stash",
			state:  "st\x00ate",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() {
				_, err = sdkfixture.NewStashConfig(&agent.Agent{}, tc.config, tc.state)
			})

			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
		})
	}
}
