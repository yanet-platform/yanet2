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

// Test_NewTallyConfig_RefusesNameItsFieldCannotHold verifies that a name its
// field cannot hold is refused before the agent is touched.
//
// The owner has no agent, so a build that reaches for its memory panics,
// which fails the test.
func Test_NewTallyConfig_RefusesNameItsFieldCannotHold(t *testing.T) {
	var err error
	require.NotPanics(t, func() {
		_, err = sdkfixture.NewTallyConfig(&agent.Agent{}, strings.Repeat("n", len(layout.CpModule{}.Name)), 7, true)
	})

	require.ErrorIs(t, err, cpconfig.ErrInvalidName)
}
