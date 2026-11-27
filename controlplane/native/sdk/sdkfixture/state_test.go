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
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// Test_NewStateObject_RefusesInvalidInput verifies that a name its field
// cannot hold and slots past the largest block are refused before the agent
// is touched.
//
// The owner has no agent, so a build that reaches for its memory panics,
// which fails the case.
func Test_NewStateObject_RefusesInvalidInput(t *testing.T) {
	cases := []struct {
		name   string
		object string
		slots  uint64
		want   error
	}{
		{
			name:   "name without room for its terminator",
			object: strings.Repeat("n", len(layout.CpObject{}.Name)),
			want:   cpconfig.ErrInvalidName,
		},
		{
			name:   "slots past the largest block",
			object: "state",
			slots:  uint64(shmalloc.MaxSize)/8 + 1,
			want:   sdkfixture.ErrInvalidArgument,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			require.NotPanics(t, func() {
				_, err = sdkfixture.NewStateObject(&agent.Agent{}, tc.object, 10, tc.slots)
			})

			require.ErrorIs(t, err, tc.want)
		})
	}
}
