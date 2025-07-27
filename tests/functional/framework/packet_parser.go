package framework

import (
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// PacketInfo contains parsed packet information
type PacketInfo struct {
	// Ethernet layer
	SrcMAC net.HardwareAddr
	DstMAC net.HardwareAddr

	// IP layer (IPv4 or IPv6)
	IsIPv4     bool
	IsIPv6     bool
	SrcIP      net.IP
	DstIP      net.IP
	Protocol   layers.IPProtocol
	NextHeader layers.IPProtocol // For IPv6

	// Transport layer
	SrcPort uint16
	DstPort uint16

	// Tunnel information
	IsTunneled  bool
	TunnelType  string // "ip4in4", "ip6in4", "ip4in6", "ip6in6", "gre"
	InnerPacket *PacketInfo

	// Raw data
	RawData []byte
	Payload []byte
}

// PacketParser provides packet parsing and verification functionality
type PacketParser struct{}

// NewPacketParser creates a new packet parser
func NewPacketParser() *PacketParser {
	return &PacketParser{}
}

// ParsePacket parses a raw packet and returns packet information
func (p *PacketParser) ParsePacket(data []byte) (*PacketInfo, error) {
	// Pad packet to minimum Ethernet frame size
	if len(data) < 60 {
		padded := make([]byte, 60)
		copy(padded, data)
		data = padded
	}

	packet := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
	if packet.ErrorLayer() != nil {
		return nil, fmt.Errorf("packet parsing error: %v", packet.ErrorLayer().Error())
	}

	info := &PacketInfo{
		RawData: data,
	}

	// Parse Ethernet layer
	if ethLayer := packet.Layer(layers.LayerTypeEthernet); ethLayer != nil {
		eth := ethLayer.(*layers.Ethernet)
		info.SrcMAC = eth.SrcMAC
		info.DstMAC = eth.DstMAC
	}

	// Parse IP layers
	if err := p.parseIPLayers(packet, info); err != nil {
		return nil, fmt.Errorf("failed to parse IP layers: %w", err)
	}

	// Parse transport layer
	p.parseTransportLayer(packet, info)

	// Extract payload
	if appLayer := packet.ApplicationLayer(); appLayer != nil {
		info.Payload = appLayer.Payload()
	}

	return info, nil
}

// parseIPLayers parses IPv4 and IPv6 layers, including tunneled packets
func (p *PacketParser) parseIPLayers(packet gopacket.Packet, info *PacketInfo) error {
	// Check for IPv4
	if ipv4Layer := packet.Layer(layers.LayerTypeIPv4); ipv4Layer != nil {
		ipv4 := ipv4Layer.(*layers.IPv4)
		info.IsIPv4 = true
		info.SrcIP = ipv4.SrcIP
		info.DstIP = ipv4.DstIP
		info.Protocol = ipv4.Protocol

		// Check for tunneled packets
		if err := p.checkTunnelInIPv4(packet, ipv4, info); err != nil {
			return err
		}
	}

	// Check for IPv6
	if ipv6Layer := packet.Layer(layers.LayerTypeIPv6); ipv6Layer != nil {
		ipv6 := ipv6Layer.(*layers.IPv6)
		info.IsIPv6 = true
		info.SrcIP = ipv6.SrcIP
		info.DstIP = ipv6.DstIP
		info.NextHeader = ipv6.NextHeader

		// Check for tunneled packets
		if err := p.checkTunnelInIPv6(packet, ipv6, info); err != nil {
			return err
		}
	}

	return nil
}

// checkTunnelInIPv4 checks for tunneled packets in IPv4
func (p *PacketParser) checkTunnelInIPv4(packet gopacket.Packet, ipv4 *layers.IPv4, info *PacketInfo) error {
	// Check if this is a fragmented packet
	if ipv4.Flags&layers.IPv4MoreFragments != 0 || ipv4.FragOffset != 0 {
		// This is a fragmented packet
		switch ipv4.Protocol {
		case layers.IPProtocolIPv4:
			// Fragmented IPv4-in-IPv4 tunnel
			info.IsTunneled = true
			info.TunnelType = "ip4in4"
			return nil // Don't parse inner packet for fragments
		case layers.IPProtocolIPv6:
			// Fragmented IPv6-in-IPv4 tunnel
			info.IsTunneled = true
			info.TunnelType = "ip6in4"
			return nil // Don't parse inner packet for fragments
		case layers.IPProtocolGRE:
			// Fragmented GRE tunnel
			info.IsTunneled = true
			info.TunnelType = "gre"
			return nil // Don't parse inner packet for fragments
		default:
			// Other fragmented protocols - not tunneled
			info.IsTunneled = false
			return nil
		}
	}

	switch ipv4.Protocol {
	case layers.IPProtocolIPv4:
		// IPv4-in-IPv4 tunnel
		info.IsTunneled = true
		info.TunnelType = "ip4in4"
		return p.parseInnerPacket(packet, info, layers.LayerTypeIPv4)

	case layers.IPProtocolIPv6:
		// IPv6-in-IPv4 tunnel
		info.IsTunneled = true
		info.TunnelType = "ip6in4"
		return p.parseInnerPacket(packet, info, layers.LayerTypeIPv6)

	case layers.IPProtocolGRE:
		// GRE tunnel
		info.IsTunneled = true
		info.TunnelType = "gre"
		return p.parseGRETunnel(packet, info)
	}

	return nil
}

// checkTunnelInIPv6 checks for tunneled packets in IPv6
func (p *PacketParser) checkTunnelInIPv6(packet gopacket.Packet, ipv6 *layers.IPv6, info *PacketInfo) error {
	switch ipv6.NextHeader {
	case layers.IPProtocolIPv4:
		// IPv4-in-IPv6 tunnel
		info.IsTunneled = true
		info.TunnelType = "ip4in6"
		return p.parseInnerPacket(packet, info, layers.LayerTypeIPv4)

	case layers.IPProtocolIPv6:
		// IPv6-in-IPv6 tunnel
		info.IsTunneled = true
		info.TunnelType = "ip6in6"
		return p.parseInnerPacket(packet, info, layers.LayerTypeIPv6)

	case layers.IPProtocolGRE:
		// GRE tunnel in IPv6
		info.IsTunneled = true
		info.TunnelType = "gre"
		return p.parseGRETunnel(packet, info)

	case layers.IPProtocolIPv6Fragment:
		// IPv6 fragmented packet - check what's inside the fragment
		if fragLayer := packet.Layer(layers.LayerTypeIPv6Fragment); fragLayer != nil {
			frag := fragLayer.(*layers.IPv6Fragment)
			switch frag.NextHeader {
			case layers.IPProtocolIPv4:
				// Fragmented IPv4-in-IPv6 tunnel
				info.IsTunneled = true
				info.TunnelType = "ip4in6"
				return nil // Don't parse inner packet for fragments
			case layers.IPProtocolIPv6:
				// Fragmented IPv6-in-IPv6 tunnel
				info.IsTunneled = true
				info.TunnelType = "ip6in6"
				return nil // Don't parse inner packet for fragments
			default:
				// Other fragmented protocols
				info.IsTunneled = false
				return nil
			}
		}
	}

	return nil
}

// parseInnerPacket parses the inner packet in a tunnel
func (p *PacketParser) parseInnerPacket(packet gopacket.Packet, info *PacketInfo, innerType gopacket.LayerType) error {
	// Find all layers
	packetLayers := packet.Layers()
	var innerStart int = -1

	// Find the inner IP layer (should be after the outer IP layer)
	outerIPFound := false
	for i, layer := range packetLayers {
		layerType := layer.LayerType()

		// Skip the outer IP layer
		if (layerType == layers.LayerTypeIPv4 || layerType == layers.LayerTypeIPv6) && !outerIPFound {
			outerIPFound = true
			continue
		}

		// Look for the inner IP layer
		if layerType == innerType && outerIPFound {
			innerStart = i
			break
		}
	}

	if innerStart == -1 {
		return fmt.Errorf("inner packet not found")
	}

	// Create inner packet info
	innerInfo := &PacketInfo{}

	// Parse inner IP layer
	innerLayer := packetLayers[innerStart]
	switch innerType {
	case layers.LayerTypeIPv4:
		if ipv4, ok := innerLayer.(*layers.IPv4); ok {
			innerInfo.IsIPv4 = true
			innerInfo.SrcIP = ipv4.SrcIP
			innerInfo.DstIP = ipv4.DstIP
			innerInfo.Protocol = ipv4.Protocol
		}
	case layers.LayerTypeIPv6:
		if ipv6, ok := innerLayer.(*layers.IPv6); ok {
			innerInfo.IsIPv6 = true
			innerInfo.SrcIP = ipv6.SrcIP
			innerInfo.DstIP = ipv6.DstIP
			innerInfo.NextHeader = ipv6.NextHeader
		}
	}

	info.InnerPacket = innerInfo
	return nil
}

// parseGRETunnel parses GRE tunneled packets
func (p *PacketParser) parseGRETunnel(packet gopacket.Packet, info *PacketInfo) error {
	if greLayer := packet.Layer(layers.LayerTypeGRE); greLayer != nil {
		gre := greLayer.(*layers.GRE)

		// Determine inner packet type based on GRE protocol
		switch gre.Protocol {
		case layers.EthernetTypeIPv4:
			info.TunnelType = "gre-ip4"
			return p.parseInnerPacket(packet, info, layers.LayerTypeIPv4)
		case layers.EthernetTypeIPv6:
			info.TunnelType = "gre-ip6"
			return p.parseInnerPacket(packet, info, layers.LayerTypeIPv6)
		}
	}

	return nil
}

// parseTransportLayer parses transport layer (TCP/UDP)
func (p *PacketParser) parseTransportLayer(packet gopacket.Packet, info *PacketInfo) {
	if tcpLayer := packet.Layer(layers.LayerTypeTCP); tcpLayer != nil {
		tcp := tcpLayer.(*layers.TCP)
		info.SrcPort = uint16(tcp.SrcPort)
		info.DstPort = uint16(tcp.DstPort)
	} else if udpLayer := packet.Layer(layers.LayerTypeUDP); udpLayer != nil {
		udp := udpLayer.(*layers.UDP)
		info.SrcPort = uint16(udp.SrcPort)
		info.DstPort = uint16(udp.DstPort)
	}
}

// VerifyDecapsulation verifies that a packet has been properly decapsulated
func (p *PacketParser) VerifyDecapsulation(originalPacket, processedPacket *PacketInfo) error {
	if !originalPacket.IsTunneled {
		return fmt.Errorf("original packet is not tunneled")
	}

	if originalPacket.InnerPacket == nil {
		return fmt.Errorf("original packet has no inner packet")
	}

	inner := originalPacket.InnerPacket

	// Verify that the processed packet matches the inner packet
	if processedPacket.IsIPv4 != inner.IsIPv4 {
		return fmt.Errorf("IP version mismatch: expected IPv4=%v, got IPv4=%v",
			inner.IsIPv4, processedPacket.IsIPv4)
	}

	if processedPacket.IsIPv6 != inner.IsIPv6 {
		return fmt.Errorf("IP version mismatch: expected IPv6=%v, got IPv6=%v",
			inner.IsIPv6, processedPacket.IsIPv6)
	}

	if !processedPacket.SrcIP.Equal(inner.SrcIP) {
		return fmt.Errorf("source IP mismatch: expected %v, got %v",
			inner.SrcIP, processedPacket.SrcIP)
	}

	if !processedPacket.DstIP.Equal(inner.DstIP) {
		return fmt.Errorf("destination IP mismatch: expected %v, got %v",
			inner.DstIP, processedPacket.DstIP)
	}

	// Verify that tunnel headers are removed
	if processedPacket.IsTunneled {
		return fmt.Errorf("processed packet is still tunneled")
	}

	return nil
}

// VerifyNAT64Translation verifies NAT64 translation between IPv4 and IPv6
func (p *PacketParser) VerifyNAT64Translation(originalPacket, translatedPacket *PacketInfo, nat64Prefix string) error {
	// Parse NAT64 prefix
	_, prefixNet, err := net.ParseCIDR(nat64Prefix)
	if err != nil {
		return fmt.Errorf("invalid NAT64 prefix: %w", err)
	}

	// IPv4 to IPv6 translation
	if originalPacket.IsIPv4 && translatedPacket.IsIPv6 {
		// Verify that IPv6 destination is in NAT64 prefix
		if !prefixNet.Contains(translatedPacket.DstIP) {
			return fmt.Errorf("translated IPv6 address %v is not in NAT64 prefix %v",
				translatedPacket.DstIP, nat64Prefix)
		}

		// Extract embedded IPv4 address from IPv6
		ipv6Bytes := translatedPacket.DstIP.To16()
		if ipv6Bytes == nil {
			return fmt.Errorf("invalid IPv6 address")
		}

		// For 64:ff9b::/96 prefix, IPv4 is embedded in the last 4 bytes
		embeddedIPv4 := net.IP(ipv6Bytes[12:16])
		if !embeddedIPv4.Equal(originalPacket.DstIP) {
			return fmt.Errorf("embedded IPv4 address %v does not match original %v",
				embeddedIPv4, originalPacket.DstIP)
		}
	}

	// IPv6 to IPv4 translation
	if originalPacket.IsIPv6 && translatedPacket.IsIPv4 {
		// Verify that original IPv6 source was in NAT64 prefix
		if !prefixNet.Contains(originalPacket.SrcIP) {
			return fmt.Errorf("original IPv6 address %v is not in NAT64 prefix %v",
				originalPacket.SrcIP, nat64Prefix)
		}

		// Extract embedded IPv4 address
		ipv6Bytes := originalPacket.SrcIP.To16()
		if ipv6Bytes == nil {
			return fmt.Errorf("invalid IPv6 address")
		}

		embeddedIPv4 := net.IP(ipv6Bytes[12:16])
		if !embeddedIPv4.Equal(translatedPacket.SrcIP) {
			return fmt.Errorf("translated IPv4 address %v does not match embedded %v",
				translatedPacket.SrcIP, embeddedIPv4)
		}
	}

	return nil
}

// String returns a string representation of packet info
func (info *PacketInfo) String() string {
	result := fmt.Sprintf("Packet: %s -> %s", info.SrcIP, info.DstIP)

	if info.IsIPv4 {
		result += " (IPv4"
		if info.Protocol != 0 {
			result += fmt.Sprintf(", proto=%d", info.Protocol)
		}
		result += ")"
	}

	if info.IsIPv6 {
		result += " (IPv6"
		if info.NextHeader != 0 {
			result += fmt.Sprintf(", next=%d", info.NextHeader)
		}
		result += ")"
	}

	if info.SrcPort != 0 || info.DstPort != 0 {
		result += fmt.Sprintf(", ports=%d->%d", info.SrcPort, info.DstPort)
	}

	if info.IsTunneled {
		result += fmt.Sprintf(", tunnel=%s", info.TunnelType)
		if info.InnerPacket != nil {
			result += fmt.Sprintf(", inner=%s->%s",
				info.InnerPacket.SrcIP, info.InnerPacket.DstIP)
		}
	}

	return result
}
