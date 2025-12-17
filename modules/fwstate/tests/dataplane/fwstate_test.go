package fwstate_test

import (
	"net"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/testutils"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
)

// createSyncPacket creates a firewall state sync packet
// with VLAN + IPv6 + UDP + sync frame structure
func createSyncPacket(t *testing.T, isExternal bool, proto uint8) gopacket.Packet {
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
	if isExternal {
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
	// Use unicast addresses for the flow being synced
	dstIP6 := net.ParseIP("2001:db8::2").To16()
	srcIP6 := net.ParseIP("2001:db8::1").To16()
	syncFrame := createSyncFrame(proto, 6, 12345, 9999, dstIP6, srcIP6)

	payload := gopacket.Payload(syncFrame)

	return xpacket.LayersToPacket(t, &eth, &vlan, &ip6, &udp, &payload)
}

func TestFWStateInternalPacket(t *testing.T) {
	// Create internal sync packet (should be forwarded)
	pkt := createSyncPacket(t, false, 6) // TCP
	t.Log("Internal sync packet:", pkt)

	memCtx := testutils.NewMemoryContext("fwstate_test", 64<<20)
	defer memCtx.Free()
	m := fwstateModuleConfig(memCtx)
	result := xerror.Unwrap(fwstateHandlePackets(m, pkt))

	// Internal packets should be in output
	require.NotEmpty(t, result.Output, "Internal packet should be forwarded")
	require.Empty(t, result.Drop, "Internal packet should not be dropped")
}

func TestFWStateExternalPacket(t *testing.T) {
	// Create external sync packet (should be dropped)
	pkt := createSyncPacket(t, true, 17) // UDP
	t.Log("External sync packet:", pkt)

	memCtx := testutils.NewMemoryContext("fwstate_test", 64<<20)
	defer memCtx.Free()
	m := fwstateModuleConfig(memCtx)
	result := xerror.Unwrap(fwstateHandlePackets(m, pkt))

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

	memCtx := testutils.NewMemoryContext("fwstate_test", 64<<20)
	defer memCtx.Free()
	m := fwstateModuleConfig(memCtx)
	result := xerror.Unwrap(fwstateHandlePackets(m, pkt))

	// Non-sync packets should pass through
	require.NotEmpty(t, result.Output, "Non-sync packet should pass through")
	require.Empty(t, result.Drop, "Non-sync packet should not be dropped")
}

// Test that sync packets actually create state entries
func TestFWStateStateCreation(t *testing.T) {
	// Create internal sync packet
	pkt := createSyncPacket(t, false, 6) // TCP
	t.Log("Internal sync packet:", pkt)

	memCtx := testutils.NewMemoryContext("fwstate_test", 64<<20)
	defer memCtx.Free()
	m := fwstateModuleConfig(memCtx)
	result := xerror.Unwrap(fwstateHandlePackets(m, pkt))

	// Verify packet was processed
	require.NotEmpty(t, result.Output, "Internal packet should be forwarded")

	// Check that state was created
	// For IPv6: src=2001:db8::1, dst=2001:db8::2, proto=TCP, src_port=12345, dst_port=9999
	stateExists := CheckStateExists(&m.cfg, true, 6, 12345, 9999, "2001:db8::1", "2001:db8::2")
	require.True(t, stateExists, "State should exist after processing sync packet")
}
