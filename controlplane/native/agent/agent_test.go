//go:build linux && (amd64 || arm64)

package agent_test

import (
	"strings"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// Test_Attach_RefusesBadName verifies that a name the registry cannot hold
// as given is refused before the instance is touched.
//
// The instance has no zones, so any access to them would fault.
func Test_Attach_RefusesBadName(t *testing.T) {
	cases := []struct {
		name  string
		agent string
	}{
		{"name without room for its terminator", strings.Repeat("n", agent.NameSize)},
		{"name with an inner NUL", "probe\x00other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := agent.Attach(&zone.Instance{}, tc.agent, uint64(8*datasize.MB))
			require.Error(t, err)
		})
	}
}

// Test_Attach_RefusesUnreadyInstance verifies that an instance not marked
// ready is refused as not ready before its control-plane zone is touched.
//
// The instance has no control-plane zone, so any access to it would
// fault.
func Test_Attach_RefusesUnreadyInstance(t *testing.T) {
	instance := &zone.Instance{Dp: &layout.DpConfig{}}

	_, err := agent.Attach(instance, "probe", uint64(8*datasize.MB))

	require.ErrorIs(t, err, zone.ErrNotReady)
}
