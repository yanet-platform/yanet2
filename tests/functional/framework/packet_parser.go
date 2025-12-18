package framework

import (
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// PacketInfo contains comprehensive parsed information about a network packet,
// including all protocol layers from Ethernet to application data. It supports
// complex scenarios such as tunneled packets, fragmentation, and various
// encapsulation protocols commonly used in network testing.
//
// The structure provides detailed information about:
//   - Ethernet layer addressing (MAC addresses)
//   - IP layer information (IPv4/IPv6 addressing and protocols)
//   - Transport layer details (TCP/UDP port information)
//   - Tunnel detection and inner packet parsing
//   - Raw packet data and payload extraction
type PacketInfo struct {
	// Ethernet layer addressing information
	SrcMAC net.HardwareAddr // Source MAC address from Ethernet header
	DstMAC net.HardwareAddr // Destination MAC address from Ethernet header

	// IP layer information (supports both IPv4 and IPv6)
	IsIPv4     bool              // True if packet contains IPv4 header
	IsIPv6     bool              // True if packet contains IPv6 header
	SrcIP      net.IP            // Source IP address (IPv4 or IPv6)
	DstIP      net.IP            // Destination IP address (IPv4 or IPv6)
	Protocol   layers.IPProtocol // IPv4 protocol field
	NextHeader layers.IPProtocol // IPv6 next header field

	// Transport layer information
	SrcPort uint16 // Source port number (TCP/UDP)
	DstPort uint16 // Destination port number (TCP/UDP)

	// Tunnel detection and analysis
	IsTunneled  bool        // True if packet is tunneled/encapsulated
	TunnelType  string      // Tunnel type: "ip4in4", "ip6in4", "ip4in6", "ip6in6", "gre"
	InnerPacket *PacketInfo // Parsed information about the inner/encapsulated packet

	// Raw packet data
	RawData []byte // Complete raw packet bytes including all headers
	Payload []byte // Application layer payload data
}

// PacketParser provides comprehensive packet parsing and verification functionality
// for network testing scenarios. It supports complex packet analysis including
// tunnel detection, protocol verification, and NAT64 translation validation.
//
// The parser handles various networking protocols and encapsulation methods
// commonly encountered in YANET testing environments.
type PacketParser struct{}

// NewPacketParser creates a new packet parser instance ready for packet analysis.
// The parser is stateless and can be safely used concurrently across multiple
// goroutines for parallel packet processing.
//
// Returns:
//   - *PacketParser: A new packet parser instance
func NewPacketParser() *PacketParser {
	return &PacketParser{}
}

// ParsePacket parses raw packet data and extracts comprehensive protocol information
// into a structured PacketInfo object. The method handles various networking protocols
// and automatically detects tunneled/encapsulated packets.
//
// The parsing process includes:
//   - Ethernet frame validation and padding to minimum size
//   - Multi-layer protocol parsing (Ethernet, IP, Transport)
//   - Tunnel detection and inner packet analysis
//   - Payload extraction and error handling
//
// Parameters:
//   - data: Raw packet bytes to parse (minimum 60 bytes after padding)
//
// Returns:
//   - *PacketInfo: Structured packet information with all parsed layers
//   - error: An error if packet parsing fails or data is malformed
//
// Example:
//
//	parser := NewPacketParser()
//	info, err := parser.ParsePacket(rawPacketData)
//	if err != nil {
//	    log.Fatalf("Packet parsing failed: %v", err)
//	}
//	fmt.Printf("Parsed packet: %s", info.String())
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

// parseIPLayers parses both IPv4 and IPv6 protocol layers within a packet,
// including detection and analysis of tunneled/encapsulated packets. This method
// handles complex scenarios such as IP-in-IP tunnels and fragmented packets.
//
// The method processes:
//   - IPv4 header parsing with protocol identification
//   - IPv6 header parsing with next header analysis
//   - Tunnel detection for various encapsulation types
//   - Fragmentation handling for both IPv4 and IPv6
//
// Parameters:
//   - packet: Parsed gopacket.Packet containing protocol layers
//   - info: PacketInfo structure to populate with parsed data
//
// Returns:
//   - error: An error if IP layer parsing or tunnel detection fails
func (p *PacketParser) parseIPLayers(packet gopacket.Packet, info *PacketInfo) error {
	if networkLayer := packet.NetworkLayer(); networkLayer != nil {
		// Check for IPv4
		if networkLayer.LayerType() == layers.LayerTypeIPv4 {
			ipv4 := networkLayer.(*layers.IPv4)
			info.IsIPv4 = true
			info.SrcIP = ipv4.SrcIP
			info.DstIP = ipv4.DstIP
			info.Protocol = ipv4.Protocol

			// Check for tunneled packets
			if err := p.checkTunnelInIPv4(packet, ipv4, info); err != nil {
				return err
			}
		} else if networkLayer.LayerType() == layers.LayerTypeIPv6 { // Check for Ipv6
			ipv6 := networkLayer.(*layers.IPv6)
			info.IsIPv6 = true
			info.SrcIP = ipv6.SrcIP
			info.DstIP = ipv6.DstIP
			info.NextHeader = ipv6.NextHeader

			// Check for tunneled packets
			if err := p.checkTunnelInIPv6(packet, ipv6, info); err != nil {
				return err
			}
		}
	}

	return nil
}

// checkTunnelInIPv4 analyzes IPv4 packets for various tunnel encapsulation types
// including IP-in-IP tunnels and GRE encapsulation. The method handles both
// complete and fragmented tunneled packets appropriately.
//
// Supported tunnel types:
//   - IPv4-in-IPv4 (protocol 4)
//   - IPv6-in-IPv4 (protocol 41)
//   - GRE tunnels (protocol 47)
//   - Fragmented tunnel packets (detected but inner packet not parsed)
//
// Parameters:
//   - packet: Complete parsed packet with all layers
//   - ipv4: IPv4 layer containing potential tunnel information
//   - info: PacketInfo structure to update with tunnel details
//
// Returns:
//   - error: An error if tunnel parsing fails or inner packet is malformed
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

// checkTunnelInIPv6 analyzes IPv6 packets for various tunnel encapsulation types
// and IPv6-specific features such as extension headers and fragmentation. The method
// provides comprehensive tunnel detection for IPv6-based encapsulation scenarios.
//
// Supported tunnel types:
//   - IPv4-in-IPv6 (next header 4)
//   - IPv6-in-IPv6 (next header 41)
//   - GRE tunnels in IPv6 (next header 47)
//   - IPv6 fragmented packets with tunnel detection
//
// Parameters:
//   - packet: Complete parsed packet with all protocol layers
//   - ipv6: IPv6 layer containing potential tunnel information
//   - info: PacketInfo structure to update with tunnel details
//
// Returns:
//   - error: An error if tunnel parsing fails or inner packet analysis fails
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

// parseInnerPacket extracts and parses the encapsulated packet within a tunnel,
// creating a nested PacketInfo structure for the inner packet. This method handles
// the complex task of locating and parsing inner IP headers within various tunnel types.
//
// The parsing process includes:
//   - Locating the inner IP layer after the outer tunnel headers
//   - Creating a separate PacketInfo structure for the inner packet
//   - Parsing inner IP headers (IPv4 or IPv6) with full protocol information
//   - Linking the inner packet information to the outer packet structure
//
// Parameters:
//   - packet: Complete packet containing both outer and inner layers
//   - info: Outer packet info structure to link the inner packet to
//   - innerType: Expected layer type of the inner packet (IPv4 or IPv6)
//
// Returns:
//   - error: An error if inner packet cannot be found or parsed
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

// parseGRETunnel analyzes GRE (Generic Routing Encapsulation) tunneled packets
// and determines the type of inner packet based on the GRE protocol field.
// This method handles the GRE-specific parsing requirements and protocol detection.
//
// The method supports:
//   - GRE-encapsulated IPv4 packets (EtherType 0x0800)
//   - GRE-encapsulated IPv6 packets (EtherType 0x86DD)
//   - Automatic inner packet type detection and parsing
//
// Parameters:
//   - packet: Complete packet containing GRE layer and inner packet
//   - info: PacketInfo structure to update with GRE tunnel information
//
// Returns:
//   - error: An error if GRE layer is missing or inner packet parsing fails
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

// parseTransportLayer extracts transport layer information from TCP and UDP
// protocols, populating port numbers for connection identification. This method
// handles the most common transport protocols used in network testing.
//
// Supported transport protocols:
//   - TCP: Extracts source and destination port numbers
//   - UDP: Extracts source and destination port numbers
//   - Other protocols: Ignored (ports remain 0)
//
// Parameters:
//   - packet: Parsed packet containing transport layer information
//   - info: PacketInfo structure to populate with port information
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

// VerifyDecapsulation validates that a tunneled packet has been properly
// decapsulated by comparing the processed packet against the original inner packet.
// This verification method is essential for testing tunnel processing functionality.
//
// The verification process includes:
//   - Confirming the original packet was tunneled
//   - Validating inner packet information exists
//   - Comparing IP version consistency (IPv4/IPv6)
//   - Verifying source and destination IP address preservation
//   - Ensuring tunnel headers have been properly removed
//
// Parameters:
//   - originalPacket: The original tunneled packet before processing
//   - processedPacket: The packet after decapsulation processing
//
// Returns:
//   - error: An error if decapsulation verification fails or packets don't match
//
// Example:
//
//	err := parser.VerifyDecapsulation(tunneled, decapsulated)
//	if err != nil {
//	    log.Fatalf("Decapsulation verification failed: %v", err)
//	}
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

// VerifyNAT64Translation validates NAT64 protocol translation between IPv4 and IPv6
// packets, ensuring proper address mapping and prefix usage. This method is crucial
// for testing NAT64 functionality in dual-stack network environments.
//
// The verification process includes:
//   - IPv4-to-IPv6 translation validation with prefix checking
//   - IPv6-to-IPv4 translation validation with embedded address extraction
//   - NAT64 prefix compliance verification (typically 64:ff9b::/96)
//   - Embedded IPv4 address consistency checking
//
// Supported translation directions:
//   - IPv4 → IPv6: Verifies IPv6 address is in NAT64 prefix with embedded IPv4
//   - IPv6 → IPv4: Verifies original IPv6 was in prefix and extracts embedded IPv4
//
// Parameters:
//   - originalPacket: The packet before NAT64 translation
//   - translatedPacket: The packet after NAT64 translation
//   - nat64Prefix: NAT64 prefix in CIDR notation (e.g., "64:ff9b::/96")
//
// Returns:
//   - error: An error if NAT64 translation verification fails or addresses don't match
//
// Example:
//
//	err := parser.VerifyNAT64Translation(ipv4Pkt, ipv6Pkt, "64:ff9b::/96")
//	if err != nil {
//	    log.Fatalf("NAT64 translation verification failed: %v", err)
//	}
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

// String returns a human-readable string representation of the packet information,
// including all relevant protocol details, addressing information, and tunnel status.
// This method is useful for debugging, logging, and test result presentation.
//
// The string representation includes:
//   - Source and destination IP addresses
//   - IP version information (IPv4/IPv6) with protocol details
//   - Transport layer port information (if available)
//   - Tunnel information and inner packet details (if tunneled)
//
// Returns:
//   - string: Formatted packet information suitable for display and logging
//
// Example output:
//
//	"Packet: 192.168.1.1 -> 10.0.0.1 (IPv4, proto=4), tunnel=ip4in4, inner=172.16.1.1->172.16.1.2"
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

// GetTransportProtocol returns the transport layer protocol (TCP/UDP) from the packet.
// For tunneled packets, it returns the protocol from the inner packet.
// Returns the IPProtocol value and a boolean indicating if a valid transport protocol was found.
func (info *PacketInfo) GetTransportProtocol() (layers.IPProtocol, bool) {
	// For tunneled packets, get protocol from inner packet
	if info.IsTunneled && info.InnerPacket != nil {
		return info.InnerPacket.GetTransportProtocol()
	}

	// Get protocol based on IP version
	var proto layers.IPProtocol
	if info.IsIPv4 {
		proto = info.Protocol
	} else if info.IsIPv6 {
		proto = info.NextHeader
	} else {
		return 0, false
	}

	// Check if it's a valid transport protocol
	if proto == layers.IPProtocolTCP || proto == layers.IPProtocolUDP {
		return proto, true
	}

	return 0, false
}
