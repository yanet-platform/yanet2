package internal

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcap"
)

// ScapyCodegen generates Go code from Scapy packet definitions
type ScapyCodegen struct {
	stripVLAN bool
	isExpect  bool
}

// NewScapyCodegen creates a new Scapy code generator
func NewScapyCodegen(stripVLAN bool, isExpect bool) *ScapyCodegen {
	return &ScapyCodegen{
		stripVLAN: stripVLAN,
		isExpect:  isExpect,
	}
}

// GeneratePacketFunction generates a Go function that creates packets from Scapy definitions
func (sg *ScapyCodegen) GeneratePacketFunction(packets []ScapyPacketDef, functionName string) string {
	var code strings.Builder

	code.WriteString(fmt.Sprintf("// %s creates packets from Scapy gen.py definitions\n", functionName))
	code.WriteString(fmt.Sprintf("func %s(t *testing.T) []gopacket.Packet {\n", functionName))
	code.WriteString("\tvar packets []gopacket.Packet\n\n")

	for idx, packet := range packets {
		code.WriteString(fmt.Sprintf("\t// Packet %d from gen.py\n", idx))
		code.WriteString(fmt.Sprintf("\t// Scapy: %s\n", sg.cleanScapyCode(packet.RawCode)))
		code.WriteString("\t{\n")

		if packet.IsFragmented {
			// For fragmented packets, use raw PCAP reading
			code.WriteString("\t\t// TODO: Fragmented packet - needs special handling\n")
			code.WriteString("\t\t// This packet uses fragment6() in Scapy\n")
		} else {
			code.WriteString(sg.generatePacketLayers(packet))
		}

		code.WriteString("\t}\n\n")
	}

	code.WriteString("\treturn packets\n")
	code.WriteString("}\n")

	return code.String()
}

// generatePacketLayers generates Go code for packet layers
func (sg *ScapyCodegen) generatePacketLayers(packet ScapyPacketDef) string {
	var code strings.Builder

	code.WriteString("\t\tvar layersToSerialize []gopacket.SerializableLayer\n\n")

	for _, layer := range packet.Layers {
		switch layer.Name {
		case "Ether":
			code.WriteString(sg.generateEtherLayer(layer))
		case "Dot1Q":
			if !sg.stripVLAN {
				code.WriteString(sg.generateDot1QLayer(layer))
			} else {
				code.WriteString("\t\t// VLAN layer stripped (StripVLAN enabled)\n\n")
			}
		case "IP":
			code.WriteString(sg.generateIPv4Layer(layer))
		case "IPv6":
			code.WriteString(sg.generateIPv6Layer(layer))
		case "TCP":
			code.WriteString(sg.generateTCPLayer(layer))
		case "UDP":
			code.WriteString(sg.generateUDPLayer(layer))
		case "ICMPv6EchoRequest":
			code.WriteString(sg.generateICMPv6EchoRequestLayer(layer))
		case "ICMPv6EchoReply":
			code.WriteString(sg.generateICMPv6EchoReplyLayer(layer))
		case "IPv6ExtHdrFragment":
			code.WriteString(sg.generateIPv6FragmentLayer(layer))
		case "IPv6ExtHdrDestOpt":
			code.WriteString(sg.generateIPv6DestOptLayer(layer))
		default:
			code.WriteString(fmt.Sprintf("\t\t// TODO: Unsupported layer %s\n", layer.Name))
		}
	}

	code.WriteString(`
		// Serialize layers
		buf := gopacket.NewSerializeBuffer()
		serializeOpts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
		err := gopacket.SerializeLayers(buf, serializeOpts, layersToSerialize...)
		require.NoError(t, err, "Failed to serialize packet")
		
		pkt := gopacket.NewPacket(buf.Bytes(), layers.LayerTypeEthernet, gopacket.Default)
		require.Empty(t, pkt.ErrorLayer())
		packets = append(packets, pkt)
`)

	return code.String()
}

// generateEtherLayer generates Ethernet layer code
func (sg *ScapyCodegen) generateEtherLayer(layer ScapyLayer) string {
	// Use framework MACs
	srcMAC := "0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1" // client
	dstMAC := "0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5" // yanet

	if sg.isExpect {
		// Swap MACs for expect packets
		srcMAC, dstMAC = dstMAC, srcMAC
	}

	etherType := "layers.EthernetTypeIPv4"
	if sg.stripVLAN {
		// Need to determine next layer type
		etherType = "layers.EthernetTypeIPv4" // default, will be fixed by caller
	} else if _, hasVLAN := layer.Params["vlan"]; hasVLAN {
		etherType = "layers.EthernetTypeDot1Q"
	}

	return fmt.Sprintf(`		ethLayer := &layers.Ethernet{
			SrcMAC:       net.HardwareAddr{%s},
			DstMAC:       net.HardwareAddr{%s},
			EthernetType: %s,
		}
		layersToSerialize = append(layersToSerialize, ethLayer)

`, srcMAC, dstMAC, etherType)
}

// generateDot1QLayer generates VLAN layer code
func (sg *ScapyCodegen) generateDot1QLayer(layer ScapyLayer) string {
	vlan := layer.Params["vlan"]
	if vlan == "" {
		vlan = "0"
	}

	return fmt.Sprintf(`		dot1qLayer := &layers.Dot1Q{
			VLANIdentifier: %s,
			Type:           layers.EthernetTypeIPv4, // Will be set based on next layer
		}
		layersToSerialize = append(layersToSerialize, dot1qLayer)

`, vlan)
}

// generateIPv4Layer generates IPv4 layer code
func (sg *ScapyCodegen) generateIPv4Layer(layer ScapyLayer) string {
	src := sg.adaptIPv4Address(layer.Params["src"])
	dst := sg.adaptIPv4Address(layer.Params["dst"])
	ttl := layer.Params["ttl"]
	if ttl == "" {
		ttl = "64"
	}
	tos := layer.Params["tos"]
	if tos == "" {
		tos = "0"
	}
	proto := layer.Params["proto"]
	if proto == "" {
		proto = "layers.IPProtocolTCP" // default
	} else {
		proto = fmt.Sprintf("layers.IPProtocol(%s)", proto)
	}

	length := layer.Params["len"]
	lengthComment := ""
	if length != "" {
		lengthComment = fmt.Sprintf(" // Malformed: len=%s", length)
	}

	return fmt.Sprintf(`		ipv4Layer := &layers.IPv4{
			Version:    4,
			IHL:        5,
			TOS:        %s,
			TTL:        %s,
			Protocol:   %s,
			SrcIP:      net.ParseIP(%q),
			DstIP:      net.ParseIP(%q),
		}%s
		layersToSerialize = append(layersToSerialize, ipv4Layer)

`, tos, ttl, proto, src, dst, lengthComment)
}

// generateIPv6Layer generates IPv6 layer code
func (sg *ScapyCodegen) generateIPv6Layer(layer ScapyLayer) string {
	src := layer.Params["src"]
	dst := layer.Params["dst"]
	hlim := layer.Params["hlim"]
	if hlim == "" {
		hlim = "64"
	}
	tc := layer.Params["tc"]
	if tc == "" {
		tc = "0"
	}
	fl := layer.Params["fl"]
	if fl == "" {
		fl = "0"
	}
	nh := layer.Params["nh"]
	nextHeader := "layers.IPProtocolTCP" // default
	if nh != "" {
		nextHeader = fmt.Sprintf("layers.IPProtocol(%s)", nh)
	}

	return fmt.Sprintf(`		ipv6Layer := &layers.IPv6{
			Version:      6,
			TrafficClass: %s,
			FlowLabel:    %s,
			HopLimit:     %s,
			NextHeader:   %s,
			SrcIP:        net.ParseIP(%q),
			DstIP:        net.ParseIP(%q),
		}
		layersToSerialize = append(layersToSerialize, ipv6Layer)

`, tc, fl, hlim, nextHeader, src, dst)
}

// generateTCPLayer generates TCP layer code
func (sg *ScapyCodegen) generateTCPLayer(layer ScapyLayer) string {
	sport := layer.Params["sport"]
	dport := layer.Params["dport"]
	if sport == "" {
		sport = "0"
	}
	if dport == "" {
		dport = "0"
	}

	return fmt.Sprintf(`		tcpLayer := &layers.TCP{
			SrcPort:    layers.TCPPort(%s),
			DstPort:    layers.TCPPort(%s),
			DataOffset: 5,
		}
		// Set network layer for checksum
		for i := len(layersToSerialize) - 1; i >= 0; i-- {
			if nl, ok := layersToSerialize[i].(gopacket.NetworkLayer); ok {
				tcpLayer.SetNetworkLayerForChecksum(nl)
				break
			}
		}
		layersToSerialize = append(layersToSerialize, tcpLayer)

`, sport, dport)
}

// generateUDPLayer generates UDP layer code
func (sg *ScapyCodegen) generateUDPLayer(layer ScapyLayer) string {
	sport := layer.Params["sport"]
	dport := layer.Params["dport"]
	if sport == "" {
		sport = "0"
	}
	if dport == "" {
		dport = "0"
	}

	return fmt.Sprintf(`		udpLayer := &layers.UDP{
			SrcPort: layers.UDPPort(%s),
			DstPort: layers.UDPPort(%s),
		}
		// Set network layer for checksum
		for i := len(layersToSerialize) - 1; i >= 0; i-- {
			if nl, ok := layersToSerialize[i].(gopacket.NetworkLayer); ok {
				udpLayer.SetNetworkLayerForChecksum(nl)
				break
			}
		}
		layersToSerialize = append(layersToSerialize, udpLayer)

`, sport, dport)
}

// generateICMPv6EchoRequestLayer generates ICMPv6 Echo Request layer code
func (sg *ScapyCodegen) generateICMPv6EchoRequestLayer(layer ScapyLayer) string {
	id := layer.Params["id"]
	seq := layer.Params["seq"]
	if id == "" {
		id = "0"
	}
	if seq == "" {
		seq = "0"
	}

	return fmt.Sprintf(`		icmpv6Layer := &layers.ICMPv6{
			TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
		}
		// Set network layer for checksum
		for i := len(layersToSerialize) - 1; i >= 0; i-- {
			if nl, ok := layersToSerialize[i].(gopacket.NetworkLayer); ok {
				icmpv6Layer.SetNetworkLayerForChecksum(nl)
				break
			}
		}
		layersToSerialize = append(layersToSerialize, icmpv6Layer)
		
		icmpv6EchoLayer := &layers.ICMPv6Echo{
			Identifier: %s,
			SeqNumber:  %s,
		}
		layersToSerialize = append(layersToSerialize, icmpv6EchoLayer)

`, id, seq)
}

// generateICMPv6EchoReplyLayer generates ICMPv6 Echo Reply layer code
func (sg *ScapyCodegen) generateICMPv6EchoReplyLayer(layer ScapyLayer) string {
	id := layer.Params["id"]
	seq := layer.Params["seq"]
	if id == "" {
		id = "0"
	}
	if seq == "" {
		seq = "0"
	}

	return fmt.Sprintf(`		icmpv6Layer := &layers.ICMPv6{
			TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoReply, 0),
		}
		// Set network layer for checksum
		for i := len(layersToSerialize) - 1; i >= 0; i-- {
			if nl, ok := layersToSerialize[i].(gopacket.NetworkLayer); ok {
				icmpv6Layer.SetNetworkLayerForChecksum(nl)
				break
			}
		}
		layersToSerialize = append(layersToSerialize, icmpv6Layer)
		
		icmpv6EchoLayer := &layers.ICMPv6Echo{
			Identifier: %s,
			SeqNumber:  %s,
		}
		layersToSerialize = append(layersToSerialize, icmpv6EchoLayer)

`, id, seq)
}

// generateIPv6FragmentLayer generates IPv6 Fragment header code
func (sg *ScapyCodegen) generateIPv6FragmentLayer(layer ScapyLayer) string {
	id := layer.Params["id"]
	offset := layer.Params["offset"]
	m := layer.Params["m"]

	if id == "" {
		id = "0"
	}
	if offset == "" {
		offset = "0"
	}
	moreFragments := "true"
	if m == "0" {
		moreFragments = "false"
	}

	return fmt.Sprintf(`		fragLayer := &layers.IPv6Fragment{
			NextHeader:     layers.IPProtocolTCP, // Will be determined by next layer
			FragmentOffset: %s,
			MoreFragments:  %s,
			Identification: %s,
		}
		layersToSerialize = append(layersToSerialize, fragLayer)

`, offset, moreFragments, id)
}

// generateIPv6DestOptLayer generates IPv6 Destination Options header code
func (sg *ScapyCodegen) generateIPv6DestOptLayer(layer ScapyLayer) string {
	nh := layer.Params["nh"]
	nextHeader := "layers.IPProtocolTCP"
	if nh != "" {
		nextHeader = fmt.Sprintf("layers.IPProtocol(%s)", nh)
	}

	return fmt.Sprintf(`		// IPv6 Destination Options header
		// TODO: Implement IPv6ExtHdrDestOpt layer
		_ = %s // nextHeader placeholder

`, nextHeader)
}

// adaptIPv4Address adapts yanet1 test addresses to yanet2 infrastructure
func (sg *ScapyCodegen) adaptIPv4Address(addr string) string {
	if !sg.isExpect {
		// Only adapt in send packets
		switch addr {
		case "200.0.0.1":
			return "203.0.113.1"
		case "200.0.0.2":
			return "203.0.113.14"
		}
	}
	return addr
}

// cleanScapyCode removes newlines and extra spaces from Scapy code
func (sg *ScapyCodegen) cleanScapyCode(code string) string {
	code = strings.ReplaceAll(code, "\n", " ")
	code = strings.ReplaceAll(code, "\t", " ")
	// Remove multiple spaces
	for strings.Contains(code, "  ") {
		code = strings.ReplaceAll(code, "  ", " ")
	}
	return strings.TrimSpace(code)
}

// ValidatePacketsAgainstPcap validates that generated packets match original PCAP files
func (sg *ScapyCodegen) ValidatePacketsAgainstPcap(t *testing.T, pairs []ScapyPcapPair, genPyDir string) {
	for _, pair := range pairs {
		t.Run(fmt.Sprintf("Validate_%s", pair.SendFile), func(t *testing.T) {
			if len(pair.SendPackets) == 0 {
				return
			}

			sendPcapPath := genPyDir + "/" + pair.SendFile
			if err := sg.validatePacketSlice(pair.SendPackets, sendPcapPath, t); err != nil {
				t.Errorf("Send packet validation failed for %s: %v", pair.SendFile, err)
			}
		})

		t.Run(fmt.Sprintf("Validate_%s", pair.ExpectFile), func(t *testing.T) {
			if len(pair.ExpectPackets) == 0 {
				return
			}

			expectPcapPath := genPyDir + "/" + pair.ExpectFile
			if err := sg.validatePacketSlice(pair.ExpectPackets, expectPcapPath, t); err != nil {
				t.Errorf("Expect packet validation failed for %s: %v", pair.ExpectFile, err)
			}
		})
	}
}

// validatePacketSlice validates a slice of packets against a PCAP file
func (sg *ScapyCodegen) validatePacketSlice(packets []ScapyPacketDef, pcapPath string, t *testing.T) error {
	handle, err := pcap.OpenOffline(pcapPath)
	if err != nil {
		return fmt.Errorf("failed to open PCAP %s: %w", pcapPath, err)
	}
	defer handle.Close()

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	pcapPackets := make([]gopacket.Packet, 0)

	for packet := range packetSource.Packets() {
		pcapPackets = append(pcapPackets, packet)
	}

	if len(packets) != len(pcapPackets) {
		return fmt.Errorf("packet count mismatch: parsed %d, PCAP has %d", len(packets), len(pcapPackets))
	}

	for i, packetDef := range packets {
		if packetDef.IsFragmented {
			t.Logf("Skipping validation of fragmented packet %d (needs special handling)", i)
			continue
		}

		// Generate Go packet from definition
		goPacket, err := sg.generateSinglePacket(packetDef)
		if err != nil {
			return fmt.Errorf("failed to generate packet %d: %w", i, err)
		}

		pcapPacket := pcapPackets[i]

		// Compare packet data
		if !bytes.Equal(goPacket.Data(), pcapPacket.Data()) {
			return fmt.Errorf("packet %d data mismatch: generated %d bytes, PCAP has %d bytes",
				i, len(goPacket.Data()), len(pcapPacket.Data()))
		}

		// Compare layers if parsing succeeds
		goParsed := gopacket.NewPacket(goPacket.Data(), layers.LayerTypeEthernet, gopacket.Default)
		pcapParsed := pcapPacket

		if goParsed.ErrorLayer() != nil {
			t.Logf("Warning: Go packet %d has parse error: %v", i, goParsed.ErrorLayer().Error())
		}
		if pcapParsed.ErrorLayer() != nil {
			t.Logf("Warning: PCAP packet %d has parse error: %v", i, pcapParsed.ErrorLayer().Error())
		}
	}

	return nil
}

// generateSinglePacket generates a single packet from Scapy definition
func (sg *ScapyCodegen) generateSinglePacket(packetDef ScapyPacketDef) (gopacket.Packet, error) {
	if packetDef.IsFragmented {
		return nil, fmt.Errorf("fragmented packets not supported yet")
	}

	var layersToSerialize []gopacket.SerializableLayer

	// Generate layers from definition
	for _, layer := range packetDef.Layers {
		switch layer.Name {
		case "Ether":
			eth := sg.generateEtherLayerFromDef(layer)
			layersToSerialize = append(layersToSerialize, eth)
		case "Dot1Q":
			if !sg.stripVLAN {
				vlan := sg.generateDot1QLayerFromDef(layer)
				layersToSerialize = append(layersToSerialize, vlan)
			}
		case "IP":
			ip := sg.generateIPv4LayerFromDef(layer)
			layersToSerialize = append(layersToSerialize, ip)
		case "IPv6":
			ip6 := sg.generateIPv6LayerFromDef(layer)
			layersToSerialize = append(layersToSerialize, ip6)
		case "TCP":
			tcp := sg.generateTCPLayerFromDef(layer)
			layersToSerialize = append(layersToSerialize, tcp)
		case "UDP":
			udp := sg.generateUDPLayerFromDef(layer)
			layersToSerialize = append(layersToSerialize, udp)
		case "ICMPv6EchoRequest":
			// Add ICMPv6 layer first
			icmpLayer := &layers.ICMPv6{
				TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
			}
			layersToSerialize = append(layersToSerialize, icmpLayer)

			// Then add ICMPv6Echo layer
			echoLayer := sg.generateICMPv6EchoRequestLayerFromDef(layer)
			layersToSerialize = append(layersToSerialize, echoLayer)
		case "IPv6ExtHdrFragment":
			frag := sg.generateIPv6FragmentLayerFromDef(layer)
			layersToSerialize = append(layersToSerialize, frag)
		default:
			return nil, fmt.Errorf("unsupported layer: %s", layer.Name)
		}
	}

	// Serialize
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	err := gopacket.SerializeLayers(buf, opts, layersToSerialize...)
	if err != nil {
		return nil, err
	}

	return gopacket.NewPacket(buf.Bytes(), layers.LayerTypeEthernet, gopacket.Default), nil
}

// generateEtherLayerFromDef generates Ethernet layer from definition
func (sg *ScapyCodegen) generateEtherLayerFromDef(layer ScapyLayer) *layers.Ethernet {
	// Use framework MACs
	srcMAC := []byte{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1} // client
	dstMAC := []byte{0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5} // yanet

	if sg.isExpect {
		srcMAC, dstMAC = dstMAC, srcMAC
	}

	etherType := layers.EthernetTypeIPv4
	if sg.stripVLAN {
		etherType = layers.EthernetTypeIPv4
	} else if _, hasVLAN := layer.Params["vlan"]; hasVLAN {
		etherType = layers.EthernetTypeDot1Q
	}

	return &layers.Ethernet{
		SrcMAC:       srcMAC,
		DstMAC:       dstMAC,
		EthernetType: etherType,
	}
}

// generateDot1QLayerFromDef generates VLAN layer from definition
func (sg *ScapyCodegen) generateDot1QLayerFromDef(layer ScapyLayer) *layers.Dot1Q {
	vlanID := uint16(0)
	if vlan, exists := layer.Params["vlan"]; exists {
		if parsed, err := parseInt(vlan); err == nil {
			vlanID = uint16(parsed)
		}
	}

	return &layers.Dot1Q{
		VLANIdentifier: vlanID,
		Type:           layers.EthernetTypeIPv4,
	}
}

// generateIPv4LayerFromDef generates IPv4 layer from definition
func (sg *ScapyCodegen) generateIPv4LayerFromDef(layer ScapyLayer) *layers.IPv4 {
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		Protocol: layers.IPProtocolTCP,
	}

	if src, exists := layer.Params["src"]; exists {
		ip.SrcIP = parseIP(sg.adaptIPv4Address(src))
	}
	if dst, exists := layer.Params["dst"]; exists {
		ip.DstIP = parseIP(sg.adaptIPv4Address(dst))
	}
	if ttl, exists := layer.Params["ttl"]; exists {
		if parsed, err := parseInt(ttl); err == nil {
			ip.TTL = uint8(parsed)
		}
	}
	if tos, exists := layer.Params["tos"]; exists {
		if parsed, err := parseInt(tos); err == nil {
			ip.TOS = uint8(parsed)
		}
	}
	if proto, exists := layer.Params["proto"]; exists {
		if parsed, err := parseInt(proto); err == nil {
			ip.Protocol = layers.IPProtocol(parsed)
		}
	}

	return ip
}

// generateIPv6LayerFromDef generates IPv6 layer from definition
func (sg *ScapyCodegen) generateIPv6LayerFromDef(layer ScapyLayer) *layers.IPv6 {
	ip := &layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolTCP,
		HopLimit:   64,
	}

	if src, exists := layer.Params["src"]; exists {
		ip.SrcIP = parseIP(src)
	}
	if dst, exists := layer.Params["dst"]; exists {
		ip.DstIP = parseIP(dst)
	}
	if hlim, exists := layer.Params["hlim"]; exists {
		if parsed, err := parseInt(hlim); err == nil {
			ip.HopLimit = uint8(parsed)
		}
	}
	if tc, exists := layer.Params["tc"]; exists {
		if parsed, err := parseInt(tc); err == nil {
			ip.TrafficClass = uint8(parsed)
		}
	}
	if fl, exists := layer.Params["fl"]; exists {
		if parsed, err := parseInt(fl); err == nil {
			ip.FlowLabel = uint32(parsed)
		}
	}
	if nh, exists := layer.Params["nh"]; exists {
		if parsed, err := parseInt(nh); err == nil {
			ip.NextHeader = layers.IPProtocol(parsed)
		}
	}

	return ip
}

// generateTCPLayerFromDef generates TCP layer from definition
func (sg *ScapyCodegen) generateTCPLayerFromDef(layer ScapyLayer) *layers.TCP {
	tcp := &layers.TCP{}

	if sport, exists := layer.Params["sport"]; exists {
		if parsed, err := parseInt(sport); err == nil {
			tcp.SrcPort = layers.TCPPort(parsed)
		}
	}
	if dport, exists := layer.Params["dport"]; exists {
		if parsed, err := parseInt(dport); err == nil {
			tcp.DstPort = layers.TCPPort(parsed)
		}
	}

	// Set network layer for checksum (will be done by caller)
	tcp.DataOffset = 5

	return tcp
}

// generateUDPLayerFromDef generates UDP layer from definition
func (sg *ScapyCodegen) generateUDPLayerFromDef(layer ScapyLayer) *layers.UDP {
	udp := &layers.UDP{}

	if sport, exists := layer.Params["sport"]; exists {
		if parsed, err := parseInt(sport); err == nil {
			udp.SrcPort = layers.UDPPort(parsed)
		}
	}
	if dport, exists := layer.Params["dport"]; exists {
		if parsed, err := parseInt(dport); err == nil {
			udp.DstPort = layers.UDPPort(parsed)
		}
	}

	return udp
}

// generateICMPv6EchoRequestLayerFromDef generates ICMPv6 Echo Request from definition
func (sg *ScapyCodegen) generateICMPv6EchoRequestLayerFromDef(layer ScapyLayer) gopacket.SerializableLayer {
	// ICMPv6Echo layer only; ICMPv6 header is added elsewhere in builders
	echo := &layers.ICMPv6Echo{}

	if id, exists := layer.Params["id"]; exists {
		if parsed, err := parseInt(id); err == nil {
			echo.Identifier = uint16(parsed)
		}
	}
	if seq, exists := layer.Params["seq"]; exists {
		if parsed, err := parseInt(seq); err == nil {
			echo.SeqNumber = uint16(parsed)
		}
	}

	// Return as a slice since we need both layers
	return echo
}

// generateIPv6FragmentLayerFromDef generates IPv6 Fragment header from definition
func (sg *ScapyCodegen) generateIPv6FragmentLayerFromDef(layer ScapyLayer) *layers.IPv6Fragment {
	frag := &layers.IPv6Fragment{
		NextHeader: layers.IPProtocolTCP,
	}

	if id, exists := layer.Params["id"]; exists {
		if parsed, err := parseInt(id); err == nil {
			frag.Identification = uint32(parsed)
		}
	}
	if offset, exists := layer.Params["offset"]; exists {
		if parsed, err := parseInt(offset); err == nil {
			frag.FragmentOffset = uint16(parsed)
		}
	}
	if m, exists := layer.Params["m"]; exists {
		frag.MoreFragments = m != "0"
	}

	return frag
}

// Helper functions for parsing
func parseInt(s string) (int, error) {
	// Handle hex notation like 0x1234
	if strings.HasPrefix(s, "0x") {
		var val int
		_, err := fmt.Sscanf(s, "0x%x", &val)
		return val, err
	}
	var val int
	_, err := fmt.Sscanf(s, "%d", &val)
	return val, err
}

func parseIP(s string) net.IP {
	return net.ParseIP(s)
}
