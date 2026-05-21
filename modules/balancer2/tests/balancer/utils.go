package test

import (
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

type PacketGenerator struct {
	SrcMAC, DstMAC net.HardwareAddr
}

func NewPacketGenerator() *PacketGenerator {
	return &PacketGenerator{
		SrcMAC: net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		DstMAC: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
	}
}

func (pg *PacketGenerator) createEthernet(ethernetType layers.EthernetType) *layers.Ethernet {
	return &layers.Ethernet{
		SrcMAC:       pg.SrcMAC,
		DstMAC:       pg.DstMAC,
		EthernetType: ethernetType,
	}
}

func (pg *PacketGenerator) createIPv4(src, dst net.IP) *layers.IPv4 {
	return &layers.IPv4{
		Version: 4,
		TTL:     64,
		SrcIP:   src,
		DstIP:   dst,
	}
}

func (pg *PacketGenerator) createIPv6(src, dst net.IP) *layers.IPv6 {
	return &layers.IPv6{
		Version:  6,
		HopLimit: 64,
		SrcIP:    src,
		DstIP:    dst,
	}
}

func (pg *PacketGenerator) MakePacket(
	srcIP, dstIP string,
	protocol gopacket.LayerType,
	additionalLayers ...gopacket.SerializableLayer,
) []gopacket.SerializableLayer {
	src := net.ParseIP(srcIP)
	dst := net.ParseIP(dstIP)

	var ip gopacket.SerializableLayer
	ethernetType := layers.EthernetTypeIPv6
	if src.To4() != nil {
		ethernetType = layers.EthernetTypeIPv4
		ipv4 := pg.createIPv4(src, dst)
		switch protocol {
		case layers.LayerTypeTCP:
			ipv4.Protocol = layers.IPProtocolTCP
		case layers.LayerTypeUDP:
			ipv4.Protocol = layers.IPProtocolUDP
		case layers.LayerTypeICMPv4:
			ipv4.Protocol = layers.IPProtocolICMPv4
		}
		ip = ipv4
	} else {
		ipv6 := pg.createIPv6(src, dst)
		switch protocol {
		case layers.LayerTypeTCP:
			ipv6.NextHeader = layers.IPProtocolTCP
		case layers.LayerTypeUDP:
			ipv6.NextHeader = layers.IPProtocolUDP
		case layers.LayerTypeICMPv6:
			ipv6.NextHeader = layers.IPProtocolICMPv6
		}
		ip = ipv6
	}

	eth := pg.createEthernet(ethernetType)

	layers := []gopacket.SerializableLayer{eth, ip}
	layers = append(layers, additionalLayers...)

	return layers
}

func (pg *PacketGenerator) MakeTCPPacket(
	srcIP, dstIP string,
	srcPort, dstPort uint16,
	syn, ack, rst, fin bool,
	payload []byte,
) []gopacket.SerializableLayer {
	tcp := &layers.TCP{
		SrcPort: layers.TCPPort(srcPort),
		DstPort: layers.TCPPort(dstPort),
		SYN:     syn,
		ACK:     ack,
		RST:     rst,
		FIN:     fin,
	}
	packet := pg.MakePacket(srcIP, dstIP, layers.LayerTypeTCP)

	switch ipv4OrIPv6 := packet[1].(type) {
	case *layers.IPv4:
		tcp.SetNetworkLayerForChecksum(ipv4OrIPv6)
	case *layers.IPv6:
		tcp.SetNetworkLayerForChecksum(ipv4OrIPv6)
	}

	return pg.MakePacket(srcIP, dstIP, layers.LayerTypeTCP, tcp, gopacket.Payload(payload))
}

func (pg *PacketGenerator) MakeUDPPacket(
	srcIP, dstIP string,
	srcPort, dstPort uint16,
	payload []byte,
) []gopacket.SerializableLayer {
	udp := &layers.UDP{
		SrcPort: layers.UDPPort(srcPort),
		DstPort: layers.UDPPort(dstPort),
	}
	packet := pg.MakePacket(srcIP, dstIP, layers.LayerTypeUDP)

	switch ipv4OrIPv6 := packet[1].(type) {
	case *layers.IPv4:
		udp.SetNetworkLayerForChecksum(ipv4OrIPv6)
	case *layers.IPv6:
		udp.SetNetworkLayerForChecksum(ipv4OrIPv6)
	}

	return pg.MakePacket(srcIP, dstIP, layers.LayerTypeUDP, udp, gopacket.Payload(payload))
}

func (pg *PacketGenerator) MakeICMPPacket(
	srcIP, dstIP string,
	icmpType layers.ICMPv4TypeCode,
	payload []byte,
) []gopacket.SerializableLayer {
	icmp := &layers.ICMPv4{
		TypeCode: icmpType,
	}

	return pg.MakePacket(srcIP, dstIP, layers.LayerTypeICMPv4, icmp, gopacket.Payload(payload))
}

func (pg *PacketGenerator) MakeICMPv6Packet(
	srcIP, dstIP string,
	icmpType layers.ICMPv6TypeCode,
	payload []byte,
) []gopacket.SerializableLayer {
	icmp := &layers.ICMPv6{
		TypeCode: icmpType,
	}

	return pg.MakePacket(srcIP, dstIP, layers.LayerTypeICMPv6, icmp, gopacket.Payload(payload))
}
