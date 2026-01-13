package fwstate_test

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
	syncFrame := createSyncFrame(proto, 6, cfg.srcPort, cfg.dstPort, dstIP6, srcIP6)

	payload := gopacket.Payload(syncFrame)

	return xpacket.LayersToPacket(t, &eth, &vlan, &ip6, &udp, &payload)
}

func TestFWStateInternalPacket(t *testing.T) {
	// Create internal sync packet (should be forwarded)
	pkt := createSyncPacket(t, layers.IPProtocolTCP)
	t.Log("Internal sync packet:", pkt)

	memCtx := testutils.NewMemoryContext("fwstate_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule := fwstateModuleConfig(memCtx)
	result := xerror.Unwrap(fwstateHandlePackets(cpModule, pkt))

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
	cpModule := fwstateModuleConfig(memCtx)
	result := xerror.Unwrap(fwstateHandlePackets(cpModule, pkt))

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
	cpModule := fwstateModuleConfig(memCtx)
	result := xerror.Unwrap(fwstateHandlePackets(cpModule, pkt))

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
	cpModule := fwstateModuleConfig(memCtx)
	result := xerror.Unwrap(fwstateHandlePackets(cpModule, pkt))

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
	cpModule := fwstateModuleConfig(memCtx)

	// Create initial state in the first layer
	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	result1 := xerror.Unwrap(fwstateHandlePackets(cpModule, pkt1))
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
	cpModule := fwstateModuleConfig(memCtx)

	// Create initial state with specific deadline
	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	result1 := xerror.Unwrap(fwstateHandlePackets(cpModule, pkt1))
	require.NotEmpty(t, result1.Output, "First packet should be forwarded")

	// Get initial deadline
	oldDeadline := GetStateDeadline(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.Greater(t, oldDeadline, uint64(0), "Initial state should have deadline")

	// Insert new layer
	InsertNewLayer(cpModule)

	// Add same state to new layer (should override old one)
	pkt2 := createSyncPacket(t, layers.IPProtocolTCP)
	result2 := xerror.Unwrap(fwstateHandlePackets(cpModule, pkt2))
	require.NotEmpty(t, result2.Output, "Second packet should be forwarded")

	// New deadline should be different (newer)
	newDeadline := GetStateDeadline(cpModule, layers.IPProtocolTCP, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.Greater(t, newDeadline, oldDeadline, "New state should have newer deadline")
}

// Test trim functionality: stale layers should be removed
func TestFWStateTrimStaleLayers(t *testing.T) {
	memCtx := testutils.NewMemoryContext("fwstate_trim_test", datasize.MB*64)
	defer memCtx.Free()
	cpModule := fwstateModuleConfig(memCtx)

	// Create state in first layer with short TTL
	pkt1 := createSyncPacket(t, layers.IPProtocolTCP)
	result1 := xerror.Unwrap(fwstateHandlePackets(cpModule, pkt1))
	require.NotEmpty(t, result1.Output)

	// Insert new layer
	InsertNewLayer(cpModule)

	// Add different state to new layer
	pkt2 := createSyncPacket(t, layers.IPProtocolUDP, WithPorts(54321, 8888), WithAddrs("2001:db8::3", "2001:db8::4"))
	result2 := xerror.Unwrap(fwstateHandlePackets(cpModule, pkt2))
	require.NotEmpty(t, result2.Output)

	// Check layer count before trim
	_, layerCountBefore := GetLayerCount(cpModule)
	require.Equal(t, uint32(2), layerCountBefore, "Should have 2 layers before trim")

	// Simulate time passing (beyond TTL of old layer)
	futureTime := GetCurrentTime() + 200e9 // 200 seconds in the future

	// Trim stale layers
	TrimStaleLayers(cpModule, futureTime)

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
