package fwstate

import (
	"encoding/binary"
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/testutils"
)

// IPPROTO constants matching C definitions.
const (
	protoTCP uint16 = 6
)

// TCP flag constants matching C FWSTATE_ definitions.
const (
	flagACK uint8 = 0x08
	flagSYN uint8 = 0x02
)

// Match fwstateModuleConfig sync timeouts (nanoseconds).
const (
	ttlTCPNs = uint64(120e9)
)

// ipToUint32 converts an IPv4 string to a uint32 in network byte order.
func ipToUint32(s string) uint32 {
	ip := net.ParseIP(s).To4()
	if ip == nil {
		panic("invalid IPv4 address: " + s)
	}
	return binary.LittleEndian.Uint32(ip)
}

func TestCursorKeyDataCorrectness(t *testing.T) {
	memCtx := testutils.NewMemoryContext("cursor_key", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	now := uint64(GetCurrentTime())

	srcAddr := ipToUint32("172.16.5.10")
	dstAddr := ipToUint32("10.20.30.40")

	err := insertFw4Entry(cpModule,
		protoTCP, 12345, 8080,
		srcAddr, dstAddr,
		flagSYN, 0,
		now, now, ttlTCPNs,
	)
	require.NoError(t, err)

	results, _, err := readCursorForward(cpModule,
		false, 0, 0, true, now, 1,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)

	r := results[0]
	require.Equal(t, protoTCP, r.Proto)
	require.Equal(t, uint16(12345), r.SrcPort)
	require.Equal(t, uint16(8080), r.DstPort)
	require.Equal(t, srcAddr, r.SrcAddr)
	require.Equal(t, dstAddr, r.DstAddr)
}

func TestCursorValueDataCorrectness(t *testing.T) {
	memCtx := testutils.NewMemoryContext("cursor_val", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	now := uint64(GetCurrentTime())

	err := insertFw4Entry(cpModule,
		protoTCP, 9000, 80,
		ipToUint32("10.0.0.1"), ipToUint32("192.168.0.1"),
		flagACK, flagACK,
		now-1000, now, ttlTCPNs,
	)
	require.NoError(t, err)

	results, _, err := readCursorForward(cpModule,
		false, 0, 0, true, now, 1,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)

	r := results[0]
	require.Equal(t, uint64(now), r.UpdatedAt)
	require.Equal(t, uint64(1), r.PktForward)
	require.Equal(t, uint64(0), r.PktBackward)
}

func TestCursorInvalidLayer(t *testing.T) {
	memCtx := testutils.NewMemoryContext("cursor_inv", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	now := uint64(GetCurrentTime())

	// The layer index 99 should fail.
	_, _, err := readCursorForward(cpModule,
		false, 99, 0, true, now, 10,
	)
	require.Error(t, err)
}
