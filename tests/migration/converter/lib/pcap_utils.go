package lib

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcap"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
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
			convertedLayers, err := p.convertLayerToIR(layer, opts)
			if err != nil {
				return nil, fmt.Errorf("failed to convert layer: %w", err)
			}
			for _, irLayer := range convertedLayers {
				if irLayer != nil {
					// Skip VLAN if StripVLAN is enabled
					if opts.StripVLAN && irLayer.Type == "Dot1Q" {
						continue
					}
					irLayers = append(irLayers, *irLayer)
				}
			}
		}

		// Append application payload as Raw layer if present
		if app := pkt.ApplicationLayer(); app != nil {
			payload := app.Payload()
			if len(payload) > 0 {
				irLayers = append(irLayers, IRLayer{
					Type:   "Raw",
					Params: map[string]interface{}{"_arg0": string(payload)},
				})
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
// Returns a slice of IRLayer to support layers that need to generate multiple IR layers (e.g. ICMPv6 + payload)
func (p *PcapAnalyzer) convertLayerToIR(layer gopacket.Layer, opts CodegenOpts) ([]*IRLayer, error) {
	switch l := layer.(type) {
	case *layers.Ethernet:
		return []*IRLayer{p.convertEthernetToIR(l, opts)}, nil
	case *layers.Dot1Q:
		return []*IRLayer{p.convertDot1QToIR(l)}, nil
	case *layers.IPv4:
		return []*IRLayer{p.convertIPv4ToIR(l, opts)}, nil
	case *layers.IPv6:
		return []*IRLayer{p.convertIPv6ToIR(l, opts)}, nil
	case *layers.TCP:
		return []*IRLayer{p.convertTCPToIR(l)}, nil
	case *layers.UDP:
		return []*IRLayer{p.convertUDPToIR(l)}, nil
	case *layers.ICMPv4:
		return []*IRLayer{p.convertICMPv4ToIR(l)}, nil
	case *layers.ICMPv6:
		return p.convertICMPv6ToIR(l), nil
	case *layers.ICMPv6Echo:
		// Handled inline by convertICMPv6ToIR (to avoid duplicate echo serialization)
		return nil, nil
	case *layers.MPLS:
		return []*IRLayer{p.convertMPLSToIR(l)}, nil
	case *layers.IPv6Fragment:
		return []*IRLayer{p.convertIPv6FragmentToIR(l)}, nil
	case *layers.IPv6Destination:
		return []*IRLayer{p.convertIPv6DestinationToIR(l)}, nil
	case *layers.IPv6HopByHop:
		return []*IRLayer{p.convertIPv6HopByHopToIR(l)}, nil
	case *layers.IPv6Routing:
		return []*IRLayer{p.convertIPv6RoutingToIR(l)}, nil
	case gopacket.Payload:
		if len(l) > 0 {
			return []*IRLayer{p.convertPayloadToIR(l)}, nil
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
			params["src"] = framework.DstMAC // YANET
			params["dst"] = framework.SrcMAC // client
		} else {
			// client -> YANET
			params["src"] = framework.SrcMAC // client
			params["dst"] = framework.DstMAC // YANET
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
	if ipv4.Flags != 0 {
		params["flags"] = int(ipv4.Flags)
	}
	if ipv4.FragOffset != 0 {
		params["frag"] = int(ipv4.FragOffset)
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

	if tcp.Window != 0 {
		params["window"] = int(tcp.Window)
	}
	if tcp.Urgent != 0 {
		params["urg"] = int(tcp.Urgent)
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
	// Preserve checksum as seen in PCAP (including zero)
	params["chksum"] = int(udp.Checksum)

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
// For Echo Request/Reply, extracts payload beyond ICMPv6Echo header (4 bytes: Id + Seq)
func (p *PcapAnalyzer) convertICMPv6ToIR(icmp *layers.ICMPv6) []*IRLayer {
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

	// For Echo Request/Reply, capture Echo Identifier/Seq from the first 4 bytes of payload
	if icmpType == 128 || icmpType == 129 {
		pl := icmp.LayerPayload()
		if len(pl) >= 4 {
			id := int(pl[0])<<8 | int(pl[1])
			seq := int(pl[2])<<8 | int(pl[3])
			if id != 0 {
				params["id"] = id
			}
			if seq != 0 {
				params["seq"] = seq
			}
		}
	}

	icmpLayer := &IRLayer{
		Type:   layerType,
		Params: params,
	}

	// For Echo Request/Reply, check if there's payload beyond ICMPv6Echo header
	// LayerPayload() contains: [4 bytes ICMPv6Echo header (Id+Seq)] + [actual payload]
	result := []*IRLayer{icmpLayer}
	if icmpType == 128 || icmpType == 129 {
		payload := icmp.LayerPayload()
		// Skip 4-byte ICMPv6Echo header (2 bytes Id + 2 bytes Seq)
		if len(payload) > 4 {
			actualPayload := payload[4:]
			if len(actualPayload) > 0 {
				rawLayer := &IRLayer{
					Type: "Raw",
					Params: map[string]interface{}{
						"_arg0": string(actualPayload),
					},
				}
				result = append(result, rawLayer)
			}
		}
	}

	return result
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

	// Return a dedicated ICMPv6Echo layer so codegen can emit echo builder
	return &IRLayer{
		Type:   "ICMPv6Echo",
		Params: params,
	}
}

// convertMPLSToIR converts MPLS layer to IR
func (p *PcapAnalyzer) convertMPLSToIR(mpls *layers.MPLS) *IRLayer {
	params := make(map[string]interface{})

	if mpls.Label != 0 {
		params["label"] = int(mpls.Label)
	}
	if mpls.TTL != 0 {
		params["ttl"] = int(mpls.TTL)
	}
	// s is bottom-of-stack bit
	if mpls.StackBottom {
		params["s"] = 1
	} else {
		params["s"] = 0
	}
	if mpls.TrafficClass != 0 {
		params["cos"] = int(mpls.TrafficClass)
	}

	return &IRLayer{
		Type:   "MPLS",
		Params: params,
	}
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
	if frag.NextHeader != 0 {
		params["nh"] = int(frag.NextHeader)
	}

	return &IRLayer{
		Type:   "IPv6ExtHdrFragment",
		Params: params,
	}
}

// convertIPv6HopByHopToIR converts IPv6 Hop-by-Hop extension header to IR as raw bytes
func (p *PcapAnalyzer) convertIPv6HopByHopToIR(hbh *layers.IPv6HopByHop) *IRLayer {
	return &IRLayer{
		Type:   "Raw",
		Params: map[string]interface{}{"_arg0": string(hbh.BaseLayer.Contents)},
	}
}

// convertIPv6DestinationToIR converts IPv6 Destination Options header to IR as raw bytes
func (p *PcapAnalyzer) convertIPv6DestinationToIR(dst *layers.IPv6Destination) *IRLayer {
	return &IRLayer{
		Type:   "Raw",
		Params: map[string]interface{}{"_arg0": string(dst.BaseLayer.Contents)},
	}
}

// convertIPv6RoutingToIR converts IPv6 Routing header to IR as raw bytes
func (p *PcapAnalyzer) convertIPv6RoutingToIR(r *layers.IPv6Routing) *IRLayer {
	return &IRLayer{
		Type:   "Raw",
		Params: map[string]interface{}{"_arg0": string(r.BaseLayer.Contents)},
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
// renameFunctionInGeneratedCode is unused and removed for cleanup

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

// getEtherTypeName returns the EtherType constant name
// getEtherTypeName was unused and removed

// getIPProtocolName returns the IPProtocol constant name
// getIPProtocolName was unused and removed

// GenerateTcpdumpComment runs tcpdump to produce a detailed packet dump comment
func (p *PcapAnalyzer) GenerateTcpdumpComment(pcapPath string, packets []*PacketInfo) (string, error) {
	if !p.verbose {
		return "", nil
	}
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

// formatByteArray formats byte array for Go code
// formatByteArray was unused and removed

// formatMAC formats MAC address for Go code
// formatMAC was unused and removed

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

// mplsLabel was unused and removed
