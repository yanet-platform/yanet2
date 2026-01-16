package utils

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// MakeTCPPacket creates a TCP packet with the specified parameters.
// Supports both IPv4 and IPv6.
func MakeTCPPacket(
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
	tcp *layers.TCP,
) []gopacket.SerializableLayer {
	// Ensure both addresses are the same IP version
	if srcIP.Is4() != dstIP.Is4() {
		panic(fmt.Sprintf("IP version mismatch: src=%v dst=%v", srcIP, dstIP))
	}

	src := net.IP(srcIP.AsSlice())
	dst := net.IP(dstIP.AsSlice())

	var ip gopacket.NetworkLayer
	ethernetType := layers.EthernetTypeIPv6
	if srcIP.Is4() {
		ethernetType = layers.EthernetTypeIPv4
		ip = &layers.IPv4{
			Version:  4,
			IHL:      5,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    src,
			DstIP:    dst,
			TOS:      214,
		}
	} else {
		ip = &layers.IPv6{
			Version:      6,
			NextHeader:   layers.IPProtocolTCP,
			HopLimit:     64,
			SrcIP:        src,
			DstIP:        dst,
			TrafficClass: 139,
		}
	}

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		EthernetType: ethernetType,
	}

	tcp.SrcPort = layers.TCPPort(srcPort)
	tcp.DstPort = layers.TCPPort(dstPort)
	tcp.SetNetworkLayerForChecksum(ip)

	payload := []byte("BALANCER TEST PAYLOAD 12345678910")
	packetLayers := []gopacket.SerializableLayer{
		eth,
		ip.(gopacket.SerializableLayer),
		tcp,
		gopacket.Payload(payload),
	}

	return packetLayers
}

// MakeUDPPacket creates a UDP packet with the specified parameters.
// Supports both IPv4 and IPv6.
func MakeUDPPacket(
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
) []gopacket.SerializableLayer {
	// Ensure both addresses are the same IP version
	if srcIP.Is4() != dstIP.Is4() {
		panic(fmt.Sprintf("IP version mismatch: src=%v dst=%v", srcIP, dstIP))
	}

	src := net.IP(srcIP.AsSlice())
	dst := net.IP(dstIP.AsSlice())

	var ip gopacket.NetworkLayer
	ethernetType := layers.EthernetTypeIPv6
	if srcIP.Is4() {
		ethernetType = layers.EthernetTypeIPv4
		ip = &layers.IPv4{
			Version:  4,
			IHL:      5,
			TTL:      64,
			Protocol: layers.IPProtocolUDP,
			SrcIP:    src,
			DstIP:    dst,
			TOS:      123,
		}
	} else {
		ip = &layers.IPv6{
			Version:      6,
			NextHeader:   layers.IPProtocolUDP,
			HopLimit:     64,
			SrcIP:        src,
			DstIP:        dst,
			TrafficClass: 212,
		}
	}

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		EthernetType: ethernetType,
	}

	udp := &layers.UDP{
		SrcPort: layers.UDPPort(srcPort),
		DstPort: layers.UDPPort(dstPort),
	}
	udp.SetNetworkLayerForChecksum(ip)

	payload := []byte("PING TEST PAYLOAD 1234567890")
	packetLayers := []gopacket.SerializableLayer{
		eth,
		ip.(gopacket.SerializableLayer),
		udp,
		gopacket.Payload(payload),
	}

	return packetLayers
}

// MakePacketLayers creates packet layers based on whether TCP or UDP is specified.
// If tcp is nil, creates a UDP packet; otherwise creates a TCP packet.
func MakePacketLayers(
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
	tcp *layers.TCP,
) []gopacket.SerializableLayer {
	if tcp == nil {
		return MakeUDPPacket(srcIP, srcPort, dstIP, dstPort)
	}
	return MakeTCPPacket(srcIP, srcPort, dstIP, dstPort, tcp)
}
