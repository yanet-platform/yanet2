package lib

import (
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcap"
)

// PcapAnalyzer analyzes pcap files and generates Go code for packet creation
type PcapAnalyzer struct {
	verbose bool
}

// NewPcapAnalyzer creates a new pcap file analyzer
func NewPcapAnalyzer(verbose bool) *PcapAnalyzer {
	return &PcapAnalyzer{
		verbose: verbose,
	}
}

// PacketInfo contains information about packet from pcap file
type PacketInfo struct {
	RawData      []byte
	EthernetType string
	SrcMAC       string
	DstMAC       string
	SrcIP        string
	DstIP        string
	Protocol     string
	SrcPort      uint16
	DstPort      uint16
	Payload      []byte
	PayloadSize  int
	IsIPv4       bool
	IsIPv6       bool
	IsTCP        bool
	IsUDP        bool
	IsICMP       bool
	VLANID       uint16
	HasVLAN      bool
	HasMPLS      bool
}

// AnalyzePcapFile analyzes pcap file and returns information about first packet
func (p *PcapAnalyzer) AnalyzePcapFile(filename string) (*PacketInfo, error) {
	handle, err := pcap.OpenOffline(filename)
	if err != nil {
		return nil, fmt.Errorf("error opening pcap file %s: %w", filename, err)
	}
	defer handle.Close()

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())

	// Read first packet
	for packet := range packetSource.Packets() {
		return p.analyzePacket(packet), nil
	}

	return nil, fmt.Errorf("pcap file %s contains no packets", filename)
}

// ReadAllPacketsFromFile reads all packets from pcap file
func (p *PcapAnalyzer) ReadAllPacketsFromFile(filename string) ([]*PacketInfo, error) {
	handle, err := pcap.OpenOffline(filename)
	if err != nil {
		return nil, fmt.Errorf("error opening pcap file %s: %w", filename, err)
	}
	defer handle.Close()

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	var packets []*PacketInfo

	for packet := range packetSource.Packets() {
		packets = append(packets, p.analyzePacket(packet))
	}

	// if len(packets) == 0 {
	// 	return nil, fmt.Errorf("pcap file %s contains no packets", filename)
	// }

	return packets, nil
}

// analyzePacket analyzes one packet
func (p *PcapAnalyzer) analyzePacket(packet gopacket.Packet) *PacketInfo {
	info := &PacketInfo{
		RawData: packet.Data(),
	}

	// Analyze Ethernet layer
	if ethLayer := packet.Layer(layers.LayerTypeEthernet); ethLayer != nil {
		eth, _ := ethLayer.(*layers.Ethernet)
		info.SrcMAC = eth.SrcMAC.String()
		info.DstMAC = eth.DstMAC.String()
		info.EthernetType = eth.EthernetType.String()
	}

	// Analyze VLAN layer
	if dot1QLayer := packet.Layer(layers.LayerTypeDot1Q); dot1QLayer != nil {
		dot1Q, _ := dot1QLayer.(*layers.Dot1Q)
		info.VLANID = dot1Q.VLANIdentifier
		info.HasVLAN = true
	}

	// Analyze MPLS layer
	if mplsLayer := packet.Layer(layers.LayerTypeMPLS); mplsLayer != nil {
		info.HasMPLS = true
	}

	// Analyze IP layers
	if ipv4Layer := packet.Layer(layers.LayerTypeIPv4); ipv4Layer != nil {
		ipv4, _ := ipv4Layer.(*layers.IPv4)
		info.SrcIP = ipv4.SrcIP.String()
		info.DstIP = ipv4.DstIP.String()
		info.Protocol = ipv4.Protocol.String()
		info.IsIPv4 = true
	}

	if ipv6Layer := packet.Layer(layers.LayerTypeIPv6); ipv6Layer != nil {
		ipv6, _ := ipv6Layer.(*layers.IPv6)
		info.SrcIP = ipv6.SrcIP.String()
		info.DstIP = ipv6.DstIP.String()
		info.Protocol = ipv6.NextHeader.String()
		info.IsIPv6 = true
	}

	// Analyze transport layers
	if tcpLayer := packet.Layer(layers.LayerTypeTCP); tcpLayer != nil {
		tcp, _ := tcpLayer.(*layers.TCP)
		info.SrcPort = uint16(tcp.SrcPort)
		info.DstPort = uint16(tcp.DstPort)
		info.IsTCP = true
	}

	if udpLayer := packet.Layer(layers.LayerTypeUDP); udpLayer != nil {
		udp, _ := udpLayer.(*layers.UDP)
		info.SrcPort = uint16(udp.SrcPort)
		info.DstPort = uint16(udp.DstPort)
		info.IsUDP = true
	}

	if icmpLayer := packet.Layer(layers.LayerTypeICMPv4); icmpLayer != nil {
		info.IsICMP = true
	}

	if icmpv6Layer := packet.Layer(layers.LayerTypeICMPv6); icmpv6Layer != nil {
		info.IsICMP = true
	}

	// Payload
	if app := packet.ApplicationLayer(); app != nil {
		info.Payload = app.Payload()
		info.PayloadSize = len(app.Payload())
	}

	return info
}

// CodegenOpts controls packet creation code generation
type CodegenOpts struct {
	StripVLAN        bool
	IsExpect         bool // Generate expected (response) packet with adapted TTL and swapped MACs
	UseFrameworkMACs bool // Force use of framework standard MACs (52:54:00:6b:ff:a1/a5)
}

// GeneratePacketCreationCode generates Go code for packet creation based on pcap analysis
func (p *PcapAnalyzer) GeneratePacketCreationCode(info *PacketInfo, functionName string) string {
	var code strings.Builder

	// %s creates packet based on pcap file analysis
	code.WriteString(fmt.Sprintf("// %s creates packet based on pcap file analysis\n", functionName))
	code.WriteString(fmt.Sprintf("func %s() []byte {\n", functionName))

	// Determine packet type and create corresponding code
	if info.IsIPv4 && info.IsTCP {
		code.WriteString(p.generateIPv4TCPPacket(info))
	} else if info.IsIPv4 && info.IsUDP {
		code.WriteString(p.generateIPv4UDPPacket(info))
	} else if info.IsIPv4 && info.IsICMP {
		code.WriteString(p.generateIPv4ICMPPacket(info))
	} else if info.IsIPv6 && info.IsTCP {
		code.WriteString(p.generateIPv6TCPPacket(info))
	} else if info.IsIPv6 && info.IsUDP {
		code.WriteString(p.generateIPv6UDPPacket(info))
	} else if info.IsIPv6 && info.IsICMP {
		code.WriteString(p.generateIPv6ICMPPacket(info))
	} else {
		code.WriteString(p.generateRawPacket(info))
	}

	code.WriteString("}\n")

	return code.String()
}

// ConvertPacketInfoToIR converts PacketInfo slice to IR JSON structure
func (p *PcapAnalyzer) ConvertPacketInfoToIR(packets []*PacketInfo, sendFile string, expectFile string, opts CodegenOpts) (*IRJSON, error) {
	var sendPackets []IRPacketDef
	var expectPackets []IRPacketDef

	targetList := &sendPackets
	if opts.IsExpect {
		targetList = &expectPackets
	}

	for _, info := range packets {
		pkt := gopacket.NewPacket(info.RawData, layers.LayerTypeEthernet, gopacket.Default)
		packetLayers := pkt.Layers()

		var irLayers []IRLayer
		for _, layer := range packetLayers {
			irLayer, err := p.convertLayerToIR(layer, opts)
			if err != nil {
				return nil, fmt.Errorf("failed to convert layer: %w", err)
			}
			if irLayer != nil {
				// Skip VLAN if StripVLAN is enabled
				if opts.StripVLAN && irLayer.Type == "Dot1Q" {
					continue
				}
				irLayers = append(irLayers, *irLayer)
			}
		}

		*targetList = append(*targetList, IRPacketDef{
			Layers:          irLayers,
			SpecialHandling: nil,
		})
	}

	pcapPair := IRPCAPPair{
		SendFile:      sendFile,
		ExpectFile:    expectFile,
		SendPackets:   sendPackets,
		ExpectPackets: expectPackets,
	}

	return &IRJSON{
		PCAPPairs:       []IRPCAPPair{pcapPair},
		HelperFunctions: []string{},
	}, nil
}

// convertLayerToIR converts a gopacket layer to IR representation
func (p *PcapAnalyzer) convertLayerToIR(layer gopacket.Layer, opts CodegenOpts) (*IRLayer, error) {
	switch l := layer.(type) {
	case *layers.Ethernet:
		return p.convertEthernetToIR(l, opts), nil
	case *layers.Dot1Q:
		return p.convertDot1QToIR(l), nil
	case *layers.IPv4:
		return p.convertIPv4ToIR(l, opts), nil
	case *layers.IPv6:
		return p.convertIPv6ToIR(l, opts), nil
	case *layers.TCP:
		return p.convertTCPToIR(l), nil
	case *layers.UDP:
		return p.convertUDPToIR(l), nil
	case *layers.ICMPv4:
		return p.convertICMPv4ToIR(l), nil
	case *layers.ICMPv6:
		return p.convertICMPv6ToIR(l), nil
	case *layers.ICMPv6Echo:
		return p.convertICMPv6EchoToIR(l), nil
	case *layers.IPv6Fragment:
		return p.convertIPv6FragmentToIR(l), nil
	case *layers.IPv6Destination:
		// Skip for now, not commonly used
		return nil, nil
	case gopacket.Payload:
		if len(l) > 0 {
			return p.convertPayloadToIR(l), nil
		}
		return nil, nil
	default:
		// Unknown layer type, skip
		return nil, nil
	}
}

// convertEthernetToIR converts Ethernet layer to IR
func (p *PcapAnalyzer) convertEthernetToIR(eth *layers.Ethernet, opts CodegenOpts) *IRLayer {
	params := make(map[string]interface{})

	// Handle MAC addresses based on options
	if opts.UseFrameworkMACs {
		// Use framework standard MACs
		if opts.IsExpect {
			// Swap: YANET sends back to client
			params["src"] = "52:54:00:6b:ff:a5" // YANET
			params["dst"] = "52:54:00:6b:ff:a1" // client
		} else {
			// client -> YANET
			params["src"] = "52:54:00:6b:ff:a1" // client
			params["dst"] = "52:54:00:6b:ff:a5" // YANET
		}
	} else {
		// Extract MACs from PCAP
		params["src"] = eth.SrcMAC.String()
		params["dst"] = eth.DstMAC.String()
	}

	return &IRLayer{
		Type:   "Ether",
		Params: params,
	}
}

// convertDot1QToIR converts VLAN layer to IR
func (p *PcapAnalyzer) convertDot1QToIR(vlan *layers.Dot1Q) *IRLayer {
	params := make(map[string]interface{})
	params["vlan"] = int(vlan.VLANIdentifier)

	return &IRLayer{
		Type:   "Dot1Q",
		Params: params,
	}
}

// convertIPv4ToIR converts IPv4 layer to IR
func (p *PcapAnalyzer) convertIPv4ToIR(ipv4 *layers.IPv4, opts CodegenOpts) *IRLayer {
	params := make(map[string]interface{})

	params["src"] = ipv4.SrcIP.String()
	params["dst"] = ipv4.DstIP.String()
	params["ttl"] = int(ipv4.TTL)

	if ipv4.TOS != 0 {
		params["tos"] = int(ipv4.TOS)
	}
	if ipv4.Id != 0 {
		params["id"] = int(ipv4.Id)
	}
	if ipv4.Protocol != 0 {
		params["proto"] = int(ipv4.Protocol)
	}

	return &IRLayer{
		Type:   "IP",
		Params: params,
	}
}

// convertIPv6ToIR converts IPv6 layer to IR
func (p *PcapAnalyzer) convertIPv6ToIR(ipv6 *layers.IPv6, opts CodegenOpts) *IRLayer {
	params := make(map[string]interface{})

	params["src"] = ipv6.SrcIP.String()
	params["dst"] = ipv6.DstIP.String()
	params["hlim"] = int(ipv6.HopLimit)

	if ipv6.TrafficClass != 0 {
		params["tc"] = int(ipv6.TrafficClass)
	}
	if ipv6.FlowLabel != 0 {
		params["fl"] = int(ipv6.FlowLabel)
	}
	if ipv6.NextHeader != 0 {
		params["nh"] = int(ipv6.NextHeader)
	}

	return &IRLayer{
		Type:   "IPv6",
		Params: params,
	}
}

// convertTCPToIR converts TCP layer to IR
func (p *PcapAnalyzer) convertTCPToIR(tcp *layers.TCP) *IRLayer {
	params := make(map[string]interface{})

	params["sport"] = int(tcp.SrcPort)
	params["dport"] = int(tcp.DstPort)

	if tcp.Seq != 0 {
		params["seq"] = int(tcp.Seq)
	}
	if tcp.Ack != 0 {
		params["ack"] = int(tcp.Ack)
	}

	// Build flags string
	var flags []string
	if tcp.FIN {
		flags = append(flags, "F")
	}
	if tcp.SYN {
		flags = append(flags, "S")
	}
	if tcp.RST {
		flags = append(flags, "R")
	}
	if tcp.PSH {
		flags = append(flags, "P")
	}
	if tcp.ACK {
		flags = append(flags, "A")
	}
	if tcp.URG {
		flags = append(flags, "U")
	}
	if tcp.ECE {
		flags = append(flags, "E")
	}
	if tcp.CWR {
		flags = append(flags, "C")
	}
	if len(flags) > 0 {
		params["flags"] = strings.Join(flags, "")
	}

	return &IRLayer{
		Type:   "TCP",
		Params: params,
	}
}

// convertUDPToIR converts UDP layer to IR
func (p *PcapAnalyzer) convertUDPToIR(udp *layers.UDP) *IRLayer {
	params := make(map[string]interface{})

	params["sport"] = int(udp.SrcPort)
	params["dport"] = int(udp.DstPort)

	return &IRLayer{
		Type:   "UDP",
		Params: params,
	}
}

// convertICMPv4ToIR converts ICMPv4 layer to IR
func (p *PcapAnalyzer) convertICMPv4ToIR(icmp *layers.ICMPv4) *IRLayer {
	params := make(map[string]interface{})

	typeCode := uint16(icmp.TypeCode)
	params["type"] = int(typeCode >> 8)
	params["code"] = int(typeCode & 0xff)

	if icmp.Id != 0 {
		params["id"] = int(icmp.Id)
	}
	if icmp.Seq != 0 {
		params["seq"] = int(icmp.Seq)
	}

	return &IRLayer{
		Type:   "ICMP",
		Params: params,
	}
}

// convertICMPv6ToIR converts ICMPv6 layer to IR
func (p *PcapAnalyzer) convertICMPv6ToIR(icmp *layers.ICMPv6) *IRLayer {
	params := make(map[string]interface{})

	typeCode := uint16(icmp.TypeCode)
	icmpType := int(typeCode >> 8)
	code := int(typeCode & 0xff)

	// Determine layer type based on ICMPv6 type
	var layerType string
	switch icmpType {
	case 128: // Echo Request
		layerType = "ICMPv6EchoRequest"
	case 129: // Echo Reply
		layerType = "ICMPv6EchoReply"
	case 1: // Destination Unreachable
		layerType = "ICMPv6DestUnreach"
		params["code"] = code
	default:
		layerType = "ICMPv6EchoRequest" // Default fallback
	}

	return &IRLayer{
		Type:   layerType,
		Params: params,
	}
}

// convertICMPv6EchoToIR converts ICMPv6 Echo layer to IR
func (p *PcapAnalyzer) convertICMPv6EchoToIR(echo *layers.ICMPv6Echo) *IRLayer {
	params := make(map[string]interface{})

	if echo.Identifier != 0 {
		params["id"] = int(echo.Identifier)
	}
	if echo.SeqNumber != 0 {
		params["seq"] = int(echo.SeqNumber)
	}

	// Note: The type should be determined by the parent ICMPv6 layer
	// This is a supplementary layer, so we return nil to avoid duplication
	return nil
}

// convertIPv6FragmentToIR converts IPv6 Fragment header to IR
func (p *PcapAnalyzer) convertIPv6FragmentToIR(frag *layers.IPv6Fragment) *IRLayer {
	params := make(map[string]interface{})

	params["id"] = int(frag.Identification)
	params["offset"] = int(frag.FragmentOffset)
	if frag.MoreFragments {
		params["m"] = 1
	} else {
		params["m"] = 0
	}

	return &IRLayer{
		Type:   "IPv6ExtHdrFragment",
		Params: params,
	}
}

// convertPayloadToIR converts payload to IR
func (p *PcapAnalyzer) convertPayloadToIR(payload gopacket.Payload) *IRLayer {
	params := make(map[string]interface{})

	// Store payload as hex string or raw bytes
	// For simplicity, we'll use a special _arg0 parameter
	params["_arg0"] = string(payload)

	return &IRLayer{
		Type:   "Raw",
		Params: params,
	}
}

// renameFunctionInGeneratedCode extracts and renames the generated function
func (p *PcapAnalyzer) renameFunctionInGeneratedCode(code string, desiredName string) string {
	// The generated code may include helper functions and the main function
	// We need to rename both the helper and the main function

	lines := strings.Split(code, "\n")
	var result strings.Builder
	inFunction := false
	braceCount := 0
	foundFunction := false
	isFirstFunction := true

	for _, line := range lines {
		// Look for function definitions (including helpers and main function)
		if strings.HasPrefix(line, "func Generate") && strings.Contains(line, "(t *testing.T)") {

			// Replace the function name
			funcStart := strings.Index(line, "func ")
			if funcStart != -1 {
				funcStart += 5 // Skip "func "
				funcEnd := strings.Index(line[funcStart:], "(")
				if funcEnd != -1 {
					oldName := line[funcStart : funcStart+funcEnd]
					// If this is a helper function (ends with "Helper"), rename it accordingly
					if strings.HasSuffix(oldName, "Helper") {
						line = "func " + desiredName + "Helper" + line[funcStart+funcEnd:]
					} else {
						// Main function
						line = "func " + desiredName + line[funcStart+funcEnd:]
					}
				}
			}
			inFunction = true
			foundFunction = true

			// Add a newline before subsequent functions (but not the first one)
			if !isFirstFunction {
				result.WriteString("\n")
			}
			isFirstFunction = false

			result.WriteString(line)
			result.WriteString("\n")

			// Count braces on the same line
			braceCount += strings.Count(line, "{") - strings.Count(line, "}")
			continue
		}

		// Look for struct definitions (for pattern-based params)
		if strings.HasPrefix(line, "type Generate") && strings.Contains(line, "Params struct") {
			// Replace the struct name
			typeStart := strings.Index(line, "type ")
			if typeStart != -1 {
				typeStart += 5 // Skip "type "
				typeEnd := strings.Index(line[typeStart:], " ")
				if typeEnd != -1 {
					line = "type " + desiredName + "Params" + line[typeStart+typeEnd:]
				}
			}
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		// If we're in a function, copy lines and track braces
		if inFunction {
			result.WriteString(line)
			result.WriteString("\n")

			braceCount += strings.Count(line, "{") - strings.Count(line, "}")

			// If braceCount reaches 0, we've finished the function
			if braceCount == 0 {
				inFunction = false
			}
		} else if foundFunction && (strings.HasPrefix(line, "//") || strings.TrimSpace(line) == "") {
			// Copy comments and empty lines between functions
			result.WriteString(line)
			result.WriteString("\n")
		}
	}

	// If we didn't find a function, return the original code
	if !foundFunction {
		return code
	}

	return result.String()
}

// GeneratePacketCreationCodeWithOptions generates packet creation code with additional options (e.g., StripVLAN)
// This method uses the same code generation path as AST parser for consistency
func (p *PcapAnalyzer) GeneratePacketCreationCodeWithOptions(packets []*PacketInfo, functionName string, opts CodegenOpts) string {
	if len(packets) == 0 {
		return fmt.Sprintf("// %s returns no packets (empty PCAP)\nfunc %s(t *testing.T) []gopacket.Packet {\n\treturn nil\n}\n", functionName, functionName)
	}

	// Convert PacketInfo to IR
	ir, err := p.ConvertPacketInfoToIR(packets, "send.pcap", "expect.pcap", opts)
	if err != nil {
		// Fallback to error comment if conversion fails
		return fmt.Sprintf("// %s - conversion error: %v\nfunc %s(t *testing.T) []gopacket.Packet {\n\treturn nil\n}\n", functionName, err, functionName)
	}

	// Extract packets from IR (same as AST parser does)
	var irPackets []IRPacketDef
	if len(ir.PCAPPairs) > 0 {
		if opts.IsExpect {
			irPackets = ir.PCAPPairs[0].ExpectPackets
		} else {
			irPackets = ir.PCAPPairs[0].SendPackets
		}
	}

	if len(irPackets) == 0 {
		return fmt.Sprintf("// %s - no packets in IR\nfunc %s(t *testing.T) []gopacket.Packet {\n\treturn nil\n}\n", functionName, functionName)
	}

	// Use ScapyCodegenV2 to generate code directly from packets (same path as AST parser)
	codegen := NewScapyCodegenV2(opts.StripVLAN)
	code := codegen.GeneratePacketFunction(functionName, irPackets, opts.IsExpect)

	return code
}

// generateEthernetLayerCode generates code for Ethernet layer with adapted MAC addresses
func (p *PcapAnalyzer) generateEthernetLayerCode(eth *layers.Ethernet, stripVLAN bool, allLayers []gopacket.Layer, isExpect bool) string {
	// Always use framework addresses
	etherType := eth.EthernetType

	// If VLAN is being stripped, change EtherType to point to the layer after VLAN
	if stripVLAN {
		for i, layer := range allLayers {
			if _, ok := layer.(*layers.Dot1Q); ok {
				// Find the next layer after VLAN
				if i+1 < len(allLayers) {
					switch allLayers[i+1].(type) {
					case *layers.IPv4:
						etherType = layers.EthernetTypeIPv4
					case *layers.IPv6:
						etherType = layers.EthernetTypeIPv6
					}
				}
				break
			}
		}
	}

	// For send packets, use fixed framework MAC addresses
	// For expect packets, use MAC addresses from PCAP as-is (already correct from yanet1)
	var srcMAC, dstMAC string
	if isExpect {
		// Swap: YANET sends back to client
		srcMAC = "0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5" // YANET
		dstMAC = "0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1" // client
	} else {
		// Use fixed framework addresses for send packets (client -> YANET)
		srcMAC = "0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1" // client
		dstMAC = "0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5" // YANET
	}

	etherTypeName := p.getEtherTypeName(etherType)
	return fmt.Sprintf(`		ethLayer := &layers.Ethernet{
			SrcMAC:       net.HardwareAddr{%s},
			DstMAC:       net.HardwareAddr{%s},
			EthernetType: %s,
		}
		layersToSerialize = append(layersToSerialize, ethLayer)

`, srcMAC, dstMAC, etherTypeName)
}

// generateVLANLayerCode generates code for VLAN (Dot1Q) layer
func (p *PcapAnalyzer) generateVLANLayerCode(vlan *layers.Dot1Q) string {
	etherType := p.getEtherTypeName(vlan.Type)
	return fmt.Sprintf(`		vlanLayer := &layers.Dot1Q{
			VLANIdentifier: %d,
			Type:           %s,
		}
		layersToSerialize = append(layersToSerialize, vlanLayer)

`, vlan.VLANIdentifier, etherType)
}

// generateIPv4LayerCode generates code for IPv4 layer with adapted addresses
func (p *PcapAnalyzer) generateIPv4LayerCode(ipv4 *layers.IPv4, isExpect bool) string {
	srcIP := ipv4.SrcIP.String()
	dstIP := ipv4.DstIP.String()

	// Only adapt router address in SEND packets (not in EXPECT packets)
	// EXPECT packets already have the correct addresses from yanet1
	if !isExpect {
		if srcIP == "200.0.0.1" {
			srcIP = "203.0.113.1"
		}
		if dstIP == "200.0.0.1" {
			dstIP = "203.0.113.1"
		}
		// Adapt YANET host IPv4 used in yanet1 tests
		if srcIP == "200.0.0.2" {
			srcIP = "203.0.113.14"
		}
		if dstIP == "200.0.0.2" {
			dstIP = "203.0.113.14"
		}
	}

	// TTL is preserved as-is from PCAP (already decremented by router if needed)
	ttl := ipv4.TTL

	protocol := p.getIPProtocolName(ipv4.Protocol)
	return fmt.Sprintf(`		ipv4Layer := &layers.IPv4{
			Version:    4,
			IHL:        %d,
			TOS:        %d,
			Id:         %d,
			Flags:      layers.IPv4Flag(%d),
			FragOffset: %d,
			TTL:        %d,
			Protocol:   %s,
			SrcIP:      net.ParseIP(%q),
			DstIP:      net.ParseIP(%q),
		}
		layersToSerialize = append(layersToSerialize, ipv4Layer)

`, ipv4.IHL, ipv4.TOS, ipv4.Id, ipv4.Flags, ipv4.FragOffset, ttl, protocol, srcIP, dstIP)
}

// generateIPv6LayerCode generates code for IPv6 layer
func (p *PcapAnalyzer) generateIPv6LayerCode(ipv6 *layers.IPv6) string {
	protocol := p.getIPProtocolName(ipv6.NextHeader)
	src := ipv6.SrcIP.String()
	dst := ipv6.DstIP.String()
	// Adapt common yanet1 link-local host address to yanet2 framework host
	if src == "fe80::2" {
		src = "fe80::5054:ff:fe6b:ffa5"
	}
	if dst == "fe80::2" {
		dst = "fe80::5054:ff:fe6b:ffa5"
	}
	return fmt.Sprintf(`		ipv6Layer := &layers.IPv6{
			Version:      6,
			TrafficClass: %d,
			FlowLabel:    %d,
			HopLimit:     %d,
			NextHeader:   %s,
			SrcIP:        net.ParseIP(%q),
			DstIP:        net.ParseIP(%q),
		}
		layersToSerialize = append(layersToSerialize, ipv6Layer)

`, ipv6.TrafficClass, ipv6.FlowLabel, ipv6.HopLimit, protocol, src, dst)
}

// generateIPv6FragmentLayerCode generates code for IPv6 Fragment layer
func (p *PcapAnalyzer) generateIPv6FragmentLayerCode(frag *layers.IPv6Fragment) string {
	nextHeader := p.getIPProtocolName(frag.NextHeader)
	return fmt.Sprintf(`		fragLayer := &layers.IPv6Fragment{
			NextHeader:     %s,
			Reserved1:      %d,
			FragmentOffset: %d,
			Reserved2:      %d,
			MoreFragments:  %t,
			Identification: %d,
		}
		layersToSerialize = append(layersToSerialize, fragLayer)

`, nextHeader, frag.Reserved1, frag.FragmentOffset, frag.Reserved2, frag.MoreFragments, frag.Identification)
}

// generateTCPLayerCode generates code for TCP layer
func (p *PcapAnalyzer) generateTCPLayerCode(tcp *layers.TCP) string {
	return fmt.Sprintf(`		tcpLayer := &layers.TCP{
			SrcPort:    layers.TCPPort(%d),
			DstPort:    layers.TCPPort(%d),
			Seq:        %d,
			Ack:        %d,
			DataOffset: %d,
			FIN:        %t,
			SYN:        %t,
			RST:        %t,
			PSH:        %t,
			ACK:        %t,
			URG:        %t,
			ECE:        %t,
			CWR:        %t,
			NS:         %t,
			Window:     %d,
			Urgent:     %d,
		}
		// Set network layer for checksum
		for i := len(layersToSerialize) - 1; i >= 0; i-- {
			if nl, ok := layersToSerialize[i].(gopacket.NetworkLayer); ok {
				tcpLayer.SetNetworkLayerForChecksum(nl)
				break
			}
		}
		layersToSerialize = append(layersToSerialize, tcpLayer)

`, tcp.SrcPort, tcp.DstPort, tcp.Seq, tcp.Ack, tcp.DataOffset,
		tcp.FIN, tcp.SYN, tcp.RST, tcp.PSH, tcp.ACK, tcp.URG, tcp.ECE, tcp.CWR, tcp.NS,
		tcp.Window, tcp.Urgent)
}

// generateUDPLayerCode generates code for UDP layer
func (p *PcapAnalyzer) generateUDPLayerCode(udp *layers.UDP) string {
	return fmt.Sprintf(`		udpLayer := &layers.UDP{
			SrcPort: layers.UDPPort(%d),
			DstPort: layers.UDPPort(%d),
		}
		// Set network layer for checksum
		for i := len(layersToSerialize) - 1; i >= 0; i-- {
			if nl, ok := layersToSerialize[i].(gopacket.NetworkLayer); ok {
				udpLayer.SetNetworkLayerForChecksum(nl)
				break
			}
		}
		layersToSerialize = append(layersToSerialize, udpLayer)

`, udp.SrcPort, udp.DstPort)
}

// generateICMPv4LayerCode generates code for ICMPv4 layer
func (p *PcapAnalyzer) generateICMPv4LayerCode(icmp *layers.ICMPv4) string {
	return fmt.Sprintf(`		icmpv4Layer := &layers.ICMPv4{
			TypeCode: layers.ICMPv4TypeCode(%d),
		}
		layersToSerialize = append(layersToSerialize, icmpv4Layer)

`, icmp.TypeCode)
}

// generateICMPv6LayerCode generates code for ICMPv6 layer
func (p *PcapAnalyzer) generateICMPv6LayerCode(icmp *layers.ICMPv6) string {
	return fmt.Sprintf(`		icmpv6Layer := &layers.ICMPv6{
			TypeCode: layers.ICMPv6TypeCode(%d),
		}
		// Set network layer for checksum
		for i := len(layersToSerialize) - 1; i >= 0; i-- {
			if nl, ok := layersToSerialize[i].(gopacket.NetworkLayer); ok {
				icmpv6Layer.SetNetworkLayerForChecksum(nl)
				break
			}
		}
		layersToSerialize = append(layersToSerialize, icmpv6Layer)

`, icmp.TypeCode)
}

// generateICMPv6EchoLayerCode generates code for ICMPv6 Echo layer
func (p *PcapAnalyzer) generateICMPv6EchoLayerCode(echo *layers.ICMPv6Echo) string {
	return fmt.Sprintf(`		icmpv6EchoLayer := &layers.ICMPv6Echo{
			Identifier: %d,
			SeqNumber:  %d,
		}
		layersToSerialize = append(layersToSerialize, icmpv6EchoLayer)

`, echo.Identifier, echo.SeqNumber)
}

// generateIPv6DestOptLayerCode generates code for IPv6 Destination Options header
func (p *PcapAnalyzer) generateIPv6DestOptLayerCode(dest *layers.IPv6Destination) string {
	// IPv6 Destination Options header - for now just a placeholder
	return fmt.Sprintf(`		// IPv6 Destination Options header
		ipv6DestLayer := &layers.IPv6Destination{}
		layersToSerialize = append(layersToSerialize, ipv6DestLayer)

`)
}

// generatePayloadCode generates code for payload
func (p *PcapAnalyzer) generatePayloadCode(payload gopacket.Payload) string {
	return fmt.Sprintf(`		payloadLayer := gopacket.Payload([]byte{%s})
		layersToSerialize = append(layersToSerialize, payloadLayer)

`, p.formatByteArray([]byte(payload)))
}

// getEtherTypeName returns the EtherType constant name
func (p *PcapAnalyzer) getEtherTypeName(et layers.EthernetType) string {
	switch et {
	case layers.EthernetTypeIPv4:
		return "layers.EthernetTypeIPv4"
	case layers.EthernetTypeIPv6:
		return "layers.EthernetTypeIPv6"
	case layers.EthernetTypeDot1Q:
		return "layers.EthernetTypeDot1Q"
	case layers.EthernetTypeARP:
		return "layers.EthernetTypeARP"
	default:
		return fmt.Sprintf("layers.EthernetType(0x%04x)", uint16(et))
	}
}

// getIPProtocolName returns the IPProtocol constant name
func (p *PcapAnalyzer) getIPProtocolName(proto layers.IPProtocol) string {
	switch proto {
	case layers.IPProtocolTCP:
		return "layers.IPProtocolTCP"
	case layers.IPProtocolUDP:
		return "layers.IPProtocolUDP"
	case layers.IPProtocolICMPv4:
		return "layers.IPProtocolICMPv4"
	case layers.IPProtocolICMPv6:
		return "layers.IPProtocolICMPv6"
	case layers.IPProtocolIPv6HopByHop:
		return "layers.IPProtocolIPv6HopByHop"
	case layers.IPProtocolIPv6Routing:
		return "layers.IPProtocolIPv6Routing"
	case layers.IPProtocolIPv6Fragment:
		return "layers.IPProtocolIPv6Fragment"
	case layers.IPProtocolIPv6Destination:
		return "layers.IPProtocolIPv6Destination"
	default:
		return fmt.Sprintf("layers.IPProtocol(%d)", uint8(proto))
	}
}

// GenerateTcpdumpComment runs tcpdump to produce a detailed packet dump comment
func (p *PcapAnalyzer) GenerateTcpdumpComment(pcapPath string, packets []*PacketInfo) (string, error) {
	cmd := exec.Command("tcpdump", "-nn", "-vvv", "-tttt", "-e", "-r", pcapPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tcpdump failed: %w", err)
	}

	short := filepath.Base(filepath.Dir(pcapPath)) + "/" + filepath.Base(pcapPath)
	var b strings.Builder

	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "reading from file ") {
			line = strings.Replace(line, pcapPath, short, 1)
		}
		b.WriteString("// ")
		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String(), nil
}

// generateRawPacket generates code for creating packet from raw data
func (p *PcapAnalyzer) generateRawPacket(info *PacketInfo) string {
	return fmt.Sprintf(`	// Return raw packet data from pcap file
	return []byte{
		%s
	}
`, p.formatByteArray(info.RawData))
}

// formatByteArray formats byte array for Go code
func (p *PcapAnalyzer) formatByteArray(data []byte) string {
	var result strings.Builder
	for i, b := range data {
		if i%16 == 0 {
			if i > 0 {
				result.WriteString(",\n\t\t")
			} else {
				result.WriteString("\t\t")
			}
		} else {
			result.WriteString(", ")
		}
		result.WriteString(fmt.Sprintf("0x%02x", b))
	}
	// Add comma at end for correct Go syntax
	if len(data) > 0 {
		result.WriteString(",")
	}
	return result.String()
}

// generateIPv4TCPPacket generates code for creating IPv4 TCP packet
func (p *PcapAnalyzer) generateIPv4TCPPacket(info *PacketInfo) string {
	if info.HasVLAN {
		return fmt.Sprintf(`	// Create buffer for packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	// Create packet layers with VLAN
	ethLayer := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{%s},
		DstMAC:       net.HardwareAddr{%s},
		EthernetType: layers.EthernetTypeDot1Q,
	}

	dot1qLayer := &layers.Dot1Q{
		VLANIdentifier: %d,
		Type:           layers.EthernetTypeIPv4,
	}

	ipLayer := &layers.IPv4{
		SrcIP:    net.ParseIP("%s"),
		DstIP:    net.ParseIP("%s"),
		Protocol: layers.IPProtocolTCP,
		TTL:      64,
	}

	tcpLayer := &layers.TCP{
		SrcPort: layers.TCPPort(%d),
		DstPort: layers.TCPPort(%d),
		Seq:     1,
		Ack:     1,
		Window:  1024,
		PSH:     true,
		ACK:     true,
	}

	// Set TCP checksum
	tcpLayer.SetNetworkLayerForChecksum(ipLayer)

	// Serialize packet with VLAN
	gopacket.SerializeLayers(buf, opts, ethLayer, dot1qLayer, ipLayer, tcpLayer, gopacket.Payload([]byte{%s}))
	return buf.Bytes()
`, p.formatMAC(info.SrcMAC), p.formatMAC(info.DstMAC), info.VLANID, info.SrcIP, info.DstIP, info.SrcPort, info.DstPort, p.formatByteArray(info.Payload))
	}

	return fmt.Sprintf(`	// Create buffer for packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	// Create packet layers
	ethLayer := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{%s},
		DstMAC:       net.HardwareAddr{%s},
		EthernetType: layers.EthernetTypeIPv4,
	}

	ipLayer := &layers.IPv4{
		SrcIP:    net.ParseIP("%s"),
		DstIP:    net.ParseIP("%s"),
		Protocol: layers.IPProtocolTCP,
		TTL:      64,
	}

	tcpLayer := &layers.TCP{
		SrcPort: layers.TCPPort(%d),
		DstPort: layers.TCPPort(%d),
		Seq:     1,
		Ack:     1,
		Window:  1024,
		PSH:     true,
		ACK:     true,
	}

	// Set TCP checksum
	tcpLayer.SetNetworkLayerForChecksum(ipLayer)

	// Serialize packet
	gopacket.SerializeLayers(buf, opts, ethLayer, ipLayer, tcpLayer, gopacket.Payload([]byte{%s}))
	return buf.Bytes()
`, p.formatMAC(info.SrcMAC), p.formatMAC(info.DstMAC), info.SrcIP, info.DstIP, info.SrcPort, info.DstPort, p.formatByteArray(info.Payload))
}

// generateIPv4UDPPacket generates code for creating IPv4 UDP packet
func (p *PcapAnalyzer) generateIPv4UDPPacket(info *PacketInfo) string {
	if info.HasVLAN {
		return fmt.Sprintf(`	// Create buffer for packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	// Create packet layers with VLAN
	ethLayer := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{%s},
		DstMAC:       net.HardwareAddr{%s},
		EthernetType: layers.EthernetTypeDot1Q,
	}

	dot1qLayer := &layers.Dot1Q{
		VLANIdentifier: %d,
		Type:           layers.EthernetTypeIPv4,
	}

	ipLayer := &layers.IPv4{
		SrcIP:    net.ParseIP("%s"),
		DstIP:    net.ParseIP("%s"),
		Protocol: layers.IPProtocolUDP,
		TTL:      64,
	}

	udpLayer := &layers.UDP{
		SrcPort: layers.UDPPort(%d),
		DstPort: layers.UDPPort(%d),
	}

	// Set UDP checksum
	udpLayer.SetNetworkLayerForChecksum(ipLayer)

	// Serialize packet with VLAN
	gopacket.SerializeLayers(buf, opts, ethLayer, dot1qLayer, ipLayer, udpLayer, gopacket.Payload([]byte{%s}))
	return buf.Bytes()
`, p.formatMAC(info.SrcMAC), p.formatMAC(info.DstMAC), info.VLANID, info.SrcIP, info.DstIP, info.SrcPort, info.DstPort, p.formatByteArray(info.Payload))
	}

	return fmt.Sprintf(`	// Create buffer for packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	// Create packet layers
	ethLayer := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{%s},
		DstMAC:       net.HardwareAddr{%s},
		EthernetType: layers.EthernetTypeIPv4,
	}

	ipLayer := &layers.IPv4{
		SrcIP:    net.ParseIP("%s"),
		DstIP:    net.ParseIP("%s"),
		Protocol: layers.IPProtocolUDP,
		TTL:      64,
	}

	udpLayer := &layers.UDP{
		SrcPort: layers.UDPPort(%d),
		DstPort: layers.UDPPort(%d),
	}

	// Set UDP checksum
	udpLayer.SetNetworkLayerForChecksum(ipLayer)

	// Serialize packet
	gopacket.SerializeLayers(buf, opts, ethLayer, ipLayer, udpLayer, gopacket.Payload([]byte{%s}))
	return buf.Bytes()
`, p.formatMAC(info.SrcMAC), p.formatMAC(info.DstMAC), info.SrcIP, info.DstIP, info.SrcPort, info.DstPort, p.formatByteArray(info.Payload))
}

// generateIPv4ICMPPacket generates code for creating IPv4 ICMP packet
func (p *PcapAnalyzer) generateIPv4ICMPPacket(info *PacketInfo) string {
	if info.HasVLAN {
		return fmt.Sprintf(`	// Create buffer for packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	// Create packet layers with VLAN
	ethLayer := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{%s},
		DstMAC:       net.HardwareAddr{%s},
		EthernetType: layers.EthernetTypeDot1Q,
	}

	dot1qLayer := &layers.Dot1Q{
		VLANIdentifier: %d,
		Type:           layers.EthernetTypeIPv4,
	}

	ipLayer := &layers.IPv4{
		SrcIP:    net.ParseIP("%s"),
		DstIP:    net.ParseIP("%s"),
		Protocol: layers.IPProtocolICMPv4,
		TTL:      64,
	}

	icmpLayer := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       0x1234,
		Seq:      1,
	}

	// Serialize packet with VLAN
	gopacket.SerializeLayers(buf, opts, ethLayer, dot1qLayer, ipLayer, icmpLayer, gopacket.Payload([]byte{%s}))
	return buf.Bytes()
`, p.formatMAC(info.SrcMAC), p.formatMAC(info.DstMAC), info.VLANID, info.SrcIP, info.DstIP, p.formatByteArray(info.Payload))
	}

	return fmt.Sprintf(`	// Create buffer for packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	// Create packet layers
	ethLayer := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{%s},
		DstMAC:       net.HardwareAddr{%s},
		EthernetType: layers.EthernetTypeIPv4,
	}

	ipLayer := &layers.IPv4{
		SrcIP:    net.ParseIP("%s"),
		DstIP:    net.ParseIP("%s"),
		Protocol: layers.IPProtocolICMPv4,
		TTL:      64,
	}

	icmpLayer := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       0x1234,
		Seq:      1,
	}

	// Serialize packet
	gopacket.SerializeLayers(buf, opts, ethLayer, ipLayer, icmpLayer, gopacket.Payload([]byte{%s}))
	return buf.Bytes()
`, p.formatMAC(info.SrcMAC), p.formatMAC(info.DstMAC), info.SrcIP, info.DstIP, p.formatByteArray(info.Payload))
}

// generateIPv6TCPPacket generates code for creating IPv6 TCP packet
func (p *PcapAnalyzer) generateIPv6TCPPacket(info *PacketInfo) string {
	if info.HasVLAN {
		return fmt.Sprintf(`	// Create buffer for packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	// Create packet layers with VLAN
	ethLayer := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{%s},
		DstMAC:       net.HardwareAddr{%s},
		EthernetType: layers.EthernetTypeDot1Q,
	}

	dot1qLayer := &layers.Dot1Q{
		VLANIdentifier: %d,
		Type:           layers.EthernetTypeIPv6,
	}

	ipLayer := &layers.IPv6{
		SrcIP:      net.ParseIP("%s"),
		DstIP:      net.ParseIP("%s"),
		NextHeader: layers.IPProtocolTCP,
		HopLimit:   64,
	}

	tcpLayer := &layers.TCP{
		SrcPort: layers.TCPPort(%d),
		DstPort: layers.TCPPort(%d),
		Seq:     1,
		Ack:     1,
		Window:  1024,
		PSH:     true,
		ACK:     true,
	}

	// Set TCP checksum
	tcpLayer.SetNetworkLayerForChecksum(ipLayer)

	// Serialize packet with VLAN
	gopacket.SerializeLayers(buf, opts, ethLayer, dot1qLayer, ipLayer, tcpLayer, gopacket.Payload([]byte{%s}))
	return buf.Bytes()
`, p.formatMAC(info.SrcMAC), p.formatMAC(info.DstMAC), info.VLANID, info.SrcIP, info.DstIP, info.SrcPort, info.DstPort, p.formatByteArray(info.Payload))
	}

	return fmt.Sprintf(`	// Create buffer for packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	// Create packet layers
	ethLayer := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{%s},
		DstMAC:       net.HardwareAddr{%s},
		EthernetType: layers.EthernetTypeIPv6,
	}

	ipLayer := &layers.IPv6{
		SrcIP:      net.ParseIP("%s"),
		DstIP:      net.ParseIP("%s"),
		NextHeader: layers.IPProtocolTCP,
		HopLimit:   64,
	}

	tcpLayer := &layers.TCP{
		SrcPort: layers.TCPPort(%d),
		DstPort: layers.TCPPort(%d),
		Seq:     1,
		Ack:     1,
		Window:  1024,
		PSH:     true,
		ACK:     true,
	}

	// Set TCP checksum
	tcpLayer.SetNetworkLayerForChecksum(ipLayer)

	// Serialize packet
	gopacket.SerializeLayers(buf, opts, ethLayer, ipLayer, tcpLayer, gopacket.Payload([]byte{%s}))
	return buf.Bytes()
`, p.formatMAC(info.SrcMAC), p.formatMAC(info.DstMAC), info.SrcIP, info.DstIP, info.SrcPort, info.DstPort, p.formatByteArray(info.Payload))
}

// generateIPv6UDPPacket generates code for creating IPv6 UDP packet
func (p *PcapAnalyzer) generateIPv6UDPPacket(info *PacketInfo) string {
	if info.HasVLAN {
		return fmt.Sprintf(`	// Create buffer for packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	// Create packet layers with VLAN
	ethLayer := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{%s},
		DstMAC:       net.HardwareAddr{%s},
		EthernetType: layers.EthernetTypeDot1Q,
	}

	dot1qLayer := &layers.Dot1Q{
		VLANIdentifier: %d,
		Type:           layers.EthernetTypeIPv6,
	}

	ipLayer := &layers.IPv6{
		SrcIP:      net.ParseIP("%s"),
		DstIP:      net.ParseIP("%s"),
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   64,
	}

	udpLayer := &layers.UDP{
		SrcPort: layers.UDPPort(%d),
		DstPort: layers.UDPPort(%d),
	}

	// Set UDP checksum
	udpLayer.SetNetworkLayerForChecksum(ipLayer)

	// Serialize packet with VLAN
	gopacket.SerializeLayers(buf, opts, ethLayer, dot1qLayer, ipLayer, udpLayer, gopacket.Payload([]byte{%s}))
	return buf.Bytes()
`, p.formatMAC(info.SrcMAC), p.formatMAC(info.DstMAC), info.VLANID, info.SrcIP, info.DstIP, info.SrcPort, info.DstPort, p.formatByteArray(info.Payload))
	}

	return fmt.Sprintf(`	// Create buffer for packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	// Create packet layers
	ethLayer := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{%s},
		DstMAC:       net.HardwareAddr{%s},
		EthernetType: layers.EthernetTypeIPv6,
	}

	ipLayer := &layers.IPv6{
		SrcIP:      net.ParseIP("%s"),
		DstIP:      net.ParseIP("%s"),
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   64,
	}

	udpLayer := &layers.UDP{
		SrcPort: layers.UDPPort(%d),
		DstPort: layers.UDPPort(%d),
	}

	// Set UDP checksum
	udpLayer.SetNetworkLayerForChecksum(ipLayer)

	// Serialize packet
	gopacket.SerializeLayers(buf, opts, ethLayer, ipLayer, udpLayer, gopacket.Payload([]byte{%s}))
	return buf.Bytes()
`, p.formatMAC(info.SrcMAC), p.formatMAC(info.DstMAC), info.SrcIP, info.DstIP, info.SrcPort, info.DstPort, p.formatByteArray(info.Payload))
}

// generateIPv6ICMPPacket generates code for creating IPv6 ICMP packet
func (p *PcapAnalyzer) generateIPv6ICMPPacket(info *PacketInfo) string {
	if info.HasVLAN {
		return fmt.Sprintf(`	// Create buffer for packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	// Create packet layers with VLAN
	ethLayer := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{%s},
		DstMAC:       net.HardwareAddr{%s},
		EthernetType: layers.EthernetTypeDot1Q,
	}

	dot1qLayer := &layers.Dot1Q{
		VLANIdentifier: %d,
		Type:           layers.EthernetTypeIPv6,
	}

	ipLayer := &layers.IPv6{
		SrcIP:      net.ParseIP("%s"),
		DstIP:      net.ParseIP("%s"),
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
	}

	icmpLayer := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}

	// Serialize packet with VLAN
	gopacket.SerializeLayers(buf, opts, ethLayer, dot1qLayer, ipLayer, icmpLayer, gopacket.Payload([]byte{%s}))
	return buf.Bytes()
`, p.formatMAC(info.SrcMAC), p.formatMAC(info.DstMAC), info.VLANID, info.SrcIP, info.DstIP, p.formatByteArray(info.Payload))
	}

	return fmt.Sprintf(`	// Create buffer for packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	// Create packet layers
	ethLayer := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{%s},
		DstMAC:       net.HardwareAddr{%s},
		EthernetType: layers.EthernetTypeIPv6,
	}

	ipLayer := &layers.IPv6{
		SrcIP:      net.ParseIP("%s"),
		DstIP:      net.ParseIP("%s"),
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
	}

	icmpLayer := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}

	// Serialize packet
	gopacket.SerializeLayers(buf, opts, ethLayer, ipLayer, icmpLayer, gopacket.Payload([]byte{%s}))
	return buf.Bytes()
`, p.formatMAC(info.SrcMAC), p.formatMAC(info.DstMAC), info.SrcIP, info.DstIP, p.formatByteArray(info.Payload))
}

// formatMAC formats MAC address for Go code
func (p *PcapAnalyzer) formatMAC(macStr string) string {
	mac, err := net.ParseMAC(macStr)
	if err != nil {
		return "0x00, 0x00, 0x00, 0x00, 0x00, 0x00"
	}

	var result strings.Builder
	for i, b := range mac {
		if i > 0 {
			result.WriteString(", ")
		}
		result.WriteString(fmt.Sprintf("0x%02x", b))
	}
	return result.String()
}

// formatPayload formats payload for Go code
func (p *PcapAnalyzer) formatPayload(payload []byte) string {
	if len(payload) == 0 {
		return "[]byte{}"
	}

	var result strings.Builder
	result.WriteString("[]byte{")
	for i, b := range payload {
		if i > 0 {
			result.WriteString(", ")
		}
		result.WriteString(fmt.Sprintf("0x%02x", b))
	}
	result.WriteString("}")
	return result.String()
}

// GetPacketDescription returns packet description for comments
func (p *PcapAnalyzer) GetPacketDescription(info *PacketInfo) string {
	var desc strings.Builder

	if info.IsIPv4 {
		desc.WriteString("IPv4")
	} else if info.IsIPv6 {
		desc.WriteString("IPv6")
	}

	if info.IsTCP {
		desc.WriteString(" TCP")
	} else if info.IsUDP {
		desc.WriteString(" UDP")
	} else if info.IsICMP {
		desc.WriteString(" ICMP")
	}

	desc.WriteString(fmt.Sprintf(" packet: %s -> %s", info.SrcIP, info.DstIP))

	if info.SrcPort != 0 || info.DstPort != 0 {
		desc.WriteString(fmt.Sprintf(" (%d -> %d)", info.SrcPort, info.DstPort))
	}

	if info.HasVLAN {
		desc.WriteString(fmt.Sprintf(" [VLAN: %d]", info.VLANID))
	}

	return desc.String()
}

// GenerateDirectPacketReadCode generates code for direct pcap file reading
func (p *PcapAnalyzer) GenerateDirectPacketReadCode(pcapPath string, functionName string) string {
	return fmt.Sprintf(`// %s reads packet directly from pcap file
func %s() ([]byte, error) {
	// Read pcap file and return first packet
	handle, err := pcap.OpenOffline("%s")
	if err != nil {
		return nil, fmt.Errorf("failed to open pcap file: %%w", err)
	}
	defer handle.Close()

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	
	for packet := range packetSource.Packets() {
		return packet.Data(), nil
	}
	
	return nil, fmt.Errorf("no packets found in pcap file")
}
`, functionName, functionName, pcapPath)
}

type mplsLabel struct {
	Label uint32
	TC    uint8
	TTL   uint8
	BOS   bool
}

func (p *PcapAnalyzer) extractMPLSLabels(info *PacketInfo) []mplsLabel {
	var labels []mplsLabel
	packet := gopacket.NewPacket(info.RawData, layers.LayerTypeEthernet, gopacket.Lazy)
	for _, layer := range packet.Layers() {
		if mplsLayer, ok := layer.(*layers.MPLS); ok {
			labels = append(labels, mplsLabel{
				Label: uint32(mplsLayer.Label),
				TC:    mplsLayer.TrafficClass,
				TTL:   mplsLayer.TTL,
				BOS:   mplsLayer.StackBottom,
			})
		}
	}
	return labels
}
