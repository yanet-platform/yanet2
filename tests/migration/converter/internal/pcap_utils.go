package internal

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

	if len(packets) == 0 {
		return nil, fmt.Errorf("pcap file %s contains no packets", filename)
	}

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
	StripVLAN bool
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

// GeneratePacketCreationCodeWithOptions generates packet creation code with additional options (e.g., StripVLAN)
// Returns gopacket.Packet created via common.LayersToPacket
func (p *PcapAnalyzer) GeneratePacketCreationCodeWithOptions(info *PacketInfo, functionName string, opts CodegenOpts) string {
	// Clone to handle VLAN stripping
	clone := *info
	if opts.StripVLAN && info.HasVLAN {
		clone.HasVLAN = false
	}

	vlanNote := ""
	if opts.StripVLAN && info.HasVLAN {
		vlanNote = " (VLAN stripped)"
	}

	// Build layers creation code
	ethLayer := fmt.Sprintf(`	srcMAC, _ := net.ParseMAC("%s")
	dstMAC, _ := net.ParseMAC("%s")
	ethLayer := &layers.Ethernet{
		SrcMAC:       srcMAC,
		DstMAC:       dstMAC,
		EthernetType: %s,
	}`, clone.SrcMAC, clone.DstMAC, p.getEtherType(&clone))

	var vlanLayer string
	if clone.HasVLAN {
		vlanType := "layers.EthernetTypeIPv4"
		if clone.IsIPv6 {
			vlanType = "layers.EthernetTypeIPv6"
		}
		vlanLayer = fmt.Sprintf(`
	vlanLayer := &layers.Dot1Q{
		VLANIdentifier: %d,
		Type:           %s,
	}`, clone.VLANID, vlanType)
	}

	var ipLayer string
	if clone.IsIPv4 {
		ipLayer = fmt.Sprintf(`
	ipLayer := &layers.IPv4{
		SrcIP:    net.ParseIP("%s"),
		DstIP:    net.ParseIP("%s"),
		Protocol: layers.IPProtocol%s,
		TTL:      64,
	}`, clone.SrcIP, clone.DstIP, p.getIPProtocolName(&clone))
	} else if clone.IsIPv6 {
		ipLayer = fmt.Sprintf(`
	ipLayer := &layers.IPv6{
		Version:    6,
		SrcIP:      net.ParseIP("%s"),
		DstIP:      net.ParseIP("%s"),
		NextHeader: layers.IPProtocol%s,
		HopLimit:   64,
	}`, clone.SrcIP, clone.DstIP, p.getIPProtocolName(&clone))
	}

	var transportLayer string
	var checksumSetup string
	if clone.IsTCP {
		transportLayer = fmt.Sprintf(`
	tcpLayer := &layers.TCP{
		SrcPort: layers.TCPPort(%d),
		DstPort: layers.TCPPort(%d),
		Seq:     1,
		Ack:     1,
		Window:  1024,
		PSH:     true,
		ACK:     true,
	}`, clone.SrcPort, clone.DstPort)
		checksumSetup = "\n\ttcpLayer.SetNetworkLayerForChecksum(ipLayer)"
	} else if clone.IsUDP {
		transportLayer = fmt.Sprintf(`
	udpLayer := &layers.UDP{
		SrcPort: layers.UDPPort(%d),
		DstPort: layers.UDPPort(%d),
	}`, clone.SrcPort, clone.DstPort)
		checksumSetup = "\n\tudpLayer.SetNetworkLayerForChecksum(ipLayer)"
	} else if clone.IsICMP {
		if clone.IsIPv4 {
			transportLayer = `
	icmpLayer := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}`
		} else {
			transportLayer = `
	icmpLayer := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}`
			checksumSetup = "\n\ticmpLayer.SetNetworkLayerForChecksum(ipLayer)"
		}
	}

	var payloadLayer string
	if len(clone.Payload) > 0 {
		payloadLayer = fmt.Sprintf("\n\tpayloadLayer := gopacket.Payload([]byte{%s})", p.formatByteArray(clone.Payload))
	}

	// Build layers list
	layersList := "ethLayer"
	if clone.HasVLAN {
		layersList += ", vlanLayer"
	}
	layersList += ", ipLayer"
	if clone.IsTCP {
		layersList += ", tcpLayer"
	} else if clone.IsUDP {
		layersList += ", udpLayer"
	} else if clone.IsICMP {
		layersList += ", icmpLayer"
	}
	if len(clone.Payload) > 0 {
		layersList += ", payloadLayer"
	}

	return fmt.Sprintf(`// %s creates packet based on pcap file analysis%s
func %s(t *testing.T) gopacket.Packet {
%s%s%s%s%s%s

	// Serialize packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err := gopacket.SerializeLayers(buf, opts, %s)
	require.NoError(t, err)
	
	pkt := gopacket.NewPacket(buf.Bytes(), layers.LayerTypeEthernet, gopacket.Default)
	require.Empty(t, pkt.ErrorLayer())
	return pkt
}
`, functionName, vlanNote, functionName, ethLayer, vlanLayer, ipLayer, transportLayer, checksumSetup, payloadLayer, layersList)
}

// getEtherType returns the Ethernet type string for the packet
func (p *PcapAnalyzer) getEtherType(info *PacketInfo) string {
	if info.HasVLAN {
		return "layers.EthernetTypeDot1Q"
	}
	if info.IsIPv4 {
		return "layers.EthernetTypeIPv4"
	}
	if info.IsIPv6 {
		return "layers.EthernetTypeIPv6"
	}
	return "layers.EthernetTypeIPv4"
}

// getIPProtocolName returns the IP protocol name for the packet
func (p *PcapAnalyzer) getIPProtocolName(info *PacketInfo) string {
	if info.IsTCP {
		return "TCP"
	}
	if info.IsUDP {
		return "UDP"
	}
	if info.IsICMP {
		if info.IsIPv4 {
			return "ICMPv4"
		}
		return "ICMPv6"
	}
	return "TCP"
}

// GenerateTcpdumpComment runs tcpdump to produce a detailed packet dump comment
func (p *PcapAnalyzer) GenerateTcpdumpComment(pcapPath string, info *PacketInfo) string {
	cmd := exec.Command("tcpdump", "-nn", "-vvv", "-XX", "-tttt", "-e", "-c", "1", "-r", pcapPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("// tcpdump error: %v\n", err)
	}
	var b strings.Builder
	// Short pcap path
	short := filepath.Base(filepath.Dir(pcapPath)) + "/" + filepath.Base(pcapPath)
	b.WriteString("// tcpdump (pcap: ")
	b.WriteString(short)
	b.WriteString("):\n")
	if info != nil {
		if info.SrcMAC != "" || info.DstMAC != "" {
			b.WriteString("// ethernet: ")
			b.WriteString(info.SrcMAC)
			b.WriteString(" > ")
			b.WriteString(info.DstMAC)
			b.WriteString("\n")
		}
		if info.HasVLAN && info.VLANID > 0 {
			b.WriteString("// vlan: ")
			b.WriteString(fmt.Sprintf("%d\n", info.VLANID))
		}
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Replace absolute path with short relative path in 'reading from file ...'
		if strings.HasPrefix(line, "reading from file ") {
			line = strings.Replace(line, pcapPath, short, 1)
		}
		b.WriteString("// ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// generateTcpdumpLikeComment generates tcpdump-like detailed packet information
func (p *PcapAnalyzer) generateTcpdumpLikeComment(info *PacketInfo) string {
	var comment strings.Builder

	// Ethernet layer
	comment.WriteString(fmt.Sprintf("//   Ethernet: %s > %s, ethertype ", info.SrcMAC, info.DstMAC))
	if info.IsIPv4 {
		comment.WriteString("IPv4 (0x0800)")
	} else if info.IsIPv6 {
		comment.WriteString("IPv6 (0x86dd)")
	}
	if info.VLANID > 0 {
		comment.WriteString(fmt.Sprintf(", vlan %d", info.VLANID))
	}
	comment.WriteString(fmt.Sprintf(", length %d\n", len(info.RawData)))

	// IP layer
	if info.IsIPv4 {
		comment.WriteString(fmt.Sprintf("//   IP: %s > %s, proto ", info.SrcIP, info.DstIP))
		if info.IsTCP {
			comment.WriteString("TCP (6)")
		} else if info.IsUDP {
			comment.WriteString("UDP (17)")
		} else if info.IsICMP {
			comment.WriteString("ICMP (1)")
		} else {
			comment.WriteString(fmt.Sprintf("(%s)", info.Protocol))
		}
		comment.WriteString(fmt.Sprintf(", length %d, ttl %d\n", info.PayloadSize, 64))
	} else if info.IsIPv6 {
		comment.WriteString(fmt.Sprintf("//   IPv6: %s > %s, ", info.SrcIP, info.DstIP))
		if info.IsTCP {
			comment.WriteString("next TCP")
		} else if info.IsUDP {
			comment.WriteString("next UDP")
		} else if info.IsICMP {
			comment.WriteString("next ICMPv6")
		} else {
			comment.WriteString(fmt.Sprintf("next %s", info.Protocol))
		}
		comment.WriteString(fmt.Sprintf(", length %d, hlim %d\n", info.PayloadSize, 64))
	}

	// Transport layer
	if info.IsTCP {
		comment.WriteString(fmt.Sprintf("//   TCP: %d > %d\n",
			info.SrcPort, info.DstPort))
	} else if info.IsUDP {
		comment.WriteString(fmt.Sprintf("//   UDP: %d > %d\n",
			info.SrcPort, info.DstPort))
	} else if info.IsICMP {
		comment.WriteString("//   ICMP\n")
	}

	return comment.String()
}

// getTCPFlags returns string representation of TCP flags
func (p *PcapAnalyzer) getTCPFlags(info *PacketInfo) string {
	// PacketInfo does not currently store per-flag booleans; return placeholder
	return "n/a"
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
