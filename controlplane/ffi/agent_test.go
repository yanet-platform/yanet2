package ffi_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// TestValidateDeviceName pins the two rejection classes: a name the C-side
// buffer cannot hold and a name with an embedded NUL byte.
func TestValidateDeviceName(t *testing.T) {
	require.NoError(t, ffi.ValidateDeviceName(strings.Repeat("a", ffi.MaxDeviceNameLen-1)))
	require.Error(t, ffi.ValidateDeviceName(strings.Repeat("a", ffi.MaxDeviceNameLen)))
	require.Error(t, ffi.ValidateDeviceName("edge\x00backup"))
}
