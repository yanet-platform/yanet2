package filter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
)

// Test_DeviceNameLen_MatchesC verifies that the pure-Go filter device name
// bound matches the C buffer size, including its terminating byte.
func Test_DeviceNameLen_MatchesC(t *testing.T) {
	require.Equal(t, filterpb.MaxDeviceNameLen, filter.MaxDeviceNameLen)
}
