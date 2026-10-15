package pdump_test

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	pdump "github.com/yanet-platform/yanet2/modules/pdump/controlplane"
	pdumppb "github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
)

// Test_ModuleConfig_SetRate_RejectsBeforeDereference verifies that invalid
// rates are rejected by C before it accesses even an uninitialized config.
func Test_ModuleConfig_SetRate_RejectsBeforeDereference(t *testing.T) {
	config := &pdump.ModuleConfig{}
	for _, rate := range []uint64{pdumppb.MaxRatePPS + 1, ^uint64(0)} {
		require.ErrorIs(t, config.SetRate(rate), syscall.EINVAL)
	}
}
