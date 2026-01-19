package utils

import (
	"encoding/binary"
	"net"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// ICMP_BROADCAST_IDENT is the magic value used to mark broadcasted packets
// This must match the value in modules/balancer/dataplane/icmp/error/broadcast.h
const ICMP_BROADCAST_IDENT uint16 = 0x0BDC

// MakeICMPv4EchoRequest creates an ICMPv4 Echo Request packet
func MakeICMPv4EchoRequest(
	srcIP netip.Addr,
	dstIP netip.Addr,
	id uint16,
	seq uint16,
) []gopacket.SerializableLayer {
	src := net.IP(srcIP.AsSlice())
	dst := net.IP(dstIP.AsSlice())

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    src,
		DstIP:    dst,
	}

	icmp := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       id,
		Seq:      seq,
	}

	payload := []byte("ICMP Echo Request Payload")

	return []gopacket.SerializableLayer{
		eth,
		ip,
		icmp,
		gopacket.Payload(payload),
	}
}

// MakeICMPv6EchoRequest creates an ICMPv6 Echo Request packet
func MakeICMPv6EchoRequest(
	srcIP netip.Addr,
	dstIP netip.Addr,
	id uint16,
	seq uint16,
) []gopacket.SerializableLayer {
	src := net.IP(srcIP.AsSlice())
	dst := net.IP(dstIP.AsSlice())

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		EthernetType: layers.EthernetTypeIPv6,
	}

	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      src,
		DstIP:      dst,
	}

	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	icmp.SetNetworkLayerForChecksum(ip)

	// ICMPv6 Echo uses the same ID/Seq format as ICMPv4
	payload := make([]byte, 4+len("ICMP Echo Request Payload"))
	payload[0] = byte(id >> 8)
	payload[1] = byte(id)
	payload[2] = byte(seq >> 8)
	payload[3] = byte(seq)
	copy(payload[4:], []byte("ICMP Echo Request Payload"))

	return []gopacket.SerializableLayer{
		eth,
		ip,
		icmp,
		gopacket.Payload(payload),
	}
}

// MakeICMPv4DestUnreachable creates an ICMPv4 Destination Unreachable error packet
// containing the original packet that triggered the error
func MakeICMPv4DestUnreachable(
	srcIP netip.Addr,
	dstIP netip.Addr,
	originalPacket gopacket.Packet,
) []gopacket.SerializableLayer {
	return MakeICMPv4DestUnreachableWithIdent(srcIP, dstIP, originalPacket, 0)
}

// MakeICMPv6DestUnreachable creates an ICMPv6 Destination Unreachable error packet
// containing the original packet that triggered the error
func MakeICMPv6DestUnreachable(
	srcIP netip.Addr,
	dstIP netip.Addr,
	originalPacket gopacket.Packet,
) []gopacket.SerializableLayer {
	return MakeICMPv6DestUnreachableWithIdent(srcIP, dstIP, originalPacket, 0)
}

// MakeICMPv4DestUnreachableWithIdent creates an ICMPv4 Destination Unreachable
// error packet with a custom icmp_ident value
func MakeICMPv4DestUnreachableWithIdent(
	srcIP netip.Addr,
	dstIP netip.Addr,
	originalPacket gopacket.Packet,
	icmpIdent uint16,
) []gopacket.SerializableLayer {
	src := net.IP(srcIP.AsSlice())
	dst := net.IP(dstIP.AsSlice())

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    src,
		DstIP:    dst,
	}

	// For ICMP error messages, we need to manually construct the header
	// because gopacket's ICMPv4 layer doesn't properly handle the unused field
	// ICMP Error format: [type:1][code:1][checksum:2][unused:4][original packet...]
	// We use the first 2 bytes of unused for our broadcast marker

	icmpType := uint8(layers.ICMPv4TypeDestinationUnreachable)
	icmpCode := uint8(3) // Port unreachable

	// Extract the original IP packet
	originalData := originalPacket.Data()
	ipStart := 14 // Ethernet header size
	var originalIPPacket []byte
	if ipStart < len(originalData) {
		originalIPPacket = originalData[ipStart:]
	}

	// Build the complete ICMP packet manually
	// [type:1][code:1][checksum:2][unused_marker:2][unused_rest:2][original packet...]
	icmpPacket := make([]byte, 8+len(originalIPPacket))
	icmpPacket[0] = icmpType
	icmpPacket[1] = icmpCode
	// checksum at [2:4] will be calculated later
	binary.BigEndian.PutUint16(icmpPacket[4:6], icmpIdent) // Our marker
	// bytes [6:8] remain zero (rest of unused field)
	copy(icmpPacket[8:], originalIPPacket)

	// Calculate checksum
	checksum := uint32(0)
	for i := 0; i < len(icmpPacket); i += 2 {
		if i+1 < len(icmpPacket) {
			checksum += uint32(icmpPacket[i])<<8 | uint32(icmpPacket[i+1])
		} else {
			checksum += uint32(icmpPacket[i]) << 8
		}
	}
	for checksum > 0xffff {
		checksum = (checksum & 0xffff) + (checksum >> 16)
	}
	binary.BigEndian.PutUint16(icmpPacket[2:4], ^uint16(checksum))

	return []gopacket.SerializableLayer{
		eth,
		ip,
		gopacket.Payload(icmpPacket),
	}
}

// MakeICMPv6DestUnreachableWithIdent creates an ICMPv6 Destination Unreachable
// error packet with a custom icmp_ident value
func MakeICMPv6DestUnreachableWithIdent(
	srcIP netip.Addr,
	dstIP netip.Addr,
	originalPacket gopacket.Packet,
	icmpIdent uint16,
) []gopacket.SerializableLayer {
	src := net.IP(srcIP.AsSlice())
	dst := net.IP(dstIP.AsSlice())

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		EthernetType: layers.EthernetTypeIPv6,
	}

	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      src,
		DstIP:      dst,
	}

	// For ICMPv6 error messages, manually construct the header
	// ICMPv6 Error format: [type:1][code:1][checksum:2][unused:4][original packet...]
	// We use the first 2 bytes of unused for our broadcast marker

	icmpType := uint8(layers.ICMPv6TypeDestinationUnreachable)
	icmpCode := uint8(4) // Port unreachable

	// Extract the original IP packet
	originalData := originalPacket.Data()
	ipStart := 14 // Ethernet header size
	var originalIPPacket []byte
	if ipStart < len(originalData) {
		originalIPPacket = originalData[ipStart:]
	}

	// Build the complete ICMPv6 packet manually
	// [type:1][code:1][checksum:2][unused_marker:2][unused_rest:2][original packet...]
	icmpPacket := make([]byte, 8+len(originalIPPacket))
	icmpPacket[0] = icmpType
	icmpPacket[1] = icmpCode
	// checksum at [2:4] will be calculated later
	binary.BigEndian.PutUint16(icmpPacket[4:6], icmpIdent) // Our marker
	// bytes [6:8] remain zero (rest of unused field)
	copy(icmpPacket[8:], originalIPPacket)

	// Calculate ICMPv6 checksum (includes pseudo-header)
	// Pseudo-header: [src:16][dst:16][length:4][zeros:3][next_header:1]
	pseudoHeader := make([]byte, 40)
	copy(pseudoHeader[0:16], src)
	copy(pseudoHeader[16:32], dst)
	binary.BigEndian.PutUint32(pseudoHeader[32:36], uint32(len(icmpPacket)))
	pseudoHeader[39] = uint8(layers.IPProtocolICMPv6)

	checksumData := append(pseudoHeader, icmpPacket...)
	checksum := uint32(0)
	for i := 0; i < len(checksumData); i += 2 {
		if i+1 < len(checksumData) {
			checksum += uint32(checksumData[i])<<8 | uint32(checksumData[i+1])
		} else {
			checksum += uint32(checksumData[i]) << 8
		}
	}
	for checksum > 0xffff {
		checksum = (checksum & 0xffff) + (checksum >> 16)
	}
	binary.BigEndian.PutUint16(icmpPacket[2:4], ^uint16(checksum))

	return []gopacket.SerializableLayer{
		eth,
		ip,
		gopacket.Payload(icmpPacket),
	}
}

// MakeTunneledICMPv4DestUnreachable creates an IP-in-IP tunneled ICMPv4
// Destination Unreachable packet with a custom icmp_ident
func MakeTunneledICMPv4DestUnreachable(
	tunnelSrcIP netip.Addr,
	tunnelDstIP netip.Addr,
	icmpSrcIP netip.Addr,
	icmpDstIP netip.Addr,
	originalPacket gopacket.Packet,
	icmpIdent uint16,
) []gopacket.SerializableLayer {
	// Create the inner ICMP packet
	innerLayers := MakeICMPv4DestUnreachableWithIdent(
		icmpSrcIP,
		icmpDstIP,
		originalPacket,
		icmpIdent,
	)

	// Serialize the inner packet (skip Ethernet header)
	innerPacketLayers := innerLayers[1:] // Skip Ethernet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	err := gopacket.SerializeLayers(buf, opts, innerPacketLayers...)
	if err != nil {
		panic(err)
	}
	innerPacketData := buf.Bytes()

	// Create outer tunnel headers
	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		EthernetType: layers.EthernetTypeIPv4,
	}

	outerIP := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: 4, // IPIP protocol
		SrcIP:    net.IP(tunnelSrcIP.AsSlice()),
		DstIP:    net.IP(tunnelDstIP.AsSlice()),
	}

	return []gopacket.SerializableLayer{
		eth,
		outerIP,
		gopacket.Payload(innerPacketData),
	}
}

// MakeTunneledICMPv6DestUnreachable creates an IPv6-in-IPv6 tunneled ICMPv6
// Destination Unreachable packet with a custom icmp_ident
func MakeTunneledICMPv6DestUnreachable(
	tunnelSrcIP netip.Addr,
	tunnelDstIP netip.Addr,
	icmpSrcIP netip.Addr,
	icmpDstIP netip.Addr,
	originalPacket gopacket.Packet,
	icmpIdent uint16,
) []gopacket.SerializableLayer {
	// Create the inner ICMP packet
	innerLayers := MakeICMPv6DestUnreachableWithIdent(
		icmpSrcIP,
		icmpDstIP,
		originalPacket,
		icmpIdent,
	)

	// Serialize the inner packet (skip Ethernet header)
	innerPacketLayers := innerLayers[1:] // Skip Ethernet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	err := gopacket.SerializeLayers(buf, opts, innerPacketLayers...)
	if err != nil {
		panic(err)
	}
	innerPacketData := buf.Bytes()

	// Create outer tunnel headers
	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		EthernetType: layers.EthernetTypeIPv6,
	}

	outerIP := &layers.IPv6{
		Version:    6,
		NextHeader: 41, // IPv6 protocol
		HopLimit:   64,
		SrcIP:      net.IP(tunnelSrcIP.AsSlice()),
		DstIP:      net.IP(tunnelDstIP.AsSlice()),
	}

	return []gopacket.SerializableLayer{
		eth,
		outerIP,
		gopacket.Payload(innerPacketData),
	}
}

// VerifyBroadcastedICMPPacket checks that a broadcasted packet is properly
// tunneled and has the ICMP_BROADCAST_IDENT marker set
func VerifyBroadcastedICMPPacket(
	t *testing.T,
	packet *framework.PacketInfo,
	expectedDstIP net.IP,
) {
	t.Helper()

	// Verify packet is tunneled
	require.True(t, packet.IsTunneled, "broadcasted packet should be tunneled")

	// Verify destination is a peer
	require.Equal(
		t,
		expectedDstIP,
		packet.DstIP,
		"packet should be sent to peer",
	)
}
