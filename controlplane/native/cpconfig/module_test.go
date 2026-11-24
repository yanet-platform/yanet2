//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// Test_NewBlackhole_RefusesNameItsFieldCannotHold verifies that a name C
// would cut short is refused before the agent is touched.
//
// The owner has no agent, so any allocation from it would fault.
func Test_NewBlackhole_RefusesNameItsFieldCannotHold(t *testing.T) {
	cases := []struct {
		name   string
		module string
	}{
		{"name without room for its terminator", strings.Repeat("n", len(layout.CpModule{}.Name))},
		{"name with an inner NUL", "probe\x00other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cpconfig.NewBlackhole(&agent.Agent{}, tc.module)
			require.Error(t, err)
		})
	}
}
