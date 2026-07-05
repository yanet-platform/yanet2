package fwstate

import (
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/testutils"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
)

// SyncPacketOption is a functional option for createSyncPacket
type SyncPacketOption func(*syncPacketConfig)

type syncPacketConfig struct {
	srcPort    uint16
	dstPort    uint16
	srcAddr    string
	dstAddr    string
	isExternal bool
	flags      uint8
	fib        uint8
}

// WithPorts sets custom source and destination ports
func WithPorts(srcPort, dstPort uint16) SyncPacketOption {
	return func(c *syncPacketConfig) {
		c.srcPort = srcPort
		c.dstPort = dstPort
	}
}

// WithAddrs sets custom source and destination addresses
func WithAddrs(srcAddr, dstAddr string) SyncPacketOption {
	return func(c *syncPacketConfig) {
		c.srcAddr = srcAddr
		c.dstAddr = dstAddr
	}
}

// WithExternal marks the packet as external (from outside source)
func WithExternal() SyncPacketOption {
	return func(c *syncPacketConfig) {
		c.isExternal = true
	}
}

// WithFlags sets raw TCP flags byte on the embedded sync frame
func WithFlags(flags uint8) SyncPacketOption {
	return func(c *syncPacketConfig) {
		c.flags = flags
	}
}

// WithFib sets fib (direction) on the embedded sync frame: 0=forward, 1=backward
func WithFib(fib uint8) SyncPacketOption {
	return func(c *syncPacketConfig) {
		c.fib = fib
	}
}

// createSyncPacket creates a firewall state sync packet
// with VLAN + IPv6 + UDP + sync frame structure
func createSyncPacket(t *testing.T, proto layers.IPProtocol, opts ...SyncPacketOption) gopacket.Packet {
	// Apply defaults
	cfg := syncPacketConfig{
		srcPort:    12345,
		dstPort:    9999,
		srcAddr:    "2001:db8::1",
		dstAddr:    "2001:db8::2",
		isExternal: false,
	}

	// Apply options
	for _, opt := range opts {
		opt(&cfg)
	}

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("02:00:00:00:00:00")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("33:33:00:00:00:01")), // Multicast
		EthernetType: layers.EthernetTypeDot1Q,
	}

	vlan := layers.Dot1Q{
		VLANIdentifier: 100,
		Type:           layers.EthernetTypeIPv6,
	}

	var srcIP net.IP
	if cfg.isExternal {
		srcIP = net.ParseIP("2001:db8::1") // External source
	} else {
		srcIP = net.IPv6zero // Internal source (all zeros)
	}

	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   64,
		SrcIP:      srcIP,
		DstIP:      net.ParseIP("ff02::1"), // Multicast destination
	}

	udp := layers.UDP{
		SrcPort: 12345,
		DstPort: 9999, // Sync port
	}
	udp.SetNetworkLayerForChecksum(&ip6)

	// Create sync frame using the helper function
	dstIP6 := net.ParseIP(cfg.dstAddr)
	srcIP6 := net.ParseIP(cfg.srcAddr)
	syncFrame := createSyncFrame(
		proto, 6, cfg.srcPort, cfg.dstPort, dstIP6, srcIP6,
		WithFrameFlags(cfg.flags),
		WithFrameFib(cfg.fib),
	)

	payload := gopacket.Payload(syncFrame)

	return xpacket.LayersToPacket(t, &eth, &vlan, &ip6, &udp, &payload)
}

func TestFWStateInternalPacket(t *testing.T) {
	// Create internal sync packet (should be forwarded)
	pkt := createSyncPacket(t, layers.IPProtocolTCP)
	t.Log("Internal sync packet:", pkt)

	memCtx := testutils.NewMemoryContext("fwstate_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)
	result := xerror.Unwrap(fwstateHandlePackets(cpModule, storage, pkt))

	// Internal packets should be in output
	require.NotEmpty(t, result.Output, "Internal packet should be forwarded")
	require.Empty(t, result.Drop, "Internal packet should not be dropped")
}

func TestFWStateExternalPacket(t *testing.T) {
	// Create external sync packet (should be dropped)
	pkt := createSyncPacket(t, layers.IPProtocolUDP, WithExternal())
	t.Log("External sync packet:", pkt)

	memCtx := testutils.NewMemoryContext("fwstate_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)
	result := xerror.Unwrap(fwstateHandlePackets(cpModule, storage, pkt))

	// External packets should be dropped
	require.Empty(t, result.Output, "External packet should not be forwarded")
	require.NotEmpty(t, result.Drop, "External packet should be dropped")
}

func TestFWStateNonSyncPacket(t *testing.T) {
	// Create a regular (non-sync) packet
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP("192.168.1.1"),
		DstIP:    net.ParseIP("192.168.1.2"),
	}

	udp := layers.UDP{
		SrcPort: 5000,
		DstPort: 8080,
	}
	udp.SetNetworkLayerForChecksum(&ip4)

	payload := gopacket.Payload([]byte("test data"))
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp, &payload)
	t.Log("Non-sync packet:", pkt)

	memCtx := testutils.NewMemoryContext("fwstate_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)
	result := xerror.Unwrap(fwstateHandlePackets(cpModule, storage, pkt))

	// Non-sync packets should pass through
	require.NotEmpty(t, result.Output, "Non-sync packet should pass through")
	require.Empty(t, result.Drop, "Non-sync packet should not be dropped")
}

// Test that sync packets actually create state entries
func TestFWStateStateCreation(t *testing.T) {
	// Create internal sync packet
	pkt := createSyncPacket(t, layers.IPProtocolTCP)
	t.Log("Internal sync packet:", pkt)

	memCtx := testutils.NewMemoryContext("fwstate_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)
	result := xerror.Unwrap(fwstateHandlePackets(cpModule, storage, pkt))

	// Verify packet was processed
	require.NotEmpty(t, result.Output, "Internal packet should be forwarded")

	// Check that state was created
	// For IPv6: src=2001:db8::1, dst=2001:db8::2, proto=TCP, src_port=12345, dst_port=9999
	stateExists := CheckStateExists(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, stateExists, "State should exist after processing sync packet")
}

// Test layer insertion: old state should be visible after adding new layer
func TestFWStateLayerInsertionOldStateVisible(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_layer_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	// Create initial state in the first layer
	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	result1 := xerror.Unwrap(fwstateHandlePackets(cpModule, storage, pkt1))
	require.NotEmpty(t, result1.Output, "First packet should be forwarded")

	// Verify initial state exists
	stateExists := CheckStateExists(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, stateExists, "Initial state should exist")

	// Insert new layer
	InsertNewLayer(cpModule)

	// Old state should still be visible through the new layer
	stateStillExists := CheckStateExists(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, stateStillExists, "Old state should be visible after layer insertion")
}

// Test layer insertion: new states override old states
func TestFWStateLayerInsertionNewStateOverridesOld(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_layer_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	// Create initial state with specific deadline
	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	result1 := xerror.Unwrap(fwstateHandlePackets(cpModule, storage, pkt1))
	require.NotEmpty(t, result1.Output, "First packet should be forwarded")

	// Get initial deadline
	oldDeadline := GetStateDeadline(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.Greater(t, oldDeadline, uint64(0), "Initial state should have deadline")

	// Insert new layer
	InsertNewLayer(cpModule)

	// Add same state to new layer (should override old one)
	pkt2 := createSyncPacket(t, layers.IPProtocolTCP)
	result2 := xerror.Unwrap(fwstateHandlePackets(cpModule, storage, pkt2))
	require.NotEmpty(t, result2.Output, "Second packet should be forwarded")

	// New deadline should be different (newer)
	newDeadline := GetStateDeadline(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.Greater(t, newDeadline, oldDeadline, "New state should have newer deadline")
}

// Test trim functionality: stale layers should be removed
func TestFWStateTrimStaleLayers(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_trim_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	// Create state in first layer with short TTL
	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	result1 := xerror.Unwrap(fwstateHandlePackets(cpModule, storage, pkt1))
	require.NotEmpty(t, result1.Output)

	// Insert new layer
	InsertNewLayer(cpModule)

	// Add different state to new layer
	pkt2 := createSyncPacket(t, layers.IPProtocolUDP, WithPorts(54321, 8888), WithAddrs("2001:db8::3", "2001:db8::4"))
	result2 := xerror.Unwrap(fwstateHandlePackets(cpModule, storage, pkt2))
	require.NotEmpty(t, result2.Output)

	// Check layer count before trim
	_, layerCountBefore := GetLayerCount(cpModule)
	require.Equal(t, uint32(2), layerCountBefore, "Should have 2 layers before trim")

	// Simulate time passing (beyond TTL of old layer)
	futureTime := GetCurrentTime() + 200e9 // 200 seconds in the future

	// Trim stale layers
	require.NoError(t, TrimStaleLayers(cpModule, futureTime))

	// Check layer count after trim
	_, layerCountAfter := GetLayerCount(cpModule)
	require.Equal(t, uint32(1), layerCountAfter, "Should have 1 layer after trim")

	// New state should still exist
	newStateExists := CheckStateExists(cpModule, layers.IPProtocolUDP, 54321, 8888, "2001:db8::3", "2001:db8::4")
	require.True(t, newStateExists, "New state should still exist after trim")

	// Old state should not exist (layer was trimmed)
	oldStateExists := CheckStateExists(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.False(t, oldStateExists, "Old state should not exist after trim")
}

// TestFWStateUpdateAccumulatesFlags verifies that on subsequent sync frames for
// the same 5-tuple in the SAME active layer, TCP flags accumulate (logical OR)
// rather than being overwritten.
//
// This is a regression test for a bug previously present in
// fwmap_update_value_fwstate() (formerly fwmap_copy_value_fwstate): when the
// entry already existed in the active layer (dst_empty=false), the function
// did *d = *s and only preserved created_at, fully clobbering flags /
// packets_* / external with the incoming frame and dropping previously
// accumulated state.
//
// Expected behavior:
//   - First sync frame sets SYN.
//   - Second sync frame (for the same key) sets ACK.
//   - After processing both, stored flags must contain SYN|ACK.
func TestFWStateUpdateAccumulatesFlags(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_acc_flags_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	const synBit = 0x02 // FWSTATE_SYN, src nibble
	const ackBit = 0x08 // FWSTATE_ACK, src nibble

	pktSyn := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(synBit))
	_, err := fwstateHandlePackets(cpModule, storage, pktSyn)
	require.NoError(t, err)

	snap1 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap1.Found, "state must exist after first sync")
	require.Equal(t, uint8(synBit), snap1.FlagsRaw, "after first sync only SYN must be set")

	pktAck := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(ackBit))
	_, err = fwstateHandlePackets(cpModule, storage, pktAck)
	require.NoError(t, err)

	snap2 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap2.Found, "state must still exist after second sync")
	require.Equalf(t, uint8(synBit|ackBit), snap2.FlagsRaw,
		"flags must accumulate via OR across updates in the same layer (got 0x%02x, want 0x%02x)",
		snap2.FlagsRaw, synBit|ackBit)
}

// TestFWStateUpdateAccumulatesPacketCounters verifies that packets_forward /
// packets_backward counters accumulate across updates in the same active layer
// rather than being reset to the last sync frame's contribution.
//
// Each incoming sync frame contributes +1 to either packets_forward (fib=0) or
// packets_backward (fib=1) — see fwstate_build_value() in
// modules/fwstate/dataplane/dataplane.c. Sending N sync frames for the same
// key MUST result in totals equal to the sum of all per-frame contributions.
func TestFWStateUpdateAccumulatesPacketCounters(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_acc_counters_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	const forwardCount = 3
	const backwardCount = 2

	for range forwardCount {
		pkt := createSyncPacket(t, layers.IPProtocolTCP, WithFib(0))
		_, err := fwstateHandlePackets(cpModule, storage, pkt)
		require.NoError(t, err)
	}
	for range backwardCount {
		pkt := createSyncPacket(t, layers.IPProtocolTCP, WithFib(1))
		_, err := fwstateHandlePackets(cpModule, storage, pkt)
		require.NoError(t, err)
	}

	snap := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap.Found, "state must exist after sync frames")

	require.Equalf(t, uint64(forwardCount), snap.PacketsForward,
		"packets_forward must accumulate (got %d, want %d)",
		snap.PacketsForward, forwardCount)
	require.Equalf(t, uint64(backwardCount), snap.PacketsBackward,
		"packets_backward must accumulate (got %d, want %d)",
		snap.PacketsBackward, backwardCount)
}

// TestFWStateUpdatePreservesCreatedAt verifies that the original created_at
// timestamp is preserved across updates within the same active layer by
// fwmap_update_value_fwstate(). Pairs with the accumulation tests above to
// pin down all preservation/merge semantics on the in-layer update path.
func TestFWStateUpdatePreservesCreatedAt(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_created_at_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	_, err := fwstateHandlePackets(cpModule, storage, pkt1)
	require.NoError(t, err)

	snap1 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap1.Found)
	createdAt := snap1.CreatedAt
	require.Greater(t, createdAt, uint64(0))

	// Sleep a tiny bit so that current_time advances between calls.
	// Even without an explicit sleep, the next call uses clock_get_time_ns()
	// which is monotonic — but we rely on observable difference only for
	// updated_at, not for the test assertion itself.
	pkt2 := createSyncPacket(t, layers.IPProtocolTCP)
	_, err = fwstateHandlePackets(cpModule, storage, pkt2)
	require.NoError(t, err)

	snap2 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap2.Found)

	require.Equal(t, createdAt, snap2.CreatedAt,
		"created_at must be preserved across updates")
}

// TestFWStateMergeFromStaleLayer verifies that when an entry exists only in a
// stale (next) layer and a new sync arrives in a fresh active layer, the
// cross-layer promotion path (fwmap_promote_value_fwstate) is used: flags are
// OR-ed and packet counters are summed. This pins the cross-layer merge
// behaviour so that a regression turning it back into "overwrite" is caught.
func TestFWStateMergeFromStaleLayer(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_merge_stale_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	const synBit = 0x02
	const ackBit = 0x08

	// Populate stale layer with SYN + 1 forward packet.
	pkt1 := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(synBit), WithFib(0))
	_, err := fwstateHandlePackets(cpModule, storage, pkt1)
	require.NoError(t, err)

	// Push that layer down, allocate a new active layer.
	InsertNewLayer(cpModule)

	// Insert into the fresh active layer with ACK + 1 backward packet.
	pkt2 := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(ackBit), WithFib(1))
	_, err = fwstateHandlePackets(cpModule, storage, pkt2)
	require.NoError(t, err)

	snap := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap.Found, "merged state must be visible from active layer")
	require.Equalf(t, uint8(synBit|ackBit), snap.FlagsRaw,
		"flags must be merged across layers (got 0x%02x, want 0x%02x)",
		snap.FlagsRaw, synBit|ackBit)
	require.Equalf(t, uint64(1), snap.PacketsForward,
		"packets_forward from stale layer must be carried over (got %d)", snap.PacketsForward)
	require.Equalf(t, uint64(1), snap.PacketsBackward,
		"packets_backward from active layer must be summed in (got %d)", snap.PacketsBackward)
}
