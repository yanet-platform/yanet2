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
	srcPort uint16
	dstPort uint16
	srcAddr string
	dstAddr string
	flags   uint8
	fib     uint8
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
		srcPort: 12345,
		dstPort: 9999,
		srcAddr: "2001:db8::1",
		dstAddr: "2001:db8::2",
	}

	// Apply options
	for _, opt := range opts {
		opt(&cfg)
	}

	dstMAC := xerror.Unwrap(net.ParseMAC("33:33:00:00:00:01"))
	outerDstIP := net.ParseIP("ff02::1")
	outerDstPort := layers.UDPPort(9999)

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("02:00:00:00:00:00")),
		DstMAC:       dstMAC,
		EthernetType: layers.EthernetTypeDot1Q,
	}

	vlan := layers.Dot1Q{
		VLANIdentifier: 100,
		Type:           layers.EthernetTypeIPv6,
	}

	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   64,
		SrcIP:      net.IPv6zero,
		DstIP:      outerDstIP,
	}

	udp := layers.UDP{
		SrcPort: 12345,
		DstPort: outerDstPort,
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

// Test_FWState_FlaggedPacketPassesThrough verifies that a sync packet an
// upstream fwstate emitted travels on byte-identical without touching the
// table, even when it matches the multicast receive contract.
func Test_FWState_FlaggedPacketPassesThrough(t *testing.T) {
	pkt := createSyncPacket(t, layers.IPProtocolTCP)

	memCtx := testutils.NewMemoryContext("fwstate_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)
	result := xerror.Unwrap(fwstateHandleFlaggedPackets(cpModule, storage, pkt))

	require.Equal(t, [][]byte{pkt.Data()}, result.Output)
	require.Empty(t, result.Drop)
	require.False(t,
		CheckStateExists(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2"),
		"a local emission must not be applied again",
	)
	require.Equal(t, uint64(1), moduleCounter(cpModule, storage, "fwstate_passthrough"))
	require.Zero(t, moduleCounter(cpModule, storage, "fwstate_sync"))
}

// Test_FWStateLocalEvent_UnicastOnly verifies that a stashed event is still
// applied and emitted when multicast reception is disabled.
func Test_FWStateLocalEvent_UnicastOnly(t *testing.T) {
	pkt := createSyncPacket(t, layers.IPProtocolTCP)

	memCtx := testutils.NewMemoryContext("fwstate_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)
	ClearSyncDestination(cpModule)
	SetSyncUnicastDestination(cpModule)

	result := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, storage, pkt))
	require.Len(t, result.Output, 1, "unicast-only local event should be emitted")
	require.Empty(t, result.Drop)

	parsed := gopacket.NewPacket(result.Output[0], layers.LayerTypeEthernet, gopacket.Default)
	ipv6, ok := parsed.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
	require.True(t, ok, "emitted packet should retain its IPv6 header")
	require.Equal(t, net.ParseIP("2001:db8::3"), ipv6.DstIP)
	udp, ok := parsed.Layer(layers.LayerTypeUDP).(*layers.UDP)
	require.True(t, ok, "emitted packet should retain its UDP header")
	require.Equal(t, layers.UDPPort(10000), udp.DstPort)
}

// Test_FWState_WirePacketWithUnspecifiedSource verifies that a received wire
// sync packet is consumed without emitting new copies.
func Test_FWState_WirePacketWithUnspecifiedSource(t *testing.T) {
	pkt := createSyncPacket(t, layers.IPProtocolUDP)

	memCtx := testutils.NewMemoryContext("fwstate_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)
	result := xerror.Unwrap(fwstateHandleWirePackets(cpModule, storage, pkt))

	require.Empty(t, result.Output, "wire packet must not be emitted")
	require.NotEmpty(t, result.Drop, "wire packet must be consumed")
}

// Test_FWStateModule_UnconfiguredSync_AppliesLocalEventWithoutEmission
// verifies that a module carrying no sync destination still applies a
// stashed event but takes no mbuf from the pool and emits nothing for it.
func Test_FWStateModule_UnconfiguredSync_AppliesLocalEventWithoutEmission(t *testing.T) {
	pkt := createSyncPacket(t, layers.IPProtocolUDP)

	memCtx := testutils.NewMemoryContext("fwstate_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)
	ClearSyncDestination(cpModule)

	outstanding := poolOutstanding(testWorker(0))
	result := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, storage, pkt))

	require.Equal(t, outstanding, poolOutstanding(testWorker(0)),
		"no endpoint means no mbuf is allocated")
	require.Empty(t, result.Output, "no endpoint means no emission")
	require.Empty(t, result.Drop, "no packet exists to drop")
	require.True(t,
		CheckStateExists(cpModule, layers.IPProtocolUDP, 12345, 9999, "2001:db8::1", "2001:db8::2"),
		"the event must still be applied",
	)
	require.Zero(t, moduleCounter(cpModule, storage, "fwstate_internal_forwarded"))
}

// Test_FWState_WirePacketWithoutMulticastEndpoint verifies that disabling the
// receive endpoint leaves matching wire packets in ordinary processing.
func Test_FWState_WirePacketWithoutMulticastEndpoint(t *testing.T) {
	pkt := createSyncPacket(t, layers.IPProtocolUDP)

	memCtx := testutils.NewMemoryContext("fwstate_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)
	ClearSyncDestination(cpModule)
	result := xerror.Unwrap(fwstateHandleWirePackets(cpModule, storage, pkt))

	require.Len(t, result.Output, 1, "wire receive is disabled without multicast")
	require.Empty(t, result.Drop)
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
	result := xerror.Unwrap(fwstateHandleWirePackets(cpModule, storage, pkt))

	// Non-sync packets should pass through
	require.NotEmpty(t, result.Output, "Non-sync packet should pass through")
	require.Empty(t, result.Drop, "Non-sync packet should not be dropped")
}

// Test layer insertion: old state should be visible after adding new layer
func TestFWStateLayerInsertionOldStateVisible(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_layer_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	// Create initial state in the first layer
	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	result1 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, storage, pkt1))
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
	result1 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, storage, pkt1))
	require.NotEmpty(t, result1.Output, "First packet should be forwarded")

	// Get initial deadline
	oldDeadline := GetStateDeadline(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.Greater(t, oldDeadline, uint64(0), "Initial state should have deadline")

	// Insert new layer
	InsertNewLayer(cpModule)

	// Add same state to new layer (should override old one)
	pkt2 := createSyncPacket(t, layers.IPProtocolTCP)
	result2 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, storage, pkt2))
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
	result1 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, storage, pkt1))
	require.NotEmpty(t, result1.Output)

	// Insert new layer
	InsertNewLayer(cpModule)

	// Add different state to new layer
	pkt2 := createSyncPacket(t, layers.IPProtocolUDP, WithPorts(54321, 8888), WithAddrs("2001:db8::3", "2001:db8::4"))
	result2 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, storage, pkt2))
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
	_, err := fwstateHandleLocalEvents(cpModule, storage, pktSyn)
	require.NoError(t, err)

	snap1 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap1.Found, "state must exist after first sync")
	require.Equal(t, uint8(synBit), snap1.FlagsRaw, "after first sync only SYN must be set")

	pktAck := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(ackBit))
	_, err = fwstateHandleLocalEvents(cpModule, storage, pktAck)
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
		_, err := fwstateHandleLocalEvents(cpModule, storage, pkt)
		require.NoError(t, err)
	}
	for range backwardCount {
		pkt := createSyncPacket(t, layers.IPProtocolTCP, WithFib(1))
		_, err := fwstateHandleLocalEvents(cpModule, storage, pkt)
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
	_, err := fwstateHandleLocalEvents(cpModule, storage, pkt1)
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
	_, err = fwstateHandleLocalEvents(cpModule, storage, pkt2)
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
	_, err := fwstateHandleLocalEvents(cpModule, storage, pkt1)
	require.NoError(t, err)

	// Push that layer down, allocate a new active layer.
	InsertNewLayer(cpModule)

	// Insert into the fresh active layer with ACK + 1 backward packet.
	pkt2 := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(ackBit), WithFib(1))
	_, err = fwstateHandleLocalEvents(cpModule, storage, pkt2)
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

// TestFWStateSyncSuppression verifies that a fully suppressed local event is
// not emitted.
func TestFWStateSyncSuppression(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_suppress_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	// 60s window: any second frame arriving within the test's real-time span
	// is well inside the window and must be suppressed.
	const suppressNs = uint64(60e9)
	SetSyncSuppressTimeout(cpModule, suppressNs)

	// First frame: entry does not exist yet, so it is applied and forwarded.
	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	res1 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, storage, pkt1))
	require.NotEmpty(t, res1.Output, "first frame must be forwarded")
	require.Empty(t, res1.Drop, "first frame must not be dropped")

	snap1 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap1.Found, "state must exist after first frame")
	require.Greater(t, snap1.Deadline, uint64(0))

	// Second frame, same 5-tuple, arrives immediately: within the window, so
	// the state record and the wire are both left untouched.
	pkt2 := createSyncPacket(t, layers.IPProtocolTCP)
	res2 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, storage, pkt2))
	require.Empty(t, res2.Output)
	require.Empty(t, res2.Drop)
	require.Equal(t, recordSuppressed, stashRecordStatus(cpModule, true, 0, 0))

	snap2 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap2.Found)
	require.Equal(t, snap1.UpdatedAt, snap2.UpdatedAt,
		"updated_at must not change when the frame is suppressed")
	require.Equal(t, snap1.Deadline, snap2.Deadline,
		"deadline must not change when the frame is suppressed")
}

// TestFWStateSyncSuppressionDisabled verifies that a zero window refreshes the
// state record on every frame.
func TestFWStateSyncSuppressionDisabled(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_suppress_off_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)
	// suppress left at its default of 0 (disabled)

	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	res1 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, storage, pkt1))
	require.NotEmpty(t, res1.Output, "first frame must be forwarded")
	snap1 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap1.Found)

	pkt2 := createSyncPacket(t, layers.IPProtocolTCP)
	xerror.Unwrap(fwstateHandleLocalEvents(cpModule, storage, pkt2))
	snap2 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap2.Found)
	require.GreaterOrEqual(t, snap2.UpdatedAt, snap1.UpdatedAt,
		"updated_at must advance when the frame is applied")
}

// TestFWStateSyncSuppressionAllowsShorterTTL verifies that a frame carrying a
// shorter TTL (a teardown transition such as FIN) is applied even when it
// arrives inside the suppress window, instead of being suppressed and leaving
// the entry on its longer established deadline.
func TestFWStateSyncSuppressionAllowsShorterTTL(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_suppress_ttl_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	// Established TTL much longer than the FIN TTL, so the FIN's new expiry
	// lands below the current deadline and must not be suppressed.
	SetSyncTCPTimeouts(cpModule, 120e9, 10e9)
	SetSyncSuppressTimeout(cpModule, 60e9)

	// Established frame (no flags): entry created on the long TTL.
	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	res1 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, storage, pkt1))
	require.NotEmpty(t, res1.Output, "established frame must be forwarded")
	snap1 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap1.Found)

	// FIN frame immediately after: shorter TTL must be applied, not
	// suppressed, so the deadline shrinks toward the FIN timeout.
	const finBit = 0x01 // FWSTATE_FIN, src nibble
	pkt2 := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(finBit))
	xerror.Unwrap(fwstateHandleLocalEvents(cpModule, storage, pkt2))

	snap2 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap2.Found)
	require.Less(t, snap2.Deadline, snap1.Deadline,
		"FIN (shorter TTL) must shrink the deadline instead of being suppressed")
}

// TestFWStateSyncSuppressionAppliesFlagChanges verifies that a frame carrying
// a flag bit the entry has not yet seen is applied even when its TTL is
// identical to the current one (so the deadline predicate alone would
// suppress it). In the default test config tcp_syn == tcp_syn_ack, so a SYN
// followed by an ACK has the same deadline; the ACK must still merge.
func TestFWStateSyncSuppressionAppliesFlagChanges(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_suppress_flags_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	// Default test config sets tcp_syn == tcp_syn_ack, so both frames below
	// pick the same TTL.
	SetSyncSuppressTimeout(cpModule, 60e9)

	const synBit = 0x02 // FWSTATE_SYN, src nibble
	const ackBit = 0x08 // FWSTATE_ACK, src nibble

	pkt1 := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(synBit))
	xerror.Unwrap(fwstateHandleLocalEvents(cpModule, storage, pkt1))
	snap1 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap1.Found)
	require.Equal(t, uint8(synBit), snap1.FlagsRaw)

	// ACK frame, same TTL, within the window: must not be suppressed because
	// it carries a new flag bit that the merge path must record.
	pkt2 := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(ackBit))
	xerror.Unwrap(fwstateHandleLocalEvents(cpModule, storage, pkt2))

	snap2 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap2.Found)
	require.Equalf(t, uint8(synBit|ackBit), snap2.FlagsRaw,
		"ACK must merge into flags even at an unchanged TTL within the window (got 0x%02x)",
		snap2.FlagsRaw)
}

// Test_FWState_StashEmission_BatchesByDefaultMTU verifies that 30 applied
// records on one TX device leave in packets of 25 and 5 frames, each frame
// byte-identical to its record and in record order.
func Test_FWState_StashEmission_BatchesByDefaultMTU(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_stash_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	worker := testWorker(0)
	nextRound(worker)
	var events []localEvent
	var want []byte
	for idx := range 30 {
		event := v6Event(uint16(1000+idx), 0, 0)
		events = append(events, event)
		want = append(want, event.frame...)
	}
	require.Equal(t, len(events), stashEvents(cpModule, worker, events...))

	result := runHandler(cpModule, storage, worker, false)

	require.Len(t, result.Output, 2)
	var got []byte
	var frames []int
	for _, raw := range result.Output {
		_, _, payload := syncPayload(raw)
		frames = append(frames, len(payload)/frameSize)
		got = append(got, payload...)
	}
	require.Equal(t, []int{25, 5}, frames)
	require.Equal(t, want, got)
	require.Equal(t, uint64(30), moduleCounter(cpModule, storage, "fwstate_sync_v6_inserted"))
	require.Equal(t, uint64(2), moduleCounter(cpModule, storage, "fwstate_internal_forwarded"))
}

// Test_FWState_StashEmission_BatchesByCustomMTU verifies that a sync MTU
// holding two frames splits five records into packets of 2, 2 and 1.
func Test_FWState_StashEmission_BatchesByCustomMTU(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_stash_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)
	SetSyncMTU(cpModule, uint16(40+8+2*frameSize))

	worker := testWorker(0)
	nextRound(worker)
	for idx := range 5 {
		require.Equal(t, 1, stashEvents(cpModule, worker, v6Event(uint16(2000+idx), 0, 0)))
	}

	result := runHandler(cpModule, storage, worker, false)

	var frames []int
	for _, raw := range result.Output {
		_, _, payload := syncPayload(raw)
		frames = append(frames, len(payload)/frameSize)
	}
	require.Equal(t, []int{2, 2, 1}, frames)
}

// Test_FWState_StashEmission_SplitsOnTxDevice verifies that a change of TX
// device starts a new packet and that each packet takes its RX device from
// the last record it carries.
func Test_FWState_StashEmission_SplitsOnTxDevice(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_stash_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	worker := testWorker(0)
	nextRound(worker)
	txDevices := []uint16{1, 1, 2, 2, 1}
	for idx, tx := range txDevices {
		event := v6Event(uint16(3000+idx), uint16(10+idx), tx)
		require.Equal(t, 1, stashEvents(cpModule, worker, event))
	}

	result := runHandler(cpModule, storage, worker, false)

	require.Len(t, result.OutputData, 3)
	var tx, rx, frames []int
	for _, data := range result.OutputData {
		tx = append(tx, int(data.TxDeviceId))
		rx = append(rx, int(data.RxDeviceId))
		_, _, payload := syncPayload(data.Payload)
		frames = append(frames, len(payload)/frameSize)
	}
	require.Equal(t, []int{1, 2, 1}, tx)
	require.Equal(t, []int{11, 13, 14}, rx)
	require.Equal(t, []int{2, 2, 1}, frames)
}

// Test_FWState_StashEmission_BothEndpoints verifies that every batch is
// emitted once to the multicast and once to the unicast endpoint.
func Test_FWState_StashEmission_BothEndpoints(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_stash_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)
	SetSyncUnicastDestination(cpModule)

	worker := testWorker(0)
	nextRound(worker)
	for idx := range 30 {
		require.Equal(t, 1, stashEvents(cpModule, worker, v6Event(uint16(4000+idx), 0, 0)))
	}

	result := runHandler(cpModule, storage, worker, false)

	type emitted struct {
		destination string
		port        uint16
		frames      int
	}
	var got []emitted
	for _, raw := range result.Output {
		destination, port, payload := syncPayload(raw)
		got = append(got, emitted{destination.String(), port, len(payload) / frameSize})
	}
	require.Equal(t, []emitted{
		{"ff02::1", 9999, 25},
		{"2001:db8::3", 10000, 25},
		{"ff02::1", 9999, 5},
		{"2001:db8::3", 10000, 5},
	}, got)
}

// Test_FWState_StashEmission_PacksBothFamilies verifies that v4 and v6
// records of one round share a packet, v4 first, and are applied to their
// own family tables.
func Test_FWState_StashEmission_PacksBothFamilies(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_stash_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	worker := testWorker(0)
	nextRound(worker)
	event6 := v6Event(5000, 0, 0)
	event4 := v4Event(5001)
	require.Equal(t, 2, stashEvents(cpModule, worker, event6, event4))

	result := runHandler(cpModule, storage, worker, false)

	require.Len(t, result.Output, 1)
	_, _, payload := syncPayload(result.Output[0])
	require.Equal(t, append(append([]byte(nil), event4.frame...), event6.frame...), payload)
	require.Equal(t, uint64(1), moduleCounter(cpModule, storage, "fwstate_sync_v4_inserted"))
	require.Equal(t, uint64(1), moduleCounter(cpModule, storage, "fwstate_sync_v6_inserted"))
}

// Test_FWState_Stash_RepeatInvocationHandlesOnlyNewRecords verifies that
// invoking one configuration again in the same round, as a redirect or a
// second placement does, handles only the records appended since its
// previous invocation.
func Test_FWState_Stash_RepeatInvocationHandlesOnlyNewRecords(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_stash_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	worker := testWorker(0)
	nextRound(worker)
	require.Equal(t, 2, stashEvents(cpModule, worker, v6Event(6000, 0, 0), v6Event(6001, 0, 0)))
	first := runHandler(cpModule, storage, worker, false)

	event := v6Event(6002, 0, 0)
	require.Equal(t, 1, stashEvents(cpModule, worker, event))
	second := runHandler(cpModule, storage, worker, false)
	third := runHandler(cpModule, storage, worker, false)

	require.Len(t, first.Output, 1)
	_, _, firstPayload := syncPayload(first.Output[0])
	require.Len(t, firstPayload, 2*frameSize)
	require.Len(t, second.Output, 1)
	_, _, secondPayload := syncPayload(second.Output[0])
	require.Equal(t, event.frame, secondPayload)
	require.Empty(t, third.Output)
	require.Equal(t, uint64(3), moduleCounter(cpModule, storage, "fwstate_sync_v6_inserted"))
}

// Test_FWState_Stash_SecondConfigEmitsWithoutDeciding verifies that a
// second configuration on the same map passes the first one's emission
// through, never inserts or suppresses a record again, and emits exactly
// the applied records to its own endpoint.
func Test_FWState_Stash_SecondConfigEmitsWithoutDeciding(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_stash_test", datasize.MB*64)
	defer memCtx.Free()
	first, firstStorage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(firstStorage)
	SetSyncSuppressTimeout(first, 60e9)
	second, secondStorage := fwstateSiblingModuleConfig(first, "second")
	defer fwstateCounterStorageFree(secondStorage)

	worker := testWorker(0)

	// Round one: two new records, both applied by the first config.
	nextRound(worker)
	refreshed := v6Event(7000, 0, 0)
	require.Equal(t, 2, stashEvents(first, worker, refreshed, v6Event(7001, 0, 0)))
	firstRound := runHandler(first, firstStorage, worker, false)
	require.Len(t, firstRound.Output, 1)
	upstream := gopacket.NewPacket(firstRound.Output[0], layers.LayerTypeEthernet, gopacket.Default)
	secondRound := runHandler(second, secondStorage, worker, true, upstream)

	require.Len(t, secondRound.Output, 2)
	require.Equal(t, firstRound.Output[0], secondRound.Output[0],
		"the upstream emission must pass through unchanged")
	destination, port, payload := syncPayload(secondRound.Output[1])
	require.Equal(t, "2001:db8::3", destination.String())
	require.Equal(t, uint16(10000), port)
	_, _, upstreamPayload := syncPayload(firstRound.Output[0])
	require.Equal(t, upstreamPayload, payload)

	// Round two: a suppressible refresh and a new record.
	nextRound(worker)
	fresh := v6Event(7002, 0, 0)
	require.Equal(t, 2, stashEvents(first, worker, refreshed, fresh))
	firstRound = runHandler(first, firstStorage, worker, false)
	secondRound = runHandler(second, secondStorage, worker, false)

	require.Equal(t, recordSuppressed, stashRecordStatus(first, true, 0, 0))
	require.Equal(t, recordApplied, stashRecordStatus(first, true, 0, 1))
	require.Len(t, firstRound.Output, 1)
	_, _, payload = syncPayload(firstRound.Output[0])
	require.Equal(t, fresh.frame, payload)
	require.Len(t, secondRound.Output, 1)
	_, _, payload = syncPayload(secondRound.Output[0])
	require.Equal(t, fresh.frame, payload)

	require.Equal(t, uint64(3), moduleCounter(first, firstStorage, "fwstate_sync_v6_inserted"))
	require.Equal(t, uint64(1), moduleCounter(first, firstStorage, "fwstate_sync_v6_suppressed"))
	require.Zero(t, moduleCounter(second, secondStorage, "fwstate_sync_v6_inserted"))
	require.Zero(t, moduleCounter(second, secondStorage, "fwstate_sync_v6_suppressed"))
	require.Equal(t, uint64(1), moduleCounter(second, secondStorage, "fwstate_passthrough"))
}

// Test_FWState_Stash_NewRoundDropsUnprocessedRecords verifies that records
// left unprocessed when a round ends are never applied or emitted.
func Test_FWState_Stash_NewRoundDropsUnprocessedRecords(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_stash_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	worker := testWorker(0)
	nextRound(worker)
	require.Equal(t, 1, stashEvents(cpModule, worker, v6Event(8000, 0, 0)))
	nextRound(worker)

	result := runHandler(cpModule, storage, worker, false)

	require.Empty(t, result.Output)
	require.Zero(t, stashSlotCount(cpModule, true, 0))
	require.False(t, CheckStateExists(cpModule, layers.IPProtocolTCP, 8000, 80, "2001:db8::1", "2001:db8::2"))
}

// Test_FWState_Stash_CursorRestartsEachRound verifies that a cursor left at
// the end of an earlier round starts from the first record of the next.
func Test_FWState_Stash_CursorRestartsEachRound(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_stash_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	worker := testWorker(0)
	nextRound(worker)
	require.Equal(t, 2, stashEvents(cpModule, worker, v6Event(9000, 0, 0), v6Event(9001, 0, 0)))
	runHandler(cpModule, storage, worker, false)

	nextRound(worker)
	event := v6Event(9002, 0, 0)
	require.Equal(t, 1, stashEvents(cpModule, worker, event))
	result := runHandler(cpModule, storage, worker, false)

	require.Len(t, result.Output, 1)
	_, _, payload := syncPayload(result.Output[0])
	require.Equal(t, event.frame, payload)
}

// Test_FWState_Stash_WorkersAreIsolated verifies that a worker handles only
// its own slot, and that the slot headers of different workers sit on
// separate 64-byte aligned cache lines, each pointing at its own
// non-overlapping 64-byte aligned records array.
func Test_FWState_Stash_WorkersAreIsolated(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_stash_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	worker0 := testWorker(0)
	worker1 := testWorker(1)
	nextRound(worker0)
	nextRound(worker1)
	event := v6Event(9100, 0, 0)
	require.Equal(t, 1, stashEvents(cpModule, worker1, event))

	idle := runHandler(cpModule, storage, worker0, false)
	busy := runHandler(cpModule, storage, worker1, false)

	require.Empty(t, idle.Output)
	require.Len(t, busy.Output, 1)
	_, _, payload := syncPayload(busy.Output[0])
	require.Equal(t, event.frame, payload)

	for _, isIPv6 := range []bool{false, true} {
		slot0 := stashSlotAddr(cpModule, isIPv6, 0)
		slot1 := stashSlotAddr(cpModule, isIPv6, 1)
		require.Zero(t, slot0%64)
		require.Zero(t, slot1%64)
		require.Equal(t, uintptr(64), slot1-slot0, "headers sit on their own cache lines")

		records0 := stashRecordsAddr(cpModule, isIPv6, 0)
		records1 := stashRecordsAddr(cpModule, isIPv6, 1)
		require.Zero(t, records0%64)
		require.Zero(t, records1%64)
		require.NotEqual(t, records0, records1)
		recordsSize := uintptr(stashDefaultSize)
		if records0 < records1 {
			require.GreaterOrEqual(t, records1-records0, recordsSize)
		} else {
			require.GreaterOrEqual(t, records0-records1, recordsSize)
		}
	}
}

// Test_FWState_Stash_LayerInsertionKeepsRecords verifies that growing the
// map's layer chain leaves the stashed records of the round in place.
func Test_FWState_Stash_LayerInsertionKeepsRecords(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_stash_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	worker := testWorker(0)
	nextRound(worker)
	event := v6Event(9200, 0, 0)
	require.Equal(t, 1, stashEvents(cpModule, worker, event))

	InsertNewLayer(cpModule)

	require.Equal(t, 1, stashSlotCount(cpModule, true, 0))
	result := runHandler(cpModule, storage, worker, false)
	require.Len(t, result.Output, 1)
	_, _, payload := syncPayload(result.Output[0])
	require.Equal(t, event.frame, payload)
}

// Test_FWState_StashEmission_AllocationFailureKeepsState verifies that when
// no mbuf can be allocated for a batch, the records are still decided and
// applied, nothing is emitted, and no mbuf stays taken from the pool.
func Test_FWState_StashEmission_AllocationFailureKeepsState(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_stash_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	worker := testWorker(0)
	nextRound(worker)
	require.Equal(t, 2, stashEvents(cpModule, worker, v6Event(9300, 0, 0), v6Event(9301, 0, 0)))

	outstanding := poolOutstanding(worker)
	limitPool(worker, 0)
	t.Cleanup(func() { unlimitPool(worker) })
	result := runHandler(cpModule, storage, worker, false)

	require.Empty(t, result.Output)
	require.Empty(t, result.Drop)
	require.Equal(t, outstanding, poolOutstanding(worker), "a failed batch must not hold an mbuf")
	require.Equal(t, recordApplied, stashRecordStatus(cpModule, true, 0, 0))
	require.Equal(t, recordApplied, stashRecordStatus(cpModule, true, 0, 1))
	require.Equal(t, uint64(2), moduleCounter(cpModule, storage, "fwstate_sync_v6_inserted"))
	require.Zero(t, moduleCounter(cpModule, storage, "fwstate_internal_forwarded"))
	require.Equal(t, uint64(1), moduleCounter(cpModule, storage, "fwstate_sync_alloc_failed"))
	require.True(t, CheckStateExists(cpModule, layers.IPProtocolTCP, 9300, 80, "2001:db8::1", "2001:db8::2"))
	require.True(t, CheckStateExists(cpModule, layers.IPProtocolTCP, 9301, 80, "2001:db8::1", "2001:db8::2"))
}

// Test_FWState_StashEmission_CloneFailureKeepsMulticastCopy verifies that
// when the unicast clone cannot be allocated, the multicast packet is still
// emitted with every applied frame, the state is kept, and the only mbuf
// taken from the pool is the emitted one.
func Test_FWState_StashEmission_CloneFailureKeepsMulticastCopy(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_stash_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)
	SetSyncUnicastDestination(cpModule)

	worker := testWorker(0)
	nextRound(worker)
	events := []localEvent{v6Event(9400, 0, 0), v4Event(9401)}
	require.Equal(t, 2, stashEvents(cpModule, worker, events...))

	outstanding := poolOutstanding(worker)
	limitPool(worker, 1)
	t.Cleanup(func() { unlimitPool(worker) })
	result := runHandler(cpModule, storage, worker, false)

	require.Len(t, result.Output, 1, "only the multicast copy survives")
	destination, port, payload := syncPayload(result.Output[0])
	require.Equal(t, "ff02::1", destination.String())
	require.Equal(t, uint16(9999), port)
	require.Equal(t, append(append([]byte(nil), events[1].frame...), events[0].frame...), payload)
	require.Equal(t, outstanding+1, poolOutstanding(worker), "only the emitted packet holds an mbuf")
	require.Equal(t, recordApplied, stashRecordStatus(cpModule, true, 0, 0))
	require.Equal(t, recordApplied, stashRecordStatus(cpModule, false, 0, 0))
	require.Equal(t, uint64(1), moduleCounter(cpModule, storage, "fwstate_sync_v6_inserted"))
	require.Equal(t, uint64(1), moduleCounter(cpModule, storage, "fwstate_sync_v4_inserted"))
	require.Equal(t, uint64(1), moduleCounter(cpModule, storage, "fwstate_internal_forwarded"))
	require.Equal(t, uint64(1), moduleCounter(cpModule, storage, "fwstate_sync_alloc_failed"),
		"the failed unicast clone is an mbuf allocation failure")
	require.True(t, CheckStateExists(cpModule, layers.IPProtocolTCP, 9400, 80, "2001:db8::1", "2001:db8::2"))
}

// Test_FWState_StashEmission_BatchLargerThanOneMbuf verifies that when the
// sync MTU allows more frames than one mbuf holds, every applied frame is
// still emitted, in record order, across several packets that each fit an
// mbuf.
func Test_FWState_StashEmission_BatchLargerThanOneMbuf(t *testing.T) {
	const recordCount = 300

	memCtx := testutils.NewMemoryContext("fwstate_stash_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfigWithStashSize(memCtx, 512*uint64(syncRecordSize))
	defer fwstateCounterStorageFree(storage)
	SetSyncMTU(cpModule, 65535)

	worker := testWorker(0)
	nextRound(worker)
	var want []byte
	for idx := range recordCount {
		event := v6Event(uint16(10000+idx), 0, 0)
		require.Equal(t, 1, stashEvents(cpModule, worker, event))
		want = append(want, event.frame...)
	}

	result := runHandler(cpModule, storage, worker, false)

	require.Greater(t, len(result.Output), 1, "one mbuf cannot hold every frame")
	var got []byte
	for _, raw := range result.Output {
		_, _, payload := syncPayload(raw)
		require.NotEmpty(t, payload)
		got = append(got, payload...)
	}
	require.Equal(t, want, got)
	require.Equal(t, uint64(recordCount), moduleCounter(cpModule, storage, "fwstate_sync_v6_inserted"))
}

// Test_FWState_Stash_ContextsOfOneConfigShareReadPosition verifies that two
// execution contexts of one config on the same worker, as one config
// placed at two points of a round has, emit each record once between them
// and each pick up only what the other has not read.
func Test_FWState_Stash_ContextsOfOneConfigShareReadPosition(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_stash_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule, storage := fwstateModuleConfig(memCtx)
	defer fwstateCounterStorageFree(storage)

	worker := testWorker(0)
	nextRound(worker)
	require.Equal(t, 2, stashEvents(cpModule, worker, v6Event(9500, 0, 0), v6Event(9501, 0, 0)))

	first := runHandlerInContext(cpModule, storage, worker, 0, false)
	second := runHandlerInContext(cpModule, storage, worker, 1, false)
	require.Len(t, first.Output, 1)
	_, _, payload := syncPayload(first.Output[0])
	require.Len(t, payload, 2*frameSize)
	require.Empty(t, second.Output, "the other context must not re-send read records")

	event := v6Event(9502, 0, 0)
	require.Equal(t, 1, stashEvents(cpModule, worker, event))
	second = runHandlerInContext(cpModule, storage, worker, 1, false)
	first = runHandlerInContext(cpModule, storage, worker, 0, false)
	require.Len(t, second.Output, 1)
	_, _, payload = syncPayload(second.Output[0])
	require.Equal(t, event.frame, payload)
	require.Empty(t, first.Output)
	require.Equal(t, uint64(2), moduleCounter(cpModule, storage, "fwstate_internal_forwarded"))
}
