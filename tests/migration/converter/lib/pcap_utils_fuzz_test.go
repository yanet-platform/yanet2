package lib

import (
	"net"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// FuzzConvertLayerToIR ensures convertLayerToIR is resilient to arbitrary inputs.
// It validates that the function doesn't panic and produces reasonable output
// for well-formed packets while handling malformed input gracefully.
func FuzzConvertLayerToIR(f *testing.F) {
	// Add comprehensive seed corpus covering major protocol combinations
	addSeedPackets(f)

	f.Fuzz(func(t *testing.T, data []byte) {
		// Parse packet with gopacket
		pkt := gopacket.NewPacket(data, layers.LayerTypeEthernet, gopacket.Default)
		pa := NewPcapAnalyzer(false)

		// Test with different CodegenOpts
		testOpts := []CodegenOpts{
			{}, // default
			{IsExpect: true, UseFrameworkMACs: true},
			{StripVLAN: true},
			{UseFrameworkMACs: true},
		}

		for _, opts := range testOpts {
			for _, layer := range pkt.Layers() {
				// Skip DecodeFailure layers as they're expected to be skipped
				if layer.LayerType() == gopacket.LayerTypeDecodeFailure {
					continue
				}

				// Convert layer to IR - should never panic
				irLayers, err := pa.convertLayerToIR(layer, opts)

				// Most errors should be nil, but some malformed packets might return errors
				// The key requirement is no panics
				_ = err

				// Validate output structure for non-nil results
				validateIRLayers(t, layer, irLayers, opts)
			}
		}
	})
}

// addSeedPackets adds realistic packet examples as fuzzing seeds
func addSeedPackets(f *testing.F) {
	// IPv4 TCP packet
	f.Add(createIPv4TCPPacket())

	// IPv4 UDP packet
	f.Add(createIPv4UDPPacket())

	// IPv4 ICMP echo request
	f.Add(createIPv4ICMPPacket())

	// IPv6 TCP packet
	f.Add(createIPv6TCPPacket())

	// IPv6 UDP packet
	f.Add(createIPv6UDPPacket())

	// IPv6 ICMPv6 echo request
	f.Add(createIPv6ICMPPacket())

	// VLAN-tagged IPv4 packet
	f.Add(createVLANPacket())

	// GRE tunneled packet
	f.Add(createGREPacket())

	// MPLS packet
	f.Add(createMPLSPacket())

	// ARP packet
	f.Add(createARPPacket())

	// Truncated packet (error case)
	f.Add([]byte{0x00, 0x11, 0x22, 0x33, 0x44})

	// Malformed IPv4 header
	f.Add(createMalformedIPv4Packet())
}

// validateIRLayers performs basic validation on converted IR layers
func validateIRLayers(t *testing.T, original gopacket.Layer, irLayers []*IRLayer, opts CodegenOpts) {
	// Unknown layer types can return nil, which is valid
	originalType := original.LayerType()

	// Known layer types that should produce output
	knownLayers := map[gopacket.LayerType]string{
		layers.LayerTypeEthernet:                    "Ether",
		layers.LayerTypeDot1Q:                       "Dot1Q",
		layers.LayerTypeIPv4:                        "IP",
		layers.LayerTypeIPv6:                        "IPv6",
		layers.LayerTypeTCP:                         "TCP",
		layers.LayerTypeUDP:                         "UDP",
		layers.LayerTypeICMPv4:                      "ICMP",
		layers.LayerTypeICMPv6:                      "ICMPv6",
		layers.LayerTypeARP:                         "ARP",
		layers.LayerTypeGRE:                         "GRE",
		layers.LayerTypeMPLS:                        "MPLS",
		layers.LayerTypeIPSecESP:                    "IPSecESP",
		layers.LayerTypeIPSecAH:                     "IPSecAH",
		layers.LayerTypeIPv6Fragment:                "IPv6ExtHdrFragment",
		layers.LayerTypeIPv6Destination:             "IPv6ExtHdrDestOpts",
		layers.LayerTypeIPv6HopByHop:                "IPv6ExtHdrHopByHop",
		layers.LayerTypeIPv6Routing:                 "IPv6ExtHdrRouting",
		layers.LayerTypeICMPv6NeighborSolicitation:  "ICMPv6ND_NS",
		layers.LayerTypeICMPv6NeighborAdvertisement: "ICMPv6ND_NA",
		layers.LayerTypeICMPv6RouterAdvertisement:   "ICMPv6ND_RA",
		layers.LayerTypeICMPv6RouterSolicitation:    "ICMPv6ND_RS",
	}

	// Payload layers should produce Raw
	if originalType == gopacket.LayerTypePayload {
		if len(irLayers) > 0 && irLayers[0] != nil {
			if irLayers[0].Type != "Raw" {
				t.Logf("Payload layer produced type %s instead of Raw", irLayers[0].Type)
			}
		}
		return
	}

	// For known layer types, validate basic structure
	if _, isKnown := knownLayers[originalType]; isKnown {
		if len(irLayers) == 0 {
			// ICMPv6Echo is handled inline, so nil is valid
			if originalType != layers.LayerTypeICMPv6Echo {
				t.Logf("Known layer type %s produced no IR layers", originalType)
			}
			return
		}

		// Check first layer (some conversions produce multiple layers like ICMPv6)
		if irLayers[0] != nil {
			// Validate Type field is set
			if irLayers[0].Type == "" {
				t.Errorf("IRLayer has empty Type field for %s", originalType)
			}

			// Validate Params map is not nil
			if irLayers[0].Params == nil {
				t.Errorf("IRLayer has nil Params for %s", originalType)
			}

			// Perform protocol-specific validation
			validateProtocolSpecific(t, originalType, irLayers[0], original, opts)
		}
	}
}

// validateProtocolSpecific performs protocol-specific field validation
func validateProtocolSpecific(t *testing.T, layerType gopacket.LayerType, ir *IRLayer, original gopacket.Layer, opts CodegenOpts) {
	switch layerType {
	case layers.LayerTypeEthernet:
		// Ethernet should have src and dst MACs
		if _, ok := ir.Params["src"]; !ok {
			t.Logf("Ethernet IR missing 'src' MAC")
		}
		if _, ok := ir.Params["dst"]; !ok {
			t.Logf("Ethernet IR missing 'dst' MAC")
		}

	case layers.LayerTypeDot1Q:
		// VLAN should have vlan ID
		if _, ok := ir.Params["vlan"]; !ok {
			t.Logf("Dot1Q IR missing 'vlan' field")
		}

	case layers.LayerTypeIPv4:
		// IPv4 should have src, dst, and proto
		if _, ok := ir.Params["src"]; !ok {
			t.Logf("IPv4 IR missing 'src' field")
		}
		if _, ok := ir.Params["dst"]; !ok {
			t.Logf("IPv4 IR missing 'dst' field")
		}

	case layers.LayerTypeIPv6:
		// IPv6 should have src, dst
		if _, ok := ir.Params["src"]; !ok {
			t.Logf("IPv6 IR missing 'src' field")
		}
		if _, ok := ir.Params["dst"]; !ok {
			t.Logf("IPv6 IR missing 'dst' field")
		}

	case layers.LayerTypeTCP:
		// TCP should have sport and dport
		if _, ok := ir.Params["sport"]; !ok {
			t.Logf("TCP IR missing 'sport' field")
		}
		if _, ok := ir.Params["dport"]; !ok {
			t.Logf("TCP IR missing 'dport' field")
		}

	case layers.LayerTypeUDP:
		// UDP should have sport and dport
		if _, ok := ir.Params["sport"]; !ok {
			t.Logf("UDP IR missing 'sport' field")
		}
		if _, ok := ir.Params["dport"]; !ok {
			t.Logf("UDP IR missing 'dport' field")
		}

	case layers.LayerTypeICMPv4:
		// ICMP should have type
		if _, ok := ir.Params["type"]; !ok {
			t.Logf("ICMPv4 IR missing 'type' field")
		}

	case layers.LayerTypeICMPv6:
		// ICMPv6 should have type
		if _, ok := ir.Params["type"]; !ok {
			t.Logf("ICMPv6 IR missing 'type' field")
		}

	case layers.LayerTypeARP:
		// ARP should have operation type
		if _, ok := ir.Params["operation"]; !ok {
			t.Logf("ARP IR missing 'operation' field")
		}
	}
}

// Helper functions to create seed packets

func createIPv4TCPPacket() []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1},
		DstMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5},
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.IP{192, 168, 1, 10},
		DstIP:    net.IP{10, 0, 0, 1},
	}

	tcp := &layers.TCP{
		SrcPort: 12345,
		DstPort: 80,
		Seq:     1000,
		Ack:     2000,
		Window:  65535,
		SYN:     true,
	}
	tcp.SetNetworkLayerForChecksum(ip)

	payload := gopacket.Payload([]byte("test payload"))

	gopacket.SerializeLayers(buf, opts, eth, ip, tcp, payload)
	return buf.Bytes()
}

func createIPv4UDPPacket() []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1},
		DstMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5},
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.IP{192, 168, 1, 10},
		DstIP:    net.IP{10, 0, 0, 1},
	}

	udp := &layers.UDP{
		SrcPort: 12345,
		DstPort: 53,
	}
	udp.SetNetworkLayerForChecksum(ip)

	payload := gopacket.Payload([]byte("DNS query"))

	gopacket.SerializeLayers(buf, opts, eth, ip, udp, payload)
	return buf.Bytes()
}

func createIPv4ICMPPacket() []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1},
		DstMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5},
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.IP{192, 168, 1, 10},
		DstIP:    net.IP{10, 0, 0, 1},
	}

	icmp := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       1,
		Seq:      1,
	}

	payload := gopacket.Payload([]byte("ping"))

	gopacket.SerializeLayers(buf, opts, eth, ip, icmp, payload)
	return buf.Bytes()
}

func createIPv6TCPPacket() []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1},
		DstMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5},
		EthernetType: layers.EthernetTypeIPv6,
	}

	ip := &layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolTCP,
		SrcIP:      net.ParseIP("fe80::1"),
		DstIP:      net.ParseIP("fe80::2"),
	}

	tcp := &layers.TCP{
		SrcPort: 12345,
		DstPort: 443,
		Seq:     1000,
		Ack:     2000,
		Window:  65535,
		SYN:     true,
	}
	tcp.SetNetworkLayerForChecksum(ip)

	payload := gopacket.Payload([]byte("test payload"))

	gopacket.SerializeLayers(buf, opts, eth, ip, tcp, payload)
	return buf.Bytes()
}

func createIPv6UDPPacket() []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1},
		DstMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5},
		EthernetType: layers.EthernetTypeIPv6,
	}

	ip := &layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolUDP,
		SrcIP:      net.ParseIP("fe80::1"),
		DstIP:      net.ParseIP("fe80::2"),
	}

	udp := &layers.UDP{
		SrcPort: 12345,
		DstPort: 53,
	}
	udp.SetNetworkLayerForChecksum(ip)

	payload := gopacket.Payload([]byte("DNS query"))

	gopacket.SerializeLayers(buf, opts, eth, ip, udp, payload)
	return buf.Bytes()
}

func createIPv6ICMPPacket() []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1},
		DstMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5},
		EthernetType: layers.EthernetTypeIPv6,
	}

	ip := &layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolICMPv6,
		SrcIP:      net.ParseIP("fe80::1"),
		DstIP:      net.ParseIP("fe80::2"),
	}

	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	icmp.SetNetworkLayerForChecksum(ip)

	payload := gopacket.Payload([]byte("ping6"))

	gopacket.SerializeLayers(buf, opts, eth, ip, icmp, payload)
	return buf.Bytes()
}

func createVLANPacket() []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1},
		DstMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5},
		EthernetType: layers.EthernetTypeDot1Q,
	}

	vlan := &layers.Dot1Q{
		VLANIdentifier: 100,
		Type:           layers.EthernetTypeIPv4,
	}

	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.IP{192, 168, 1, 10},
		DstIP:    net.IP{10, 0, 0, 1},
	}

	udp := &layers.UDP{
		SrcPort: 12345,
		DstPort: 80,
	}
	udp.SetNetworkLayerForChecksum(ip)

	gopacket.SerializeLayers(buf, opts, eth, vlan, ip, udp)
	return buf.Bytes()
}

func createGREPacket() []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1},
		DstMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5},
		EthernetType: layers.EthernetTypeIPv4,
	}

	outerIP := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolGRE,
		SrcIP:    net.IP{192, 168, 1, 10},
		DstIP:    net.IP{10, 0, 0, 1},
	}

	gre := &layers.GRE{
		Protocol: layers.EthernetTypeIPv4,
	}

	innerIP := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      63,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.IP{172, 16, 0, 1},
		DstIP:    net.IP{172, 16, 0, 2},
	}

	tcp := &layers.TCP{
		SrcPort: 8080,
		DstPort: 80,
		Seq:     100,
		Window:  65535,
	}
	tcp.SetNetworkLayerForChecksum(innerIP)

	gopacket.SerializeLayers(buf, opts, eth, outerIP, gre, innerIP, tcp)
	return buf.Bytes()
}

func createMPLSPacket() []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1},
		DstMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5},
		EthernetType: layers.EthernetTypeMPLSUnicast,
	}

	mpls := &layers.MPLS{
		Label:        100,
		TrafficClass: 0,
		StackBottom:  true,
		TTL:          64,
	}

	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.IP{192, 168, 1, 10},
		DstIP:    net.IP{10, 0, 0, 1},
	}

	tcp := &layers.TCP{
		SrcPort: 12345,
		DstPort: 80,
		Window:  65535,
	}
	tcp.SetNetworkLayerForChecksum(ip)

	gopacket.SerializeLayers(buf, opts, eth, mpls, ip, tcp)
	return buf.Bytes()
}

func createARPPacket() []byte {
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1},
		DstMAC:       net.HardwareAddr{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		EthernetType: layers.EthernetTypeARP,
	}

	arp := &layers.ARP{
		AddrType:          layers.LinkTypeEthernet,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         layers.ARPRequest,
		SourceHwAddress:   []byte{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1},
		SourceProtAddress: []byte{192, 168, 1, 10},
		DstHwAddress:      []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		DstProtAddress:    []byte{192, 168, 1, 1},
	}

	gopacket.SerializeLayers(buf, opts, eth, arp)
	return buf.Bytes()
}

func createMalformedIPv4Packet() []byte {
	// Start with a valid packet, then corrupt it
	validPacket := createIPv4TCPPacket()

	// Corrupt the IP header checksum (bytes 24-25 in the Ethernet frame)
	if len(validPacket) > 25 {
		validPacket[24] = 0xFF
		validPacket[25] = 0xFF
	}

	return validPacket
}
