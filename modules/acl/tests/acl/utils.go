package test_acl

import (
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/yanet-platform/yanet2/tests/go/common"
)

////////////////////////////////////////////////////////////////////////////////

func MakeUDPPacket(
	srcIP string,
	srcPort uint16,
	dstIP string,
	dstPort uint16,
) []gopacket.SerializableLayer {

	src := net.ParseIP(srcIP)
	dst := net.ParseIP(dstIP)

	var ip gopacket.NetworkLayer
	ethernetType := layers.EthernetTypeIPv6
	if src.To4() != nil {
		ethernetType = layers.EthernetTypeIPv4
		ip = &layers.IPv4{
			Version:  4,
			IHL:      5,
			TTL:      64,
			Protocol: layers.IPProtocolUDP,
			SrcIP:    src,
			DstIP:    dst,
		}
	} else {
		ip = &layers.IPv6{
			Version:    6,
			NextHeader: layers.IPProtocolUDP,
			HopLimit:   64,
			SrcIP:      src,
			DstIP:      dst,
		}
	}

	eth := &layers.Ethernet{
		SrcMAC:       common.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       common.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: ethernetType,
	}

	udp := &layers.UDP{
		SrcPort: layers.UDPPort(srcPort),
		DstPort: layers.UDPPort(dstPort),
	}
	udp.SetNetworkLayerForChecksum(ip)

	payload := []byte("PING TEST PAYLOAD 1234567890")
	layers := []gopacket.SerializableLayer{
		eth,
		ip.(gopacket.SerializableLayer),
		udp,
		gopacket.Payload(payload),
	}

	return layers
}

////////////////////////////////////////////////////////////////////////////////

func MakeTCPPacket(
	srcIP string,
	srcPort uint16,
	dstIP string,
	dstPort uint16,
	tcp *layers.TCP,
) []gopacket.SerializableLayer {

	src := net.ParseIP(srcIP)
	dst := net.ParseIP(dstIP)

	var ip gopacket.NetworkLayer
	ethernetType := layers.EthernetTypeIPv6
	if src.To4() != nil {
		ethernetType = layers.EthernetTypeIPv4
		ip = &layers.IPv4{
			Version:  4,
			IHL:      5,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    src,
			DstIP:    dst,
		}
	} else {
		ip = &layers.IPv6{
			Version:    6,
			NextHeader: layers.IPProtocolTCP,
			HopLimit:   64,
			SrcIP:      src,
			DstIP:      dst,
		}
	}

	eth := &layers.Ethernet{
		SrcMAC:       common.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       common.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: ethernetType,
	}

	tcp.SrcPort = layers.TCPPort(srcPort)
	tcp.DstPort = layers.TCPPort(dstPort)
	tcp.SetNetworkLayerForChecksum(ip)

	payload := []byte("PING TEST PAYLOAD 1234567890")
	layers := []gopacket.SerializableLayer{
		eth,
		ip.(gopacket.SerializableLayer),
		tcp,
		gopacket.Payload(payload),
	}

	return layers
}
