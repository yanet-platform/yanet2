package bird

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/rib"
)

// bird/lib/net.c#L60
func TestSizeAssert(t *testing.T) {
	require.EqualValues(t, unsafe.Sizeof(netAddrIP4{}), 8)
	require.EqualValues(t, unsafe.Sizeof(netAddrIP6{}), 20)
	require.EqualValues(t, unsafe.Sizeof(netAddrVPN4{}), 16)
	require.EqualValues(t, unsafe.Sizeof(netAddrVPN6{}), 32)
	require.EqualValues(t, unsafe.Sizeof(rib.LargeCommunity{}), 12)
}
