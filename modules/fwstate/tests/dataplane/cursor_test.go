package fwstate

import (
	"encoding/binary"
	"math"
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/testutils"
)

// IPPROTO constants matching C definitions.
const (
	protoTCP uint16 = 6
	protoUDP uint16 = 17
)

// TCP flag constants matching C FWSTATE_ definitions.
const (
	flagACK uint8 = 0x08
	flagSYN uint8 = 0x02
)

// ipToUint32 converts an IPv4 string to a uint32 in network byte order.
func ipToUint32(s string) uint32 {
	ip := net.ParseIP(s).To4()
	if ip == nil {
		panic("invalid IPv4 address: " + s)
	}
	return binary.LittleEndian.Uint32(ip)
}

func TestCursorForwardRead(t *testing.T) {
	memCtx := testutils.NewMemoryContext("cursor_fwd", datasize.MB*64)
	defer memCtx.Free()
	cpModule := fwstateModuleConfig(memCtx)

	now := uint64(GetCurrentTime())

	// Insert 5 IPv4 entries with distinct ports.
	for i := range 5 {
		err := insertFw4Entry(cpModule,
			protoTCP, uint16(1000+i), 80,
			ipToUint32("10.0.0.1"), ipToUint32("192.168.0.1"),
			flagACK, flagACK,
			now, now,
		)
		require.NoError(t, err, "insert entry %d", i)
	}

	results, newIdx, err := readCursorForward(cpModule,
		false, 0, 0, true, now, 10,
	)
	require.NoError(t, err)
	require.Len(t, results, 5)
	require.Equal(t, int64(5), newIdx)

	// Verify ascending idx order.
	for i, r := range results {
		require.Equal(t, uint32(i), r.Idx)
		require.Equal(t, uint16(1000+i), r.SrcPort)
		require.Equal(t, uint16(80), r.DstPort)
		require.Equal(t, protoTCP, r.Proto)
	}
}

func TestCursorBackwardRead(t *testing.T) {
	memCtx := testutils.NewMemoryContext("cursor_bwd", datasize.MB*64)
	defer memCtx.Free()
	cpModule := fwstateModuleConfig(memCtx)

	now := uint64(GetCurrentTime())

	for i := range 5 {
		err := insertFw4Entry(cpModule,
			protoTCP, uint16(2000+i), 443,
			ipToUint32("10.0.0.1"), ipToUint32("192.168.0.2"),
			flagACK, flagACK,
			now, now,
		)
		require.NoError(t, err)
	}

	results, newIdx, err := readCursorBackward(cpModule,
		false, 0, math.MaxUint32, true, now, 10,
	)
	require.NoError(t, err)
	require.Len(t, results, 5)
	require.Equal(t, int64(-1), newIdx)

	// Verify descending idx order (4..0).
	for i, r := range results {
		require.Equal(t, uint32(4-i), r.Idx)
		require.Equal(t, uint16(2000+4-i), r.SrcPort)
		require.Equal(t, uint16(443), r.DstPort)
		require.Equal(t, protoTCP, r.Proto)
	}
}

func TestCursorExpiredFiltering(t *testing.T) {
	memCtx := testutils.NewMemoryContext("cursor_exp", datasize.MB*64)
	defer memCtx.Free()
	cpModule := fwstateModuleConfig(memCtx)

	now := uint64(GetCurrentTime())

	// Insert TCP entry (120s TTL).
	err := insertFw4Entry(cpModule,
		protoTCP, 3000, 80,
		ipToUint32("10.0.0.1"), ipToUint32("192.168.0.1"),
		flagACK, flagACK,
		now, now,
	)
	require.NoError(t, err)

	// Insert UDP entry (30s TTL).
	err = insertFw4Entry(cpModule,
		protoUDP, 3001, 53,
		ipToUint32("10.0.0.2"), ipToUint32("192.168.0.1"),
		0, 0,
		now, now,
	)
	require.NoError(t, err)

	// Insert TCP entry (120s TTL).
	err = insertFw4Entry(cpModule,
		protoTCP, 3002, 443,
		ipToUint32("10.0.0.3"), ipToUint32("192.168.0.1"),
		flagACK, flagACK,
		now, now,
	)
	require.NoError(t, err)

	// Advance time past UDP TTL (30s) but not TCP (120s).
	readNow := now + uint64(31e9)

	// With include_expired=false, should skip the UDP entry.
	results, _, err := readCursorForward(cpModule,
		false, 0, 0, false, readNow, 10,
	)
	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, r := range results {
		require.Equal(t, uint16(protoTCP), r.Proto)
	}

	// With include_expired=true, should return all 3.
	results, _, err = readCursorForward(cpModule,
		false, 0, 0, true, readNow, 10,
	)
	require.NoError(t, err)
	require.Len(t, results, 3)
}

func TestCursorKeyDataCorrectness(t *testing.T) {
	memCtx := testutils.NewMemoryContext("cursor_key", datasize.MB*64)
	defer memCtx.Free()
	cpModule := fwstateModuleConfig(memCtx)

	now := uint64(GetCurrentTime())

	srcAddr := ipToUint32("172.16.5.10")
	dstAddr := ipToUint32("10.20.30.40")

	err := insertFw4Entry(cpModule,
		protoTCP, 12345, 8080,
		srcAddr, dstAddr,
		flagSYN, 0,
		now, now,
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
	cpModule := fwstateModuleConfig(memCtx)

	now := uint64(GetCurrentTime())

	err := insertFw4Entry(cpModule,
		protoTCP, 9000, 80,
		ipToUint32("10.0.0.1"), ipToUint32("192.168.0.1"),
		flagACK, flagACK,
		now-1000, now,
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
	cpModule := fwstateModuleConfig(memCtx)

	now := uint64(GetCurrentTime())

	// layer_index=99 should fail.
	_, _, err := readCursorForward(cpModule,
		false, 99, 0, true, now, 10,
	)
	require.Error(t, err)
}

func TestCursorPaging(t *testing.T) {
	memCtx := testutils.NewMemoryContext("cursor_page", datasize.MB*64)
	defer memCtx.Free()
	cpModule := fwstateModuleConfig(memCtx)

	now := uint64(GetCurrentTime())

	// Insert 10 entries.
	for i := range 10 {
		err := insertFw4Entry(cpModule,
			protoTCP, uint16(6000+i), 80,
			ipToUint32("10.0.0.1")+uint32(i), ipToUint32("192.168.0.1"),
			flagACK, flagACK,
			now, now,
		)
		require.NoError(t, err)
	}

	// Read in batches of 3.
	var allResults []CursorResult

	var idx int64
	for {
		results, newIdx, err := readCursorForward(cpModule,
			false, 0, idx, true, now, 3,
		)
		require.NoError(t, err)
		if len(results) == 0 {
			break
		}
		allResults = append(allResults, results...)
		idx = newIdx
	}

	require.Len(t, allResults, 10)
	require.Equal(t, int64(10), idx)

	// Verify all entries covered.
	for i, r := range allResults {
		require.Equal(t, uint32(i), r.Idx)
		require.Equal(t, uint16(6000+i), r.SrcPort)
	}
}
