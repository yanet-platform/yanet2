package cfwstate_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	fwstatepb "github.com/yanet-platform/yanet2/modules/fwstate/controlplane/fwstatepb/v1"
)

// Test_TTL48Max_MatchesC verifies that the Go request bound matches the C
// storage bound.
func Test_TTL48Max_MatchesC(t *testing.T) {
	require.Equal(t, fwstatepb.TTL48Max, cfwstate.TTL48Max)
}

// Test_MinSyncMTU_MatchesC verifies that the Go request bound matches the C
// packet bound.
func Test_MinSyncMTU_MatchesC(t *testing.T) {
	require.Equal(t, fwstatepb.MinSyncMTU, uint32(cfwstate.MinSyncMTU))
}
