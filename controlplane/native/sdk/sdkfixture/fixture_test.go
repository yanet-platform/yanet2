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

// Test_NewFixtureConfig_RefusesInvalidInput verifies that a name its field
// cannot hold and a range that ends before it starts are refused before the
// agent is touched.
//
// The owner has no agent, so a build that reaches for its memory panics,
// which fails the case.
func Test_NewFixtureConfig_RefusesInvalidInput(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		fixture sdkfixture.Fixture
		want    error
	}{
		{
			name:   "name without room for its terminator",
			config: strings.Repeat("n", len(layout.CpModule{}.Name)),
			want:   cpconfig.ErrInvalidName,
		},
		{
			name:   "range that ends before it starts",
			config: "config",
			fixture: sdkfixture.Fixture{Routes: []sdkfixture.Range4{
				{From: [4]byte{10}, To: [4]byte{10, 0, 0, 255}},
				{From: [4]byte{10, 0, 0, 2}, To: [4]byte{10, 0, 0, 1}},
			}},
			want: sdkfixture.ErrInvalidArgument,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() {
				_, err = sdkfixture.NewFixtureConfig(&agent.Agent{}, tc.config, tc.fixture)
			})

			require.ErrorIs(t, err, tc.want)
		})
	}
}
