package functional

import (
	"net"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// TestDecap_BasicFunctionality tests basic decap module functionality
func TestDecap(t *testing.T) {
	fw := globalFramework
	require.NotNil(t, fw, "Global framework should be initialized")

	t.Run("Configure_Decap_Module", func(t *testing.T) {
		// Decap-specific configuration
		commands := []string{
			"/mnt/target/release/yanet-cli-decap prefix-add --cfg decap0 --instances 0 -p 4.5.6.7/32",
			"/mnt/target/release/yanet-cli-decap prefix-add --cfg decap0 --instances 0 -p 1:2:3:4::abcd/128",

			"/mnt/target/release/yanet-cli-function update --name=test --chains c0:3=forward:forward0,decap:decap0,route:route0 --instance=0",
			"/mnt/target/release/yanet-cli-pipeline update --name=test --functions test --instance=0",
		}

		_, err := fw.CLI.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure decap module")
	})

	t.Run("Test_IPIP6_Decapsulation", func(t *testing.T) {
		// Test IPv6-in-IPv4 decapsulation (IPIP6)
		// Based on unit test TestDecap_IPIP6
		packet := createIPIP6Packet(
			net.ParseIP("4.5.6.7"), // outer IPv4 dst (matches our prefix)
			net.ParseIP("::1"),     // inner IPv6 src
			net.ParseIP("::2"),     // inner IPv6 dst
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		require.True(t, inputPacket.IsTunneled, "Input packet should be tunneled")
		require.False(t, outputPacket.IsTunneled, "Output packet should not be tunneled")

		// If we got output, verify it's decapsulated
		assert.Equal(t, "::1", outputPacket.SrcIP.String(), "Decapsulated source IP should match inner packet")
		assert.Equal(t, "::2", outputPacket.DstIP.String(), "Decapsulated destination IP should match inner packet")
	})

	t.Run("Test_IP6IP_Decapsulation", func(t *testing.T) {
		// Test IPv4-in-IPv6 decapsulation (IP6IP)
		// Based on unit test TestDecap_IP6IP
		packet := createIP6IPPacket(
			net.ParseIP("1:2:3:4::abcd"), // outer IPv6 dst (matches our prefix)
			net.ParseIP("1.1.0.0"),       // inner IPv4 src
			net.ParseIP("2.2.0.0"),       // inner IPv4 dst
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send and capture IP6IP packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		require.True(t, inputPacket.IsTunneled, "Input packet should be tunneled")
		require.False(t, outputPacket.IsTunneled, "Output packet should not be tunneled")

		assert.Equal(t, "1.1.0.0", outputPacket.SrcIP.String(), "Decapsulated source IP should match inner packet")
		assert.Equal(t, "2.2.0.0", outputPacket.DstIP.String(), "Decapsulated destination IP should match inner packet")
	})

	t.Run("Test_Non_Matching_Prefix", func(t *testing.T) {
		// Test packet with non-matching prefix should not be decapsulated but forwarded
		packet := createIPIP6Packet(
			net.ParseIP("4.5.6.8"), // outer IPv4 dst (does NOT match our prefix 4.5.6.7/32)
			net.ParseIP("::3"),     // inner IPv6 src
			net.ParseIP("::4"),     // inner IPv6 dst
		)

		// Send packet and wait for response (should be forwarded, not decapsulated)
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Packet should be forwarded")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be present (forwarded)")
		require.True(t, inputPacket.IsTunneled, "Input packet should be tunneled")
		require.True(t, outputPacket.IsTunneled, "Output packet should still be tunneled (not decapsulated)")

		// Verify that the packet was not decapsulated - outer and inner IPs should be the same
		assert.Equal(t, inputPacket.DstIP.String(), outputPacket.DstIP.String(), "Outer destination IP should be unchanged")
		assert.Equal(t, inputPacket.TunnelType, outputPacket.TunnelType, "Tunnel type should be unchanged")
	})

	t.Run("Test_IPIP_Decapsulation", func(t *testing.T) {
		// Test IPv4-in-IPv4 decapsulation (IPIP)
		// Based on unit test TestDecap_IPIP
		packet := createIPIPPacket(
			net.ParseIP("4.5.6.7"), // outer IPv4 dst (matches our prefix)
			net.ParseIP("1.1.0.0"), // inner IPv4 src
			net.ParseIP("2.2.0.0"), // inner IPv4 dst
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send and capture IPIP packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		require.True(t, inputPacket.IsTunneled, "Input packet should be tunneled")
		require.False(t, outputPacket.IsTunneled, "Output packet should not be tunneled")

		// Verify it's decapsulated
		assert.Equal(t, "1.1.0.0", outputPacket.SrcIP.String(), "Decapsulated source IP should match inner packet")
		assert.Equal(t, "2.2.0.0", outputPacket.DstIP.String(), "Decapsulated destination IP should match inner packet")
	})

	t.Run("Test_IP6IP6_Decapsulation", func(t *testing.T) {
		// Test IPv6-in-IPv6 decapsulation (IP6IP6)
		// Based on unit test TestDecap_IP6IP6
		packet := createIP6IP6Packet(
			net.ParseIP("1:2:3:4::abcd"), // outer IPv6 dst (matches our prefix)
			net.ParseIP("::1"),           // inner IPv6 src
			net.ParseIP("::2"),           // inner IPv6 dst
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send and capture IP6IP6 packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		require.True(t, inputPacket.IsTunneled, "Input packet should be tunneled")
		require.False(t, outputPacket.IsTunneled, "Output packet should not be tunneled")

		// Verify it's decapsulated
		assert.Equal(t, "::1", outputPacket.SrcIP.String(), "Decapsulated source IP should match inner packet")
		assert.Equal(t, "::2", outputPacket.DstIP.String(), "Decapsulated destination IP should match inner packet")
	})

	t.Run("Test_GRE_IPv6_to_IPv4_Decapsulation", func(t *testing.T) {
		// Test GRE IPv6-to-IPv4 decapsulation
		// Based on unit test TestDecap_GRE
		packet := createGRESimplePacket(
			net.ParseIP("1:2:3:4::abcd"), // outer IPv6 dst (matches our prefix)
			net.ParseIP("1.1.0.0"),       // inner IPv4 src
			net.ParseIP("2.2.0.0"),       // inner IPv4 dst
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send and capture GRE packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		require.True(t, inputPacket.IsTunneled, "Input packet should be tunneled")
		require.False(t, outputPacket.IsTunneled, "Output packet should not be tunneled")

		// Verify it's decapsulated
		assert.Equal(t, "1.1.0.0", outputPacket.SrcIP.String(), "Decapsulated source IP should match inner packet")
		assert.Equal(t, "2.2.0.0", outputPacket.DstIP.String(), "Decapsulated destination IP should match inner packet")
	})

	t.Run("Test_GRE_With_Options", func(t *testing.T) {
		// Test GRE with checksum, key, and sequence options
		packet := createGREPacket(
			net.ParseIP("1:2:3:4::abcd"), // outer IPv6 dst (matches our prefix)
			net.ParseIP("1.2.3.4"),       // inner IPv4 src
			net.ParseIP("2.3.4.5"),       // inner IPv4 dst
			true, true, true,             // with checksum, key, and sequence
		)

		// Send packet and wait for response
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send and capture GRE packet with options")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		require.True(t, inputPacket.IsTunneled, "Input packet should be tunneled")
		require.False(t, outputPacket.IsTunneled, "Output packet should not be tunneled")

		// Verify it's decapsulated
		assert.Equal(t, "1.2.3.4", outputPacket.SrcIP.String(), "Decapsulated source IP should match inner packet")
		assert.Equal(t, "2.3.4.5", outputPacket.DstIP.String(), "Decapsulated destination IP should match inner packet")
	})

	t.Run("Test_Fragmented_Packet_Drop", func(t *testing.T) {
		// Test that fragmented packets are dropped (based on unit tests)
		packet := createFragmentedIPIP6Packet(
			net.ParseIP("4.5.6.7"), // outer IPv4 dst (matches our prefix)
			net.ParseIP("::1"),     // inner IPv6 src
			net.ParseIP("::2"),     // inner IPv6 dst
			0,                      // fragment offset
			true,                   // more fragments
		)

		// Send packet and wait for response (expect timeout or drop)
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)

		// Fragmented packets should be dropped
		require.Error(t, err, "Fragmented packet should be dropped")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.Nil(t, outputPacket, "Output packet should be absent")
		require.True(t, inputPacket.IsTunneled, "Input packet should be tunneled")
	})

	t.Run("Test_Invalid_GRE_Version", func(t *testing.T) {
		// Test GRE packet with invalid version (should be dropped)
		// Based on unit test TestDecap_GRE negative cases
		packet := createInvalidGREPacket(
			net.ParseIP("1:2:3:4::abcd"), // outer IPv6 dst (matches our prefix)
			net.ParseIP("1.1.0.0"),       // inner IPv4 src
			net.ParseIP("2.2.0.0"),       // inner IPv4 dst
			1,                            // invalid version (should be 0)
		)

		// Send packet and wait for response (expect timeout or drop)
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)

		// Invalid GRE packets should be dropped
		require.Error(t, err, "Invalid GRE packet should be dropped")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.Nil(t, outputPacket, "Output packet should be absent")
		require.True(t, inputPacket.IsTunneled, "Input packet should be tunneled")
	})

	t.Run("Test_GRE_With_Reserved_Bits", func(t *testing.T) {
		// Test GRE packet with reserved bits set (should be dropped)
		packet := createGREWithReservedBits(
			net.ParseIP("1:2:3:4::abcd"), // outer IPv6 dst (matches our prefix)
			net.ParseIP("1.1.0.0"),       // inner IPv4 src
			net.ParseIP("2.2.0.0"),       // inner IPv4 dst
		)

		// Send packet and wait for response (expect timeout or drop)
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)

		// GRE packets with reserved bits should be dropped
		require.Error(t, err, "GRE packet with reserved bits should be dropped")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.Nil(t, outputPacket, "Output packet should be absent")
		require.True(t, inputPacket.IsTunneled, "Input packet should be tunneled")
	})

	t.Run("Test_IPv6_Fragment_Drop", func(t *testing.T) {
		// Test that IPv6 fragmented packets are dropped
		packet := createFragmentedIP6IPPacket(
			net.ParseIP("1:2:3:4::abcd"), // outer IPv6 dst (matches our prefix)
			net.ParseIP("1.1.0.0"),       // inner IPv4 src
			net.ParseIP("2.2.0.0"),       // inner IPv4 dst
		)

		// Send packet and wait for response (expect timeout or drop)
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)

		// Fragmented IPv6 packets should be dropped
		require.Error(t, err, "Fragmented IPv6 packet should be dropped")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.Nil(t, outputPacket, "Output packet should be absent")
		require.True(t, inputPacket.IsTunneled, "Input packet should be tunneled")
	})

	t.Run("Check_Yanet_State", func(t *testing.T) {
		cmd := "/mnt/target/release/yanet-cli-counters pipeline --instance 0 --device-name kni0 --pipeline-name test"
		output, err := fw.CLI.ExecuteCommand(cmd)
		require.NoError(t, err, "Failed to execute command %s with output %s", cmd, output)
	})
}

// Helper functions to create test packets based on unit tests

// createInvalidGREPacket creates a GRE packet with invalid version
func createInvalidGREPacket(outerDstIP, innerSrcIP, innerDstIP net.IP, version uint8) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv6,
	}

	// Outer IPv6 tunnel header
	ip6tunnel := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolGRE,
		HopLimit:   64,
		SrcIP:      net.IPv6zero,
		DstIP:      outerDstIP,
	}

	// GRE header with invalid version
	gre := layers.GRE{
		Protocol: layers.EthernetTypeIPv4,
		Version:  version, // Invalid version (should be 0)
	}

	// Inner IPv4 packet
	ip4 := layers.IPv4{
		Version:  4,
		IHL:      5,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    innerSrcIP,
		DstIP:    innerDstIP,
	}

	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       1,
		Seq:      1,
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err := gopacket.SerializeLayers(buf, opts, &eth, &ip6tunnel, &gre, &ip4, &icmp)
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// createIPIP6Packet creates an IPv6-in-IPv4 tunneled packet
// Based on unit test TestDecap_IPIP6
func createIPIP6Packet(outerDstIP, innerSrcIP, innerDstIP net.IP) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv4,
	}

	// Outer IPv4 tunnel header
	ip4tunnel := layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolIPv6,
		SrcIP:    net.IPv4zero,
		DstIP:    outerDstIP,
	}

	// Inner IPv6 packet
	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      innerSrcIP,
		DstIP:      innerDstIP,
	}

	icmp := layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	err := icmp.SetNetworkLayerForChecksum(&ip6)
	if err != nil {
		panic(err)
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err = gopacket.SerializeLayers(buf, opts, &eth, &ip4tunnel, &ip6, &icmp)
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// createIP6IPPacket creates an IPv4-in-IPv6 tunneled packet
// Based on unit test TestDecap_IP6IP
func createIP6IPPacket(outerDstIP, innerSrcIP, innerDstIP net.IP) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv6,
	}

	// Outer IPv6 tunnel header
	ip6tunnel := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolIPv4,
		HopLimit:   64,
		SrcIP:      net.IPv6zero,
		DstIP:      outerDstIP,
	}

	// Inner IPv4 packet
	ip4 := layers.IPv4{
		Version:  4,
		IHL:      5,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    innerSrcIP,
		DstIP:    innerDstIP,
	}

	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       1,
		Seq:      1,
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err := gopacket.SerializeLayers(buf, opts, &eth, &ip6tunnel, &ip4, &icmp)
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// createIPIPPacket creates an IPv4-in-IPv4 tunneled packet
// Based on unit test TestDecap_IPIP
func createIPIPPacket(outerDstIP, innerSrcIP, innerDstIP net.IP) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv4,
	}

	// Outer IPv4 tunnel header
	ip4tunnel := layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolIPv4,
		SrcIP:    net.IPv4zero,
		DstIP:    outerDstIP,
	}

	// Inner IPv4 packet
	ip4 := layers.IPv4{
		Version:  4,
		IHL:      5,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    innerSrcIP,
		DstIP:    innerDstIP,
	}

	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       1,
		Seq:      1,
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err := gopacket.SerializeLayers(buf, opts, &eth, &ip4tunnel, &ip4, &icmp)
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// createIP6IP6Packet creates an IPv6-in-IPv6 tunneled packet
// Based on unit test TestDecap_IP6IP6
func createIP6IP6Packet(outerDstIP, innerSrcIP, innerDstIP net.IP) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv6,
	}

	// Outer IPv6 tunnel header
	ip6tunnel := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolIPv6,
		HopLimit:   64,
		SrcIP:      net.IPv6zero,
		DstIP:      outerDstIP,
	}

	// Inner IPv6 packet
	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      innerSrcIP,
		DstIP:      innerDstIP,
	}

	icmp := layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	err := icmp.SetNetworkLayerForChecksum(&ip6)
	if err != nil {
		panic(err)
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err = gopacket.SerializeLayers(buf, opts, &eth, &ip6tunnel, &ip6, &icmp)
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// createGRESimplePacket creates a simple GRE tunneled packet
// Based on unit test TestDecap_GRE
func createGRESimplePacket(outerDstIP, innerSrcIP, innerDstIP net.IP) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv6,
	}

	// Outer IPv6 tunnel header
	ip6tunnel := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolGRE,
		HopLimit:   64,
		SrcIP:      net.IPv6zero,
		DstIP:      outerDstIP,
	}

	// GRE header
	gre := layers.GRE{
		Protocol: layers.EthernetTypeIPv4,
	}

	// Inner IPv4 packet
	ip4 := layers.IPv4{
		Version:  4,
		IHL:      5,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    innerSrcIP,
		DstIP:    innerDstIP,
	}

	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       1,
		Seq:      1,
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err := gopacket.SerializeLayers(buf, opts, &eth, &ip6tunnel, &gre, &ip4, &icmp)
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// createGREWithReservedBits creates a GRE packet with reserved bits set
func createGREWithReservedBits(outerDstIP, innerSrcIP, innerDstIP net.IP) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv6,
	}

	// Outer IPv6 tunnel header
	ip6tunnel := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolGRE,
		HopLimit:   64,
		SrcIP:      net.IPv6zero,
		DstIP:      outerDstIP,
	}

	// GRE header with reserved bits set (StrictSourceRoute = true)
	gre := layers.GRE{
		Protocol:          layers.EthernetTypeIPv4,
		Version:           0,
		StrictSourceRoute: true, // This should cause packet to be dropped
	}

	// Inner IPv4 packet
	ip4 := layers.IPv4{
		Version:  4,
		IHL:      5,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    innerSrcIP,
		DstIP:    innerDstIP,
	}

	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       1,
		Seq:      1,
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err := gopacket.SerializeLayers(buf, opts, &eth, &ip6tunnel, &gre, &ip4, &icmp)
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// createFragmentedIP6IPPacket creates a fragmented IPv4-in-IPv6 packet
func createFragmentedIP6IPPacket(outerDstIP, innerSrcIP, innerDstIP net.IP) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv6,
	}

	// Outer IPv6 tunnel header with fragment header
	ip6tunnel := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolIPv6Fragment,
		HopLimit:   64,
		SrcIP:      net.IPv6zero,
		DstIP:      outerDstIP,
	}

	// IPv6 Fragment header
	frag := layers.IPv6Fragment{
		NextHeader:     layers.IPProtocolIPv4,
		Identification: 0x31337,
		FragmentOffset: 0,
		MoreFragments:  true, // This should cause packet to be dropped
	}

	// Inner IPv4 packet
	ip4 := layers.IPv4{
		Version:  4,
		IHL:      5,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    innerSrcIP,
		DstIP:    innerDstIP,
	}

	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       1,
		Seq:      1,
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err := gopacket.SerializeLayers(buf, opts, &eth, &ip6tunnel, &frag, &ip4, &icmp)
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// createGREPacket creates a GRE tunneled packet
// Based on TestDecap_GRE unit test
func createGREPacket(outerDstIP, innerSrcIP, innerDstIP net.IP, withChecksum, withKey, withSeq bool) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv6,
	}

	// Outer IPv6 tunnel header
	ip6tunnel := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolGRE,
		HopLimit:   64,
		SrcIP:      net.IPv6zero,
		DstIP:      outerDstIP,
	}

	// GRE header
	gre := layers.GRE{
		Protocol:        layers.EthernetTypeIPv4,
		ChecksumPresent: withChecksum,
		KeyPresent:      withKey,
		SeqPresent:      withSeq,
		Version:         0,
	}

	// Inner IPv4 packet
	ip4 := layers.IPv4{
		Version:  4,
		IHL:      5,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    innerSrcIP,
		DstIP:    innerDstIP,
	}

	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       1,
		Seq:      1,
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err := gopacket.SerializeLayers(buf, opts, &eth, &ip6tunnel, &gre, &ip4, &icmp)
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// createFragmentedIPIP6Packet creates a fragmented IPv6-in-IPv4 tunneled packet
// Based on unit test fragment handling
func createFragmentedIPIP6Packet(outerDstIP, innerSrcIP, innerDstIP net.IP, fragOffset uint16, moreFragments bool) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv4,
	}

	// Outer IPv4 tunnel header with fragmentation
	ip4tunnel := layers.IPv4{
		Version:    4,
		IHL:        5,
		TTL:        64,
		Protocol:   layers.IPProtocolIPv6,
		SrcIP:      net.IPv4zero,
		DstIP:      outerDstIP,
		FragOffset: fragOffset,
		Flags:      0,
	}

	if moreFragments {
		ip4tunnel.Flags |= layers.IPv4MoreFragments
	}

	// Inner IPv6 packet
	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      innerSrcIP,
		DstIP:      innerDstIP,
	}

	icmp := layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	err := icmp.SetNetworkLayerForChecksum(&ip6)
	if err != nil {
		panic(err)
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err = gopacket.SerializeLayers(buf, opts, &eth, &ip4tunnel, &ip6, &icmp)
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}
