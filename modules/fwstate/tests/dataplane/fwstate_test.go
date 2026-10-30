package fwstate_test

import (
	"net"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
)

// SyncPacketOption customizes the state carried by a synchronization packet.
type SyncPacketOption func(*syncPacketConfig)

type syncPacketConfig struct {
	srcPort   uint16
	dstPort   uint16
	srcAddr   string
	dstAddr   string
	flags     uint8
	fib       uint8
	outerAddr string
	outerPort uint16
	outerMAC  string
}

// WithOuter overrides the packet's outer wire destination: IPv6 address,
// UDP port and Ethernet destination MAC.
func WithOuter(addr string, port uint16, mac string) SyncPacketOption {
	return func(c *syncPacketConfig) {
		c.outerAddr = addr
		c.outerPort = port
		c.outerMAC = mac
	}
}

// WithPorts sets custom source and destination ports.
func WithPorts(srcPort, dstPort uint16) SyncPacketOption {
	return func(c *syncPacketConfig) {
		c.srcPort = srcPort
		c.dstPort = dstPort
	}
}

// WithAddrs sets custom source and destination addresses.
func WithAddrs(srcAddr, dstAddr string) SyncPacketOption {
	return func(c *syncPacketConfig) {
		c.srcAddr = srcAddr
		c.dstAddr = dstAddr
	}
}

// WithFlags sets raw TCP flags byte on the embedded sync frame.
func WithFlags(flags uint8) SyncPacketOption {
	return func(c *syncPacketConfig) {
		c.flags = flags
	}
}

// WithFib sets fib (direction) on the embedded sync frame: 0=forward,
// 1=backward
func WithFib(fib uint8) SyncPacketOption {
	return func(c *syncPacketConfig) {
		c.fib = fib
	}
}

// createSyncPacket wraps one state frame in VLAN, IPv6 and UDP headers.
func createSyncPacket(t *testing.T, proto layers.IPProtocol, opts ...SyncPacketOption) gopacket.Packet {
	cfg := syncPacketConfig{
		srcPort:   12345,
		dstPort:   9999,
		srcAddr:   "2001:db8::1",
		dstAddr:   "2001:db8::2",
		outerAddr: "ff02::1",
		outerPort: 9999,
		outerMAC:  "33:33:00:00:00:01",
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	dstMAC := xerror.Unwrap(net.ParseMAC(cfg.outerMAC))
	outerDstIP := net.ParseIP(cfg.outerAddr)
	outerDstPort := layers.UDPPort(cfg.outerPort)

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

	cpModule := newFWStateFixture(t, 0)
	result := xerror.Unwrap(fwstateHandleFlaggedPackets(cpModule, pkt))

	require.Equal(t, [][]byte{pkt.Data()}, result.Output)
	require.Empty(t, result.Drop)
	require.False(t,
		CheckStateExists(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2"),
		"a local emission must not be applied again",
	)
	require.Equal(t, uint64(1), moduleCounter(cpModule, "fwstate_passthrough"))
	require.Zero(t, moduleCounter(cpModule, "fwstate_sync"))
}

// Test_FWStateLocalEvent_UnicastOnly verifies that a stashed event is still
// applied and emitted when multicast reception is disabled.
func Test_FWStateLocalEvent_UnicastOnly(t *testing.T) {
	pkt := createSyncPacket(t, layers.IPProtocolTCP)

	cpModule := newFWStateFixture(t, 0)
	ClearSyncDestination(cpModule)
	SetSyncUnicastDestination(cpModule)

	result := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, pkt))
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

	cpModule := newFWStateFixture(t, 0)
	result := xerror.Unwrap(fwstateHandleWirePackets(cpModule, pkt))

	require.Empty(t, result.Output, "wire packet must not be emitted")
	require.NotEmpty(t, result.Drop, "wire packet must be consumed")
	require.True(t, GetStateValue(cpModule, layers.IPProtocolUDP, 12345, 9999, "2001:db8::1", "2001:db8::2").External)
}

// Test_FWStateModule_UnconfiguredSync_AppliesLocalEventWithoutEmission verifies
// that absent endpoints allow state updates without allocation or emission.
func Test_FWStateModule_UnconfiguredSync_AppliesLocalEventWithoutEmission(t *testing.T) {
	pkt := createSyncPacket(t, layers.IPProtocolUDP)

	cpModule := newFWStateFixture(t, 0)
	ClearSyncDestination(cpModule)
	limitPool(cpModule.Worker(0), 0)
	defer unlimitPool(cpModule.Worker(0))

	outstanding := poolOutstanding(cpModule.Worker(0))
	result := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, pkt))

	require.Equal(t, outstanding, poolOutstanding(cpModule.Worker(0)),
		"no endpoint means no mbuf is allocated",
	)
	require.Empty(t, result.Output, "no endpoint means no emission")
	require.Empty(t, result.Drop, "no packet exists to drop")
	require.True(t,
		CheckStateExists(cpModule, layers.IPProtocolUDP, 12345, 9999, "2001:db8::1", "2001:db8::2"),
		"the event must still be applied",
	)
	require.Zero(t, moduleCounter(cpModule, "fwstate_internal_forwarded"))
	require.Zero(t, moduleCounter(cpModule, "fwstate_sync_alloc_failed"))
	require.False(t, GetStateValue(cpModule, layers.IPProtocolUDP, 12345, 9999, "2001:db8::1", "2001:db8::2").External)
}

// Test_FWState_WirePacketWithoutListener verifies that a config without
// listeners leaves matching wire packets in ordinary processing, even while
// its emission endpoint names the same destination.
func Test_FWState_WirePacketWithoutListener(t *testing.T) {
	pkt := createSyncPacket(t, layers.IPProtocolUDP)

	cpModule := newFWStateFixture(t, 0)
	cpModule.Sync.Listeners = nil
	result := xerror.Unwrap(fwstateHandleWirePackets(cpModule, pkt))

	require.Len(t, result.Output, 1, "wire receive is disabled without a listener")
	require.Empty(t, result.Drop)
}

// Test_FWState_WirePacketUnicastListener verifies that a unicast listener
// claims point-to-point sync carried by a packet with a unicast Ethernet
// destination.
func Test_FWState_WirePacketUnicastListener(t *testing.T) {
	pkt := createSyncPacket(t, layers.IPProtocolUDP,
		WithOuter("2001:db8::5", 9998, "02:00:00:00:00:99"),
	)

	cpModule := newFWStateFixture(t, 0)
	SetSyncUnicastListener(cpModule)
	result := xerror.Unwrap(fwstateHandleWirePackets(cpModule, pkt))

	require.Empty(t, result.Output, "received sync packets are consumed")
	require.Len(t, result.Drop, 1)
	require.True(t,
		CheckStateExists(cpModule, layers.IPProtocolUDP, 12345, 9999, "2001:db8::1", "2001:db8::2"),
		"unicast-listened sync must be applied",
	)
	require.True(t, GetStateValue(cpModule, layers.IPProtocolUDP, 12345, 9999, "2001:db8::1", "2001:db8::2").External)
	require.Equal(t, uint64(1), moduleCounter(cpModule, "fwstate_sync"))
}

// Test_FWState_NonSyncPacket verifies that ordinary packets pass through
// unchanged.
func Test_FWState_NonSyncPacket(t *testing.T) {
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

	cpModule := newFWStateFixture(t, 0)
	result := xerror.Unwrap(fwstateHandleWirePackets(cpModule, pkt))

	require.NotEmpty(t, result.Output, "Non-sync packet should pass through")
	require.Empty(t, result.Drop, "Non-sync packet should not be dropped")
}

// Test_FWState_LayerInsertionOldStateVisible verifies that older states remain
// visible after a layer is inserted.
func Test_FWState_LayerInsertionOldStateVisible(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	result1 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, pkt1))
	require.NotEmpty(t, result1.Output, "First packet should be forwarded")

	stateExists := CheckStateExists(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, stateExists, "Initial state should exist")

	InsertNewLayer(cpModule)

	stateStillExists := CheckStateExists(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, stateStillExists, "Old state should be visible after layer insertion")
}

// Test_FWState_LayerInsertionNewStateOverridesOld verifies that a state updated
// in a new layer gets a newer deadline.
func Test_FWState_LayerInsertionNewStateOverridesOld(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	result1 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, pkt1))
	require.NotEmpty(t, result1.Output, "First packet should be forwarded")

	oldDeadline := GetStateDeadline(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.Greater(t, oldDeadline, uint64(0), "Initial state should have deadline")

	InsertNewLayer(cpModule)

	// Add same state to new layer (should override old one)
	pkt2 := createSyncPacket(t, layers.IPProtocolTCP)
	result2 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, pkt2))
	require.NotEmpty(t, result2.Output, "Second packet should be forwarded")

	// New deadline should be different (newer)
	newDeadline := GetStateDeadline(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.Greater(t, newDeadline, oldDeadline, "New state should have newer deadline")
}

// Test_FWState_TrimStaleLayers verifies that reclaiming expired tail layers
// removes only their states.
func Test_FWState_TrimStaleLayers(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	result1 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, pkt1))
	require.NotEmpty(t, result1.Output)

	InsertNewLayer(cpModule)

	pkt2 := createSyncPacket(t, layers.IPProtocolUDP, WithPorts(54321, 8888), WithAddrs("2001:db8::3", "2001:db8::4"))
	result2 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, pkt2))
	require.NotEmpty(t, result2.Output)

	_, layerCountBefore := GetLayerCount(cpModule)
	require.Equal(t, uint32(2), layerCountBefore, "Should have 2 layers before trim")

	// Simulate time passing (beyond TTL of old layer)
	futureTime := uint64(cpModule.Fixture.Harness.CurrentTime().UnixNano()) + 200e9 // 200 seconds in the future

	require.NoError(t, TrimStaleLayers(cpModule, futureTime))

	_, layerCountAfter := GetLayerCount(cpModule)
	require.Equal(t, uint32(1), layerCountAfter, "Should have 1 layer after trim")

	newStateExists := CheckStateExists(cpModule, layers.IPProtocolUDP, 54321, 8888, "2001:db8::3", "2001:db8::4")
	require.True(t, newStateExists, "New state should still exist after trim")

	// Old state should not exist (layer was trimmed)
	oldStateExists := CheckStateExists(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.False(t, oldStateExists, "Old state should not exist after trim")
}

// Test_FWState_UpdateAccumulatesFlags verifies that successive local state
// updates accumulate TCP flags.
func Test_FWState_UpdateAccumulatesFlags(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	const synBit = 0x02 // FWSTATE_SYN, src nibble
	const ackBit = 0x08 // FWSTATE_ACK, src nibble

	pktSyn := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(synBit))
	_, err := fwstateHandleLocalEvents(cpModule, pktSyn)
	require.NoError(t, err)

	snap1 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap1.Found, "state must exist after first sync")
	require.Equal(t, uint32(synBit), snap1.Flags, "after first sync only SYN must be set")

	pktAck := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(ackBit))
	_, err = fwstateHandleLocalEvents(cpModule, pktAck)
	require.NoError(t, err)

	snap2 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap2.Found, "state must still exist after second sync")
	require.Equalf(t, uint32(synBit|ackBit), snap2.Flags,
		"flags must accumulate via OR across updates in the same layer (got 0x%02x, want 0x%02x)",
		snap2.Flags, synBit|ackBit,
	)
}

// Test_FWState_UpdateAccumulatesPacketCounters verifies that both direction
// counters sum all local updates.
func Test_FWState_UpdateAccumulatesPacketCounters(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	const forwardCount = 3
	const backwardCount = 2

	for range forwardCount {
		pkt := createSyncPacket(t, layers.IPProtocolTCP, WithFib(0))
		_, err := fwstateHandleLocalEvents(cpModule, pkt)
		require.NoError(t, err)
	}
	for range backwardCount {
		pkt := createSyncPacket(t, layers.IPProtocolTCP, WithFib(1))
		_, err := fwstateHandleLocalEvents(cpModule, pkt)
		require.NoError(t, err)
	}

	snap := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap.Found, "state must exist after sync frames")

	require.Equalf(t, uint64(forwardCount), snap.PacketsForward,
		"packets_forward must accumulate (got %d, want %d)",
		snap.PacketsForward, forwardCount,
	)
	require.Equalf(t, uint64(backwardCount), snap.PacketsBackward,
		"packets_backward must accumulate (got %d, want %d)",
		snap.PacketsBackward, backwardCount,
	)
}

// Test_FWState_UpdatePreservesCreatedAt verifies that successive updates retain
// the original creation time.
func Test_FWState_UpdatePreservesCreatedAt(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	_, err := fwstateHandleLocalEvents(cpModule, pkt1)
	require.NoError(t, err)

	snap1 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap1.Found)
	createdAt := snap1.CreatedAt
	require.Greater(t, createdAt, uint64(0))

	pkt2 := createSyncPacket(t, layers.IPProtocolTCP)
	_, err = fwstateHandleLocalEvents(cpModule, pkt2)
	require.NoError(t, err)

	snap2 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap2.Found)

	require.Equal(t, createdAt, snap2.CreatedAt,
		"created_at must be preserved across updates",
	)
}

// Test_FWState_MergeFromStaleLayer verifies that promotion merges flags and
// direction counters from the old layer.
func Test_FWState_MergeFromStaleLayer(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	const synBit = 0x02
	const ackBit = 0x08

	// Populate stale layer with SYN + 1 forward packet.
	pkt1 := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(synBit), WithFib(0))
	_, err := fwstateHandleLocalEvents(cpModule, pkt1)
	require.NoError(t, err)

	// Push that layer down, allocate a new active layer.
	InsertNewLayer(cpModule)

	// Insert into the fresh active layer with ACK + 1 backward packet.
	pkt2 := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(ackBit), WithFib(1))
	_, err = fwstateHandleLocalEvents(cpModule, pkt2)
	require.NoError(t, err)

	snap := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap.Found, "merged state must be visible from active layer")
	require.Equalf(t, uint32(synBit|ackBit), snap.Flags,
		"flags must be merged across layers (got 0x%02x, want 0x%02x)",
		snap.Flags, synBit|ackBit,
	)
	require.Equalf(t, uint64(1), snap.PacketsForward,
		"packets_forward from stale layer must be carried over (got %d)", snap.PacketsForward,
	)
	require.Equalf(t, uint64(1), snap.PacketsBackward,
		"packets_backward from active layer must be summed in (got %d)", snap.PacketsBackward,
	)
}

// Test_FWState_SyncSuppression verifies that a suppressed refresh changes
// neither timestamps nor deadline.
func Test_FWState_SyncSuppression(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	// 60s window: any second frame arriving within the test's consecutive rounds
	// is well inside the window and must be suppressed.
	const suppressNs = uint64(60e9)
	SetSyncSuppressTimeout(cpModule, suppressNs)

	// First frame: entry does not exist yet, so it is applied and forwarded.
	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	res1 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, pkt1))
	require.NotEmpty(t, res1.Output, "first frame must be forwarded")
	require.Empty(t, res1.Drop, "first frame must not be dropped")

	snap1 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap1.Found, "state must exist after first frame")
	require.Greater(t, snap1.Deadline, uint64(0))

	// Second frame, same 5-tuple, arrives immediately: within the window, so the
	// state record and the wire are both left untouched.
	pkt2 := createSyncPacket(t, layers.IPProtocolTCP)
	res2 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, pkt2))
	require.Empty(t, res2.Output)
	require.Empty(t, res2.Drop)
	require.Equal(t, recordSuppressed, stashRecordStatus(cpModule, true, 0, 0))

	snap2 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap2.Found)
	require.Equal(t, snap1.UpdatedAt, snap2.UpdatedAt,
		"updated_at must not change when the frame is suppressed",
	)
	require.Equal(t, snap1.Deadline, snap2.Deadline,
		"deadline must not change when the frame is suppressed",
	)
}

// Test_FWState_SyncSuppressionDisabled verifies that disabling suppression
// permits every refresh.
func Test_FWState_SyncSuppressionDisabled(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)
	// suppress left at its default of 0 (disabled)

	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	res1 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, pkt1))
	require.NotEmpty(t, res1.Output, "first frame must be forwarded")
	snap1 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap1.Found)

	pkt2 := createSyncPacket(t, layers.IPProtocolTCP)
	xerror.Unwrap(fwstateHandleLocalEvents(cpModule, pkt2))
	snap2 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap2.Found)
	require.GreaterOrEqual(t, snap2.UpdatedAt, snap1.UpdatedAt,
		"updated_at must advance when the frame is applied",
	)
}

// Test_FWState_SyncSuppressionAllowsShorterTTL verifies that a shorter TTL
// shrinks the deadline inside the suppression window.
func Test_FWState_SyncSuppressionAllowsShorterTTL(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	// Established TTL much longer than the FIN TTL, so the FIN's new expiry lands
	// below the current deadline and must not be suppressed.
	SetSyncTCPTimeouts(cpModule, 120e9, 10e9)
	SetSyncSuppressTimeout(cpModule, 60e9)

	// Established frame (no flags): entry created on the long TTL.
	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	res1 := xerror.Unwrap(fwstateHandleLocalEvents(cpModule, pkt1))
	require.NotEmpty(t, res1.Output, "established frame must be forwarded")
	snap1 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap1.Found)

	// FIN frame immediately after: shorter TTL must be applied, not suppressed, so
	// the deadline shrinks toward the FIN timeout.
	const finBit = 0x01 // FWSTATE_FIN, src nibble
	pkt2 := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(finBit))
	xerror.Unwrap(fwstateHandleLocalEvents(cpModule, pkt2))

	snap2 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap2.Found)
	require.Less(t, snap2.Deadline, snap1.Deadline,
		"FIN (shorter TTL) must shrink the deadline instead of being suppressed",
	)
}

// Test_FWState_SyncSuppressionAppliesFlagChanges verifies that new flag bits
// are applied inside the suppression window.
func Test_FWState_SyncSuppressionAppliesFlagChanges(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	// The default SYN and SYN-ACK timeouts match, so both frames below pick the
	// same TTL.
	SetSyncSuppressTimeout(cpModule, 60e9)

	const synBit = 0x02 // FWSTATE_SYN, src nibble
	const ackBit = 0x08 // FWSTATE_ACK, src nibble

	pkt1 := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(synBit))
	xerror.Unwrap(fwstateHandleLocalEvents(cpModule, pkt1))
	snap1 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap1.Found)
	require.Equal(t, uint32(synBit), snap1.Flags)

	// ACK frame, same TTL, within the window: must not be suppressed because it
	// carries a new flag bit that the merge path must record.
	pkt2 := createSyncPacket(t, layers.IPProtocolTCP, WithFlags(ackBit))
	xerror.Unwrap(fwstateHandleLocalEvents(cpModule, pkt2))

	snap2 := GetStateValue(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, snap2.Found)
	require.Equalf(t, uint32(synBit|ackBit), snap2.Flags,
		"ACK must merge into flags even at an unchanged TTL within the window (got 0x%02x)",
		snap2.Flags,
	)
}

// Test_FWState_StashEmission_BatchesByDefaultMTU verifies that 30 applied
// records on one TX device leave in packets of 25 and 5 frames, each frame
// byte-identical to its record and in record order.
func Test_FWState_StashEmission_BatchesByDefaultMTU(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	worker := cpModule.Worker(0)
	nextRound(worker)
	var events []localEvent
	var want []byte
	for idx := range 30 {
		event := v6Event(uint16(1000 + idx))
		events = append(events, event)
		want = append(want, event.frame...)
	}
	require.Equal(t, len(events), stashEvents(cpModule, worker, events...))

	result := runConfig(cpModule, worker, false)

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
	require.Equal(t, uint64(30), moduleCounter(cpModule, "fwstate_sync_v6_inserted"))
	require.Equal(t, uint64(2), moduleCounter(cpModule, "fwstate_internal_forwarded"))
}

// Test_FWState_StashEmission_BatchesByCustomMTU verifies that a sync MTU
// holding two frames splits five records into packets of 2, 2 and 1.
func Test_FWState_StashEmission_BatchesByCustomMTU(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)
	SetSyncMTU(cpModule, uint16(40+8+2*frameSize))

	worker := cpModule.Worker(0)
	nextRound(worker)
	for idx := range 5 {
		require.Equal(t, 1, stashEvents(cpModule, worker, v6Event(uint16(2000+idx))))
	}

	result := runConfig(cpModule, worker, false)

	var frames []int
	for _, raw := range result.Output {
		_, _, payload := syncPayload(raw)
		frames = append(frames, len(payload)/frameSize)
	}
	require.Equal(t, []int{2, 2, 1}, frames)
}

// Test_FWState_StashEmission_BothEndpoints verifies that every batch is emitted
// once to the multicast and once to the unicast endpoint.
func Test_FWState_StashEmission_BothEndpoints(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)
	SetSyncUnicastDestination(cpModule)

	worker := cpModule.Worker(0)
	nextRound(worker)
	for idx := range 30 {
		require.Equal(t, 1, stashEvents(cpModule, worker, v6Event(uint16(4000+idx))))
	}

	result := runConfig(cpModule, worker, false)

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
	}, got,
	)
}

// Test_FWState_StashEmission_RandomSourcePortsAndNeutralEthernet verifies the
// per-copy emission invariants: the UDP source port is randomized into the
// ephemeral range so the flow hash spreads over equal-cost paths, the IPv6
// source names the endpoint, and the Ethernet destination stays zeroed for
// the route module downstream to resolve.
func Test_FWState_StashEmission_RandomSourcePortsAndNeutralEthernet(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)
	SetSyncUnicastDestination(cpModule)

	worker := cpModule.Worker(0)
	nextRound(worker)
	require.Equal(t, 1, stashEvents(cpModule, worker, v6Event(6000)))

	result := runConfig(cpModule, worker, false)
	require.Len(t, result.Output, 2)

	for _, raw := range result.Output {
		parsed := gopacket.NewPacket(raw, layers.LayerTypeEthernet, gopacket.Default)
		ethernet, ok := parsed.Layer(layers.LayerTypeEthernet).(*layers.Ethernet)
		require.True(t, ok)
		require.Equal(t, net.HardwareAddr{0, 0, 0, 0, 0, 0}, ethernet.DstMAC)
		ipv6, ok := parsed.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
		require.True(t, ok)
		require.Equal(t, net.ParseIP("fd00::1"), ipv6.SrcIP)
		udp, ok := parsed.Layer(layers.LayerTypeUDP).(*layers.UDP)
		require.True(t, ok)
		require.GreaterOrEqual(t, uint16(udp.SrcPort), uint16(1024))
	}
}

// Test_FWState_StashEmission_PacksBothFamilies verifies that both families
// share one emitted packet.
//
// IPv4 records precede IPv6 records, and each updates its own family table.
func Test_FWState_StashEmission_PacksBothFamilies(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	worker := cpModule.Worker(0)
	nextRound(worker)
	event6 := v6Event(5000)
	event4 := v4Event(5001)
	require.Equal(t, 2, stashEvents(cpModule, worker, event6, event4))

	result := runConfig(cpModule, worker, false)

	require.Len(t, result.Output, 1)
	_, _, payload := syncPayload(result.Output[0])
	require.Equal(t, append(append([]byte(nil), event4.frame...), event6.frame...), payload)
	require.Equal(t, uint64(1), moduleCounter(cpModule, "fwstate_sync_v4_inserted"))
	require.Equal(t, uint64(1), moduleCounter(cpModule, "fwstate_sync_v6_inserted"))
}

// Test_FWState_Stash_RepeatInvocationHandlesOnlyNewRecords verifies that
// same-round calls handle only newly appended records.
//
// Redirects and additional placements resume after the previously read records.
func Test_FWState_Stash_RepeatInvocationHandlesOnlyNewRecords(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	worker := cpModule.Worker(0)
	nextRound(worker)
	require.Equal(t, 2, stashEvents(cpModule, worker, v6Event(6000), v6Event(6001)))
	first := runConfig(cpModule, worker, false)

	event := v6Event(6002)
	require.Equal(t, 1, stashEvents(cpModule, worker, event))
	second := runConfig(cpModule, worker, false)
	third := runConfig(cpModule, worker, false)

	require.Len(t, first.Output, 1)
	_, _, firstPayload := syncPayload(first.Output[0])
	require.Len(t, firstPayload, 2*frameSize)
	require.Len(t, second.Output, 1)
	_, _, secondPayload := syncPayload(second.Output[0])
	require.Equal(t, event.frame, secondPayload)
	require.Empty(t, third.Output)
	require.Equal(t, uint64(3), moduleCounter(cpModule, "fwstate_sync_v6_inserted"))
}

// Test_FWState_Stash_SecondConfigEmitsWithoutDeciding verifies that a second
// config emits applied records without making their state decisions again.
//
// It passes the first emission through and emits to its own endpoint without
// repeating insertion or suppression.
func Test_FWState_Stash_SecondConfigEmitsWithoutDeciding(t *testing.T) {
	first := newFWStateFixture(t, 0)
	SetSyncSuppressTimeout(first, 60e9)
	second := fwstateSiblingModuleConfig(first, "second")

	worker := first.Worker(0)

	// Round one: two new records, both applied by the first config.
	nextRound(worker)
	refreshed := v6Event(7000)
	require.Equal(t, 2, stashEvents(first, worker, refreshed, v6Event(7001)))
	firstRound := runConfig(first, worker, false)
	require.Len(t, firstRound.Output, 1)
	upstream := gopacket.NewPacket(firstRound.Output[0], layers.LayerTypeEthernet, gopacket.Default)
	secondRound := runConfig(second, worker, true, upstream)

	require.Len(t, secondRound.Output, 2)
	require.Equal(t, firstRound.Output[0], secondRound.Output[0],
		"the upstream emission must pass through unchanged",
	)
	destination, port, payload := syncPayload(secondRound.Output[1])
	require.Equal(t, "2001:db8::3", destination.String())
	require.Equal(t, uint16(10000), port)
	_, _, upstreamPayload := syncPayload(firstRound.Output[0])
	require.Equal(t, upstreamPayload, payload)

	// Round two: a suppressible refresh and a new record.
	nextRound(worker)
	fresh := v6Event(7002)
	require.Equal(t, 2, stashEvents(first, worker, refreshed, fresh))
	firstRound = runConfig(first, worker, false)
	secondRound = runConfig(second, worker, false)

	require.Equal(t, recordSuppressed, stashRecordStatus(first, true, 0, 0))
	require.Equal(t, recordApplied, stashRecordStatus(first, true, 0, 1))
	require.Len(t, firstRound.Output, 1)
	_, _, payload = syncPayload(firstRound.Output[0])
	require.Equal(t, fresh.frame, payload)
	require.Len(t, secondRound.Output, 1)
	_, _, payload = syncPayload(secondRound.Output[0])
	require.Equal(t, fresh.frame, payload)

	require.Equal(t, uint64(3), moduleCounter(first, "fwstate_sync_v6_inserted"))
	require.Equal(t, uint64(1), moduleCounter(first, "fwstate_sync_v6_suppressed"))
	require.Zero(t, moduleCounter(second, "fwstate_sync_v6_inserted"))
	require.Zero(t, moduleCounter(second, "fwstate_sync_v6_suppressed"))
	require.Equal(t, uint64(1), moduleCounter(second, "fwstate_passthrough"))
}

// Test_FWState_Stash_NewRoundDropsUnprocessedRecords verifies that records left
// unprocessed when a round ends are never applied or emitted.
func Test_FWState_Stash_NewRoundDropsUnprocessedRecords(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	worker := cpModule.Worker(0)
	nextRound(worker)
	require.Equal(t, 1, stashEvents(cpModule, worker, v6Event(8000)))
	nextRound(worker)

	result := runConfig(cpModule, worker, false)

	require.Empty(t, result.Output)
	require.Zero(t, stashSlotCount(cpModule, true, 0))
	require.False(t, CheckStateExists(cpModule, layers.IPProtocolTCP, 8000, 80, "2001:db8::1", "2001:db8::2"))
}

// Test_FWState_Stash_CursorRestartsEachRound verifies that a cursor left at the
// end of an earlier round starts from the first record of the next.
func Test_FWState_Stash_CursorRestartsEachRound(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	worker := cpModule.Worker(0)
	nextRound(worker)
	require.Equal(t, 2, stashEvents(cpModule, worker, v6Event(9000), v6Event(9001)))
	runConfig(cpModule, worker, false)

	nextRound(worker)
	event := v6Event(9002)
	require.Equal(t, 1, stashEvents(cpModule, worker, event))
	result := runConfig(cpModule, worker, false)

	require.Len(t, result.Output, 1)
	_, _, payload := syncPayload(result.Output[0])
	require.Equal(t, event.frame, payload)
}

// Test_FWState_Stash_WorkersAreIsolated verifies that each worker consumes
// only its own records.
//
// Slot headers occupy separate 64-byte aligned cache lines; their record
// buffers are also 64-byte aligned and do not overlap.
func Test_FWState_Stash_WorkersAreIsolated(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	worker0 := cpModule.Worker(0)
	worker1 := cpModule.Worker(1)
	nextRound(worker0)
	nextRound(worker1)
	event := v6Event(9100)
	require.Equal(t, 1, stashEvents(cpModule, worker1, event))

	idle := runConfig(cpModule, worker0, false)
	busy := runConfig(cpModule, worker1, false)

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

// Test_FWState_Stash_LayerInsertionKeepsRecords verifies that growing the map's
// layer chain leaves the stashed records of the round in place.
func Test_FWState_Stash_LayerInsertionKeepsRecords(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	worker := cpModule.Worker(0)
	nextRound(worker)
	event := v6Event(9200)
	require.Equal(t, 1, stashEvents(cpModule, worker, event))

	InsertNewLayer(cpModule)

	require.Equal(t, 1, stashSlotCount(cpModule, true, 0))
	result := runConfig(cpModule, worker, false)
	require.Len(t, result.Output, 1)
	_, _, payload := syncPayload(result.Output[0])
	require.Equal(t, event.frame, payload)
}

// Test_FWState_StashEmission_AllocationFailureKeepsState verifies that state
// updates survive a failed packet allocation.
//
// Records are decided and applied without emission or retained pool objects.
func Test_FWState_StashEmission_AllocationFailureKeepsState(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	worker := cpModule.Worker(0)
	nextRound(worker)
	require.Equal(t, 2, stashEvents(cpModule, worker, v6Event(9300), v6Event(9301)))

	outstanding := poolOutstanding(worker)
	limitPool(worker, 0)
	t.Cleanup(func() { unlimitPool(worker) })
	result := runConfig(cpModule, worker, false)

	require.Empty(t, result.Output)
	require.Empty(t, result.Drop)
	require.Equal(t, outstanding, poolOutstanding(worker), "a failed batch must not hold an mbuf")
	require.Equal(t, recordApplied, stashRecordStatus(cpModule, true, 0, 0))
	require.Equal(t, recordApplied, stashRecordStatus(cpModule, true, 0, 1))
	require.Equal(t, uint64(2), moduleCounter(cpModule, "fwstate_sync_v6_inserted"))
	require.Zero(t, moduleCounter(cpModule, "fwstate_internal_forwarded"))
	require.Equal(t, uint64(1), moduleCounter(cpModule, "fwstate_sync_alloc_failed"))
	require.True(t, CheckStateExists(cpModule, layers.IPProtocolTCP, 9300, 80, "2001:db8::1", "2001:db8::2"))
	require.True(t, CheckStateExists(cpModule, layers.IPProtocolTCP, 9301, 80, "2001:db8::1", "2001:db8::2"))
}

// Test_FWState_StashEmission_CloneFailureKeepsMulticastCopy verifies that
// multicast emission survives failure to allocate its unicast clone.
//
// The emitted packet retains every applied frame and is the only live pool
// object; state updates remain applied.
func Test_FWState_StashEmission_CloneFailureKeepsMulticastCopy(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)
	SetSyncUnicastDestination(cpModule)

	worker := cpModule.Worker(0)
	nextRound(worker)
	events := []localEvent{v6Event(9400), v4Event(9401)}
	require.Equal(t, 2, stashEvents(cpModule, worker, events...))

	outstanding := poolOutstanding(worker)
	limitPool(worker, 1)
	t.Cleanup(func() { unlimitPool(worker) })
	result := runConfig(cpModule, worker, false)

	require.Len(t, result.Output, 1, "only the multicast copy survives")
	destination, port, payload := syncPayload(result.Output[0])
	require.Equal(t, "ff02::1", destination.String())
	require.Equal(t, uint16(9999), port)
	require.Equal(t, append(append([]byte(nil), events[1].frame...), events[0].frame...), payload)
	require.Equal(t, outstanding+1, result.OutstandingMbufs, "only the emitted packet holds an mbuf")
	require.Equal(t, outstanding, poolOutstanding(worker), "disposing the result returns the emitted mbuf")
	require.Equal(t, recordApplied, stashRecordStatus(cpModule, true, 0, 0))
	require.Equal(t, recordApplied, stashRecordStatus(cpModule, false, 0, 0))
	require.Equal(t, uint64(1), moduleCounter(cpModule, "fwstate_sync_v6_inserted"))
	require.Equal(t, uint64(1), moduleCounter(cpModule, "fwstate_sync_v4_inserted"))
	require.Equal(t, uint64(1), moduleCounter(cpModule, "fwstate_internal_forwarded"))
	require.Equal(t, uint64(1), moduleCounter(cpModule, "fwstate_sync_alloc_failed"),
		"the failed unicast clone is an mbuf allocation failure",
	)
	require.True(t, CheckStateExists(cpModule, layers.IPProtocolTCP, 9400, 80, "2001:db8::1", "2001:db8::2"))
}

// Test_FWState_StashEmission_BatchLargerThanOneMbuf verifies that a batch
// exceeding one mbuf preserves every frame.
//
// Even when the MTU permits a larger batch, emitted packets each fit an mbuf
// and retain the original record order.
func Test_FWState_StashEmission_BatchLargerThanOneMbuf(t *testing.T) {
	const recordCount = 300

	cpModule := newFWStateFixture(t, 512*uint64(syncRecordSize))
	SetSyncMTU(cpModule, 65535)

	worker := cpModule.Worker(0)
	nextRound(worker)
	var want []byte
	for idx := range recordCount {
		event := v6Event(uint16(10000 + idx))
		require.Equal(t, 1, stashEvents(cpModule, worker, event))
		want = append(want, event.frame...)
	}

	result := runConfig(cpModule, worker, false)

	require.Greater(t, len(result.Output), 1, "one mbuf cannot hold every frame")
	var got []byte
	for _, raw := range result.Output {
		_, _, payload := syncPayload(raw)
		require.NotEmpty(t, payload)
		got = append(got, payload...)
	}
	require.Equal(t, want, got)
	require.Equal(t, uint64(recordCount), moduleCounter(cpModule, "fwstate_sync_v6_inserted"))
}

// Test_FWState_Stash_ContextsOfOneConfigShareReadPosition verifies that
// placements of one config share progress on the same worker.
//
// Each record is emitted once across both placements, and either placement
// can consume records appended since their previous calls.
func Test_FWState_Stash_ContextsOfOneConfigShareReadPosition(t *testing.T) {
	cpModule := newFWStateFixture(t, 0)

	worker := cpModule.Worker(0)
	nextRound(worker)
	require.Equal(t, 2, stashEvents(cpModule, worker, v6Event(9500), v6Event(9501)))

	first := runConfigPlacement(cpModule, worker, 0, false)
	second := runConfigPlacement(cpModule, worker, 1, false)
	require.Len(t, first.Output, 1)
	_, _, payload := syncPayload(first.Output[0])
	require.Len(t, payload, 2*frameSize)
	require.Empty(t, second.Output, "the other context must not re-send read records")

	event := v6Event(9502)
	require.Equal(t, 1, stashEvents(cpModule, worker, event))
	second = runConfigPlacement(cpModule, worker, 1, false)
	first = runConfigPlacement(cpModule, worker, 0, false)
	require.Len(t, second.Output, 1)
	_, _, payload = syncPayload(second.Output[0])
	require.Equal(t, event.frame, payload)
	require.Empty(t, first.Output)
	require.Equal(t, uint64(2), moduleCounter(cpModule, "fwstate_internal_forwarded"))
}

// Test_FWState_StashEmission_RepeatedBatchingReturnsPoolObjects verifies that
// more than 512 calls reclaim every emitted mbuf and preserve exact batches.
func Test_FWState_StashEmission_RepeatedBatchingReturnsPoolObjects(t *testing.T) {
	config := newFWStateFixture(t, 0)
	worker := config.Worker(0)
	var events []localEvent
	var expected []byte
	for idx := range 30 {
		event := v6Event(uint16(12000 + idx))
		events = append(events, event)
		expected = append(expected, event.frame...)
	}
	for range 600 {
		nextRound(worker)
		require.Equal(t, len(events), stashEvents(config, worker, events...))
		result := runConfig(config, worker, false)
		require.Len(t, result.Output, 2)
		require.Equal(t, uint64(2), result.OutstandingMbufs)
		var actual []byte
		for idx, raw := range result.Output {
			_, _, payload := syncPayload(raw)
			actual = append(actual, payload...)
			require.Equal(t, uint16(2), result.OutputFlags[idx])
		}
		require.Equal(t, expected, actual)
		require.Zero(t, poolOutstanding(worker))
	}
	require.Equal(t, uint64(18000), moduleCounter(config, "fwstate_sync_v6_inserted"))
	require.Equal(t, uint64(1200), moduleCounter(config, "fwstate_internal_forwarded"))
}
