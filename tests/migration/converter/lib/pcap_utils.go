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

// CodegenOpts controls packet creation code generation from IR/PacketInfo.
// These options are deliberately kept small: higher-level behaviour (skiplist,
// test type, module selection) lives in converter.go.
type CodegenOpts struct {
	StripVLAN        bool
	IsExpect         bool // Generate expected (response) packet with adapted TTL and swapped MACs
	UseFrameworkMACs bool // Force use of framework standard MACs (52:54:00:6b:ff:a1/a5)
}

// ConvertPacketInfoToIR converts a slice of PacketInfo structures into the IR JSON
// representation used by the code generator. It is responsible for:
//   - normal packet layer conversion (Ethernet, VLAN, IPv4/IPv6, TCP/UDP, ICMP, GRE, MPLS, ARP, IPSec)
//   - IPv6 special handling (malformed plen, extension headers, No Next Header with payload)
//   - GRE corner cases where gopacket emits DecodeFailure or IPv6.NextHeader==GRE but GRE is not decoded
//   - IPv4 and TCP manual reconstruction from raw bytes when gopacket cannot parse headers correctly
//   - preserving malformed packets byte-for-byte (including padding, checksums, and options)
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

		// Detect malformed IPv6 payload length using gopacket layer view (robust across tunnels)
		// Collapse extension headers into a Raw payload ONLY if parsing didn't reach a transport layer.
		collapseIPv6ToRaw := false
		var ipv6PayloadBytes []byte
		if ipv6Layer := pkt.Layer(layers.LayerTypeIPv6); ipv6Layer != nil {
			ipv6 := ipv6Layer.(*layers.IPv6)
			expectedPayloadLen := int(ipv6.Length)
			ipv6PayloadBytes = ipv6.LayerPayload()
			actualPayloadLen := len(ipv6PayloadBytes)

			// Determine if any transport was parsed beyond IPv6
			hasTransport := pkt.Layer(layers.LayerTypeTCP) != nil ||
				pkt.Layer(layers.LayerTypeUDP) != nil ||
				pkt.Layer(layers.LayerTypeICMPv6) != nil ||
				pkt.Layer(layers.LayerTypeICMPv4) != nil

			// Special case: IPv6 with malformed plen or extension headers
			// For packets where plen doesn't match actual payload, or where there are extension headers,
			// we need to preserve exact bytes

			// Check if packet has IPv6 extension headers
			hasIPv6ExtHeaders := pkt.Layer(layers.LayerTypeIPv6Destination) != nil ||
				pkt.Layer(layers.LayerTypeIPv6HopByHop) != nil ||
				pkt.Layer(layers.LayerTypeIPv6Routing) != nil ||
				pkt.Layer(layers.LayerTypeIPv6Fragment) != nil

			// Check if we need to extract actual payload from raw data
			// This is needed when:
			// 1. NextHeader = No Next Header (0x1B) but has payload
			// 2. plen doesn't match actual payload
			// 3. There are IPv6 extension headers (they need to be preserved as Raw)
			if (ipv6.NextHeader == 0x1B || expectedPayloadLen != actualPayloadLen || hasIPv6ExtHeaders) && !hasTransport {
				// For malformed packets or packets with extension headers,
				// gopacket may not parse payload correctly
				// We need to extract the actual bytes from the raw packet data
				// Calculate the offset of IPv6 payload in raw data
				var payloadOffset int

				// Find Ethernet layer
				if ethLayer := pkt.Layer(layers.LayerTypeEthernet); ethLayer != nil {
					payloadOffset += 14 // Ethernet header

					// Check for VLAN
					if vlanLayer := pkt.Layer(layers.LayerTypeDot1Q); vlanLayer != nil {
						payloadOffset += 4 // VLAN tag
					}

					payloadOffset += 40 // IPv6 header

					// Extract payload bytes from raw data
					if payloadOffset < len(info.RawData) {
						actualPayloadFromRaw := info.RawData[payloadOffset:]
						if len(actualPayloadFromRaw) > 0 {
							ipv6PayloadBytes = actualPayloadFromRaw
							collapseIPv6ToRaw = true
						}
					}
				}
			}
		}

		// Note: We don't skip malformed packets here - the converter should generate
		// exactly the same packets as in the original PCAP, including malformed ones.
		// The dropping/filtering logic should be implemented in the target system.

		// Check for GRE packets with DecodeFailure (malformed GRE flags)
		// In such cases, manually parse GRE payload
		hasGRE := pkt.Layer(layers.LayerTypeGRE) != nil
		hasDecodeFailure := false
		hasIPv6WithGRE := false
		for _, layer := range packetLayers {
			if layer.LayerType() == gopacket.LayerTypeDecodeFailure {
				hasDecodeFailure = true
			}
			if layer.LayerType() == layers.LayerTypeIPv6 {
				ipv6 := layer.(*layers.IPv6)
				if ipv6.NextHeader == layers.IPProtocolGRE {
					hasIPv6WithGRE = true
				}
			}
		}

		var irLayers []IRLayer
		// Flag to stop processing layers after ICMPv6 error messages
		// (their payload contains an embedded packet that was already extracted)
		stopAfterICMPv6Error := false

		for _, layer := range packetLayers {
			// Stop processing if we already handled ICMPv6 error (payload is already extracted)
			if stopAfterICMPv6Error {
				break
			}

			// Skip IPv6 extension headers if we plan to collapse IPv6 into Raw.
			if collapseIPv6ToRaw {
				layerType := layer.LayerType()
				if layerType == layers.LayerTypeIPv6Destination ||
					layerType == layers.LayerTypeIPv6HopByHop ||
					layerType == layers.LayerTypeIPv6Routing {
					continue
				}
			}

			// Skip DecodeFailure layers
			if layer.LayerType() == gopacket.LayerTypeDecodeFailure {
				continue
			}

			convertedLayers, err := p.convertLayerToIR(layer, opts)
			if err != nil {
				return nil, fmt.Errorf("failed to convert layer: %w", err)
			}

			// Check if this is an ICMPv6 error message (sets flag to stop after this layer)
			isICMPv6Error := false
			for _, irLayer := range convertedLayers {
				if irLayer != nil {
					if irLayer.Type == "ICMPv6PacketTooBig" || irLayer.Type == "ICMPv6DestUnreach" ||
						irLayer.Type == "ICMPv6TimeExceeded" || irLayer.Type == "ICMPv6ParamProblem" {
						isICMPv6Error = true
					}
				}
			}

			for _, irLayer := range convertedLayers {
				if irLayer != nil {
					// Skip VLAN if StripVLAN is enabled
					if opts.StripVLAN && irLayer.Type == "Dot1Q" {
						continue
					}
					// Skip empty Raw layers (will be handled by special IPv6 malformed packet logic)
					if irLayer.Type == "Raw" {
						if arg0, ok := irLayer.Params["_arg0"].(string); ok && len(arg0) == 0 {
							continue
						}
					}
					irLayers = append(irLayers, *irLayer)
				}
			}

			// Stop after ICMPv6 error messages (embedded packet was already extracted)
			if isICMPv6Error {
				stopAfterICMPv6Error = true
			}

			// Special handling for GRE with DecodeFailure: manually parse GRE payload
			// Case 1: GRE layer exists but has DecodeFailure after it
			if hasGRE && hasDecodeFailure && layer.LayerType() == layers.LayerTypeGRE {
				greLayer := layer.(*layers.GRE)

				// gopacket may incorrectly parse GRE with unsupported flags (e.g., ack present)
				// Get the full packet data and find GRE layer position
				rawData := info.RawData
				greContents := greLayer.LayerContents()

				// Find GRE header in raw data
				var actualPayload []byte
				for i := 0; i < len(rawData)-len(greContents); i++ {
					// Check if we found the GRE header
					if len(greContents) >= 4 && i+len(greContents) <= len(rawData) {
						match := true
						for j := 0; j < len(greContents) && j < 4; j++ {
							if rawData[i+j] != greContents[j] {
								match = false
								break
							}
						}
						if match {
							// Found GRE header, now find IP header after it
							for j := i + 4; j < len(rawData)-20 && j < i+16; j++ {
								if rawData[j] == 0x45 || (rawData[j]&0xF0) == 0x40 || (rawData[j]&0xF0) == 0x60 {
									actualPayload = rawData[j:]
									break
								}
							}
							break
						}
					}
				}

				// Try to parse as IPv4 if protocol is 0x0800
				if greLayer.Protocol == layers.EthernetTypeIPv4 && len(actualPayload) > 0 {
					ipv4Packet := gopacket.NewPacket(actualPayload, layers.LayerTypeIPv4, gopacket.Default)
					for _, innerLayer := range ipv4Packet.Layers() {
						if innerLayer.LayerType() == gopacket.LayerTypeDecodeFailure {
							continue
						}
						innerConverted, err := p.convertLayerToIR(innerLayer, opts)
						if err != nil {
							return nil, fmt.Errorf("failed to convert GRE inner layer: %w", err)
						}
						for _, innerIR := range innerConverted {
							if innerIR != nil {
								irLayers = append(irLayers, *innerIR)
							}
						}
					}
				} else if greLayer.Protocol == layers.EthernetTypeIPv6 && len(actualPayload) > 0 {
					// Try to parse as IPv6 if protocol is 0x86DD
					ipv6Packet := gopacket.NewPacket(actualPayload, layers.LayerTypeIPv6, gopacket.Default)
					for _, innerLayer := range ipv6Packet.Layers() {
						if innerLayer.LayerType() == gopacket.LayerTypeDecodeFailure {
							continue
						}
						innerConverted, err := p.convertLayerToIR(innerLayer, opts)
						if err != nil {
							return nil, fmt.Errorf("failed to convert GRE inner layer: %w", err)
						}
						for _, innerIR := range innerConverted {
							if innerIR != nil {
								irLayers = append(irLayers, *innerIR)
							}
						}
					}
				}
			}
		}

		// Note: gopacket correctly decodes IPv4-in-IPv6 tunneling automatically
		// No special handling needed - IPv4 layer will be present in packetLayers

		// Case 2: IPv6 with NextHeader=GRE but GRE layer not decoded (completely malformed)
		// This happens when GRE has routing_present or other unsupported features
		if !hasGRE && hasIPv6WithGRE && hasDecodeFailure {
			// Find IPv6 layer and manually parse GRE from its payload
			ipv6Layer := pkt.Layer(layers.LayerTypeIPv6)
			if ipv6Layer != nil {
				ipv6 := ipv6Layer.(*layers.IPv6)
				ipv6Payload := ipv6.LayerPayload()

				if len(ipv6Payload) >= 4 {
					// Parse GRE header manually
					greFlags := uint16(ipv6Payload[0])<<8 | uint16(ipv6Payload[1])
					greProtocol := uint16(ipv6Payload[2])<<8 | uint16(ipv6Payload[3])

					// Add GRE layer to IR
					greParams := make(map[string]interface{})
					greParams["proto"] = int(greProtocol)
					greParams["raw_flags"] = int(greFlags)
					greParams["chksum_present"] = 0
					if greFlags&0x8000 != 0 {
						greParams["chksum_present"] = 1
					}
					greParams["routing_present"] = 0
					if greFlags&0x4000 != 0 {
						greParams["routing_present"] = 1
					}
					greParams["key_present"] = 0
					if greFlags&0x2000 != 0 {
						greParams["key_present"] = 1
					}
					greParams["seqnum_present"] = 0
					if greFlags&0x1000 != 0 {
						greParams["seqnum_present"] = 1
					}

					irLayers = append(irLayers, IRLayer{
						Type:   "GRE",
						Params: greParams,
					})

					// Find IPv4/IPv6 after GRE header
					// GRE header size: 4 bytes base + optional fields
					greHeaderSize := 4
					if greFlags&0x8000 != 0 { // checksum present
						greHeaderSize += 4
					}
					if greFlags&0x4000 != 0 { // routing present
						greHeaderSize += 4
					}
					if greFlags&0x2000 != 0 { // key present
						greHeaderSize += 4
					}
					if greFlags&0x1000 != 0 { // sequence present
						greHeaderSize += 4
					}

					if len(ipv6Payload) > greHeaderSize {
						innerPayload := ipv6Payload[greHeaderSize:]

						// Try to parse as IPv4 or IPv6
						if len(innerPayload) > 0 && (innerPayload[0]&0xF0) == 0x40 {
							// IPv4
							ipv4Packet := gopacket.NewPacket(innerPayload, layers.LayerTypeIPv4, gopacket.Default)
							for _, innerLayer := range ipv4Packet.Layers() {
								if innerLayer.LayerType() == gopacket.LayerTypeDecodeFailure {
									continue
								}
								innerConverted, err := p.convertLayerToIR(innerLayer, opts)
								if err != nil {
									return nil, fmt.Errorf("failed to convert GRE inner layer: %w", err)
								}
								for _, innerIR := range innerConverted {
									if innerIR != nil {
										irLayers = append(irLayers, *innerIR)
									}
								}
							}
						} else if len(innerPayload) > 0 && (innerPayload[0]&0xF0) == 0x60 {
							// IPv6
							ipv6Packet := gopacket.NewPacket(innerPayload, layers.LayerTypeIPv6, gopacket.Default)
							for _, innerLayer := range ipv6Packet.Layers() {
								if innerLayer.LayerType() == gopacket.LayerTypeDecodeFailure {
									continue
								}
								innerConverted, err := p.convertLayerToIR(innerLayer, opts)
								if err != nil {
									return nil, fmt.Errorf("failed to convert GRE inner layer: %w", err)
								}
								for _, innerIR := range innerConverted {
									if innerIR != nil {
										irLayers = append(irLayers, *innerIR)
									}
								}
							}
						}
					}
				}
			}
		}

		// Special handling for IPv4 packets with DecodeFailure (malformed len field)
		// If gopacket couldn't parse IPv4 properly (nil addresses), manually extract it from raw bytes
		hasIPv4InIR := false
		for _, layer := range irLayers {
			if layer.Type == "IP" || layer.Type == "IPv4" {
				hasIPv4InIR = true
				break
			}
		}

		if !hasIPv4InIR && hasDecodeFailure {
			// Check if packet should have IPv4 based on EtherType
			ethLayer := pkt.Layer(layers.LayerTypeEthernet)
			vlanLayer := pkt.Layer(layers.LayerTypeDot1Q)

			var expectedEtherType layers.EthernetType
			if vlanLayer != nil {
				expectedEtherType = vlanLayer.(*layers.Dot1Q).Type
			} else if ethLayer != nil {
				expectedEtherType = ethLayer.(*layers.Ethernet).EthernetType
			}

			if expectedEtherType == layers.EthernetTypeIPv4 {
				// Calculate offset to IPv4 header
				ipv4Offset := 0
				if ethLayer != nil {
					ipv4Offset += 14
				}
				if vlanLayer != nil {
					ipv4Offset += 4
				}

				// Extract IPv4 header from raw bytes (minimum 20 bytes)
				if ipv4Offset+20 <= len(info.RawData) {
					ipv4Data := info.RawData[ipv4Offset:]

					// Verify it's IPv4 (version = 4)
					if len(ipv4Data) >= 20 && (ipv4Data[0]&0xF0) == 0x40 {
						// Extract IPv4 fields manually
						ihl := ipv4Data[0] & 0x0F
						tos := ipv4Data[1]
						totalLen := uint16(ipv4Data[2])<<8 | uint16(ipv4Data[3])
						id := uint16(ipv4Data[4])<<8 | uint16(ipv4Data[5])
						flagsAndOffset := uint16(ipv4Data[6])<<8 | uint16(ipv4Data[7])
						flags := (flagsAndOffset >> 13) & 0x07
						fragOffset := flagsAndOffset & 0x1FFF
						ttl := ipv4Data[8]
						protocol := ipv4Data[9]
						checksum := uint16(ipv4Data[10])<<8 | uint16(ipv4Data[11])
						srcIP := net.IP(ipv4Data[12:16])
						dstIP := net.IP(ipv4Data[16:20])

						// Create IPv4 IR layer
						ipv4Params := make(map[string]interface{})
						ipv4Params["src"] = srcIP.String()
						ipv4Params["dst"] = dstIP.String()
						ipv4Params["ttl"] = int(ttl)
						if tos != 0 {
							ipv4Params["tos"] = int(tos)
						}
						ipv4Params["id"] = int(id)
						if flags != 0 {
							ipv4Params["flags"] = int(flags)
						}
						if fragOffset != 0 {
							ipv4Params["frag"] = int(fragOffset)
						}
						ipv4Params["proto"] = int(protocol)
						ipv4Params["chksum"] = int(checksum)
						ipv4Params["len"] = int(totalLen)
						if ihl != 5 {
							ipv4Params["ihl"] = int(ihl)
						}

						// Extract IPv4 options if present
						if ihl > 5 {
							optionsLen := int(ihl)*4 - 20
							if 20+optionsLen <= len(ipv4Data) {
								optionsData := ipv4Data[20 : 20+optionsLen]
								// For non-standard options (like padding), store as dummy options
								// Check if all bytes are the same (common for padding/dummy options)
								isDummy := true
								if len(optionsData) > 0 {
									firstByte := optionsData[0]
									for _, b := range optionsData {
										if b != firstByte {
											isDummy = false
											break
										}
									}
								}

								if isDummy {
									// Store as raw options data (preserves exact bytes)
									ipv4Params["raw_options"] = fmt.Sprintf("%x", optionsData)
								} else {
									// Try to parse structured options
									var opts []map[string]interface{}
									i := 0
									for i < len(optionsData) {
										optType := optionsData[i]
										if optType == 0 || optType == 1 {
											// EOL or NOP
											opts = append(opts, map[string]interface{}{
												"type": int(optType),
											})
											i++
										} else if i+1 < len(optionsData) {
											// Option with length
											optLen := int(optionsData[i+1])
											opt := map[string]interface{}{
												"type": int(optType),
												"len":  optLen,
											}
											if i+optLen <= len(optionsData) && optLen > 2 {
												opt["data"] = fmt.Sprintf("%x", optionsData[i+2:i+optLen])
											}
											opts = append(opts, opt)
											i += optLen
										} else {
											break
										}
									}
									if len(opts) > 0 {
										ipv4Params["options"] = opts
									}
								}
							}
						}

						// Insert IPv4 layer after Ethernet/VLAN
						ipv4IR := IRLayer{
							Type:   "IPv4",
							Params: ipv4Params,
						}

						// Find insertion point (after Ethernet/VLAN)
						insertIdx := 0
						for i, layer := range irLayers {
							if layer.Type == "Ether" || layer.Type == "Dot1Q" {
								insertIdx = i + 1
							}
						}

						// Insert IPv4 layer
						irLayers = append(irLayers[:insertIdx], append([]IRLayer{ipv4IR}, irLayers[insertIdx:]...)...)

						// Extract IPv4 payload (data after IPv4 header)
						ipv4HeaderLen := int(ihl) * 4
						if ipv4Offset+ipv4HeaderLen < len(info.RawData) {
							payloadData := info.RawData[ipv4Offset+ipv4HeaderLen:]
							// Only add payload if it's within the declared total length
							declaredPayloadLen := int(totalLen) - ipv4HeaderLen
							if declaredPayloadLen > 0 && declaredPayloadLen <= len(payloadData) {
								payloadData = payloadData[:declaredPayloadLen]
							}

							// If protocol is TCP (6), try to manually parse TCP header
							if protocol == 6 && len(payloadData) >= 20 {
								sport := uint16(payloadData[0])<<8 | uint16(payloadData[1])
								dport := uint16(payloadData[2])<<8 | uint16(payloadData[3])
								seq := uint32(payloadData[4])<<24 | uint32(payloadData[5])<<16 | uint32(payloadData[6])<<8 | uint32(payloadData[7])
								ack := uint32(payloadData[8])<<24 | uint32(payloadData[9])<<16 | uint32(payloadData[10])<<8 | uint32(payloadData[11])
								dataOffset := (payloadData[12] >> 4) & 0x0F
								tcpFlags := payloadData[13]
								window := uint16(payloadData[14])<<8 | uint16(payloadData[15])
								chksum := uint16(payloadData[16])<<8 | uint16(payloadData[17])
								urgent := uint16(payloadData[18])<<8 | uint16(payloadData[19])

								tcpParams := make(map[string]interface{})
								tcpParams["sport"] = int(sport)
								tcpParams["dport"] = int(dport)
								tcpParams["seq"] = int(seq)
								tcpParams["ack"] = int(ack)
								if dataOffset > 0 {
									tcpParams["dataofs"] = int(dataOffset)
								}
								tcpParams["window"] = int(window)
								tcpParams["chksum"] = int(chksum)
								if urgent != 0 {
									tcpParams["urgptr"] = int(urgent)
								}

								// Parse TCP flags
								var flags []string
								if tcpFlags&0x01 != 0 {
									flags = append(flags, "F")
								}
								if tcpFlags&0x02 != 0 {
									flags = append(flags, "S")
								}
								if tcpFlags&0x04 != 0 {
									flags = append(flags, "R")
								}
								if tcpFlags&0x08 != 0 {
									flags = append(flags, "P")
								}
								if tcpFlags&0x10 != 0 {
									flags = append(flags, "A")
								}
								if tcpFlags&0x20 != 0 {
									flags = append(flags, "U")
								}
								if tcpFlags&0x40 != 0 {
									flags = append(flags, "E")
								}
								if tcpFlags&0x80 != 0 {
									flags = append(flags, "C")
								}
								tcpParams["flags"] = strings.Join(flags, "")

								// Extract TCP options if present
								if dataOffset > 5 && int(dataOffset)*4 <= len(payloadData) {
									optionsLen := int(dataOffset)*4 - 20
									if optionsLen > 0 && 20+optionsLen <= len(payloadData) {
										optionsData := payloadData[20 : 20+optionsLen]
										var opts []map[string]interface{}
										i := 0
										for i < len(optionsData) {
											optKind := optionsData[i]
											if optKind == 0 || optKind == 1 { // EOL or NOP
												opts = append(opts, map[string]interface{}{
													"kind": int(optKind),
												})
												i++
											} else if i+1 < len(optionsData) {
												optLen := int(optionsData[i+1])
												opt := map[string]interface{}{
													"kind": int(optKind),
													"len":  optLen,
												}
												if i+optLen <= len(optionsData) && optLen > 2 {
													opt["data"] = fmt.Sprintf("%x", optionsData[i+2:i+optLen])
												}
												opts = append(opts, opt)
												i += optLen
											} else {
												break
											}
										}
										if len(opts) > 0 {
											tcpParams["options"] = opts
										}
									}
								}

								tcpIR := IRLayer{
									Type:   "TCP",
									Params: tcpParams,
								}
								irLayers = append(irLayers, tcpIR)

								// Add remaining payload after TCP header
								tcpHeaderLen := int(dataOffset) * 4
								if tcpHeaderLen < len(payloadData) {
									tcpPayload := payloadData[tcpHeaderLen:]
									if len(tcpPayload) > 0 {
										rawPayloadIR := IRLayer{
											Type:   "Raw",
											Params: map[string]interface{}{"_arg0": string(tcpPayload)},
										}
										irLayers = append(irLayers, rawPayloadIR)
									}
								}
							} else if len(payloadData) > 0 {
								// For non-TCP or too short payload, add as Raw
								rawPayloadIR := IRLayer{
									Type:   "Raw",
									Params: map[string]interface{}{"_arg0": string(payloadData)},
								}
								irLayers = append(irLayers, rawPayloadIR)
							}
						}
					}
				}
			}
		}

		// If collapsing IPv6 to Raw, append the IPv6 payload bytes as a Raw layer
		if collapseIPv6ToRaw && len(ipv6PayloadBytes) > 0 {
			irLayers = append(irLayers, IRLayer{
				Type:   "Raw",
				Params: map[string]interface{}{"_arg0": string(ipv6PayloadBytes)},
			})
		}

		// Append application payload as Raw layer if present (only when not collapsing IPv6)
		if !collapseIPv6ToRaw {
			if app := pkt.ApplicationLayer(); app != nil {
				payload := app.Payload()
				if len(payload) > 0 {
					irLayers = append(irLayers, IRLayer{
						Type:   "Raw",
						Params: map[string]interface{}{"_arg0": string(payload)},
					})
				}
			}
		}

		// Fix IPv4 Total Length from raw bytes (gopacket corrects it during parsing)
		if ipv4Layer := pkt.Layer(layers.LayerTypeIPv4); ipv4Layer != nil {
			// Calculate offset to IPv4 header
			ipv4HeaderOffset := 0
			if ethLayer := pkt.Layer(layers.LayerTypeEthernet); ethLayer != nil {
				ipv4HeaderOffset += 14
			}
			if dot1qLayer := pkt.Layer(layers.LayerTypeDot1Q); dot1qLayer != nil {
				ipv4HeaderOffset += 4
			}

			// IPv4 Total Length is at offset 2-3 of IPv4 header
			if ipv4HeaderOffset+4 <= len(info.RawData) {
				rawTotalLen := uint16(info.RawData[ipv4HeaderOffset+2])<<8 | uint16(info.RawData[ipv4HeaderOffset+3])
				// Find IP layer in irLayers and update len parameter
				for i := range irLayers {
					if irLayers[i].Type == "IP" {
						irLayers[i].Params["len"] = int(rawTotalLen)
						break
					}
				}
			}
		}

		// Fix malformed TCP layers (zero ports) or missing TCP layers from raw bytes
		// This happens when gopacket fails to parse TCP due to incorrect IPv4 length field
		tcpFixed := false
		for tcpIdx := range irLayers {
			if irLayers[tcpIdx].Type == "TCP" {
				sport, _ := irLayers[tcpIdx].Params["sport"].(int)
				dport, _ := irLayers[tcpIdx].Params["dport"].(int)

				// If both ports are zero, TCP parsing failed - try to recover from raw bytes
				if sport == 0 && dport == 0 && hasDecodeFailure {
					// Find IPv4 or IPv6 layer index
					ipv4Idx := -1
					ipv6Idx := -1
					for i := range irLayers {
						if irLayers[i].Type == "IPv4" || irLayers[i].Type == "IP" {
							ipv4Idx = i
							break
						}
						if irLayers[i].Type == "IPv6" {
							ipv6Idx = i
							break
						}
					}

					tcpOffset := 0

					// Calculate TCP offset for IPv4
					if ipv4Idx >= 0 {
						if ethLayer := pkt.Layer(layers.LayerTypeEthernet); ethLayer != nil {
							tcpOffset += 14
						}
						if dot1qLayer := pkt.Layer(layers.LayerTypeDot1Q); dot1qLayer != nil {
							tcpOffset += 4
						}

						// Add IPv4 header length
						ihl, _ := irLayers[ipv4Idx].Params["ihl"].(int)
						if ihl == 0 {
							ihl = 5 // default
						}
						tcpOffset += ihl * 4
					} else if ipv6Idx >= 0 {
						// Calculate TCP offset for IPv6
						if ethLayer := pkt.Layer(layers.LayerTypeEthernet); ethLayer != nil {
							tcpOffset += 14
						}
						if dot1qLayer := pkt.Layer(layers.LayerTypeDot1Q); dot1qLayer != nil {
							tcpOffset += 4
						}

						// IPv6 header is 40 bytes
						tcpOffset += 40

						// Add extension headers length (all Raw layers between IPv6 and TCP)
						for i := ipv6Idx + 1; i < tcpIdx; i++ {
							if irLayers[i].Type == "Raw" {
								if arg, ok := irLayers[i].Params["_arg0"].(string); ok {
									tcpOffset += len(arg)
								}
							}
						}
					}

					if tcpOffset > 0 && tcpOffset+20 <= len(info.RawData) {
						tcpData := info.RawData[tcpOffset:]

						sport := uint16(tcpData[0])<<8 | uint16(tcpData[1])
						dport := uint16(tcpData[2])<<8 | uint16(tcpData[3])
						seq := uint32(tcpData[4])<<24 | uint32(tcpData[5])<<16 | uint32(tcpData[6])<<8 | uint32(tcpData[7])
						ack := uint32(tcpData[8])<<24 | uint32(tcpData[9])<<16 | uint32(tcpData[10])<<8 | uint32(tcpData[11])
						dataOffset := (tcpData[12] >> 4) & 0x0F
						tcpFlags := tcpData[13]
						window := uint16(tcpData[14])<<8 | uint16(tcpData[15])
						chksum := uint16(tcpData[16])<<8 | uint16(tcpData[17])
						urgent := uint16(tcpData[18])<<8 | uint16(tcpData[19])

						// Update TCP params
						irLayers[tcpIdx].Params["sport"] = int(sport)
						irLayers[tcpIdx].Params["dport"] = int(dport)
						irLayers[tcpIdx].Params["seq"] = int(seq)
						irLayers[tcpIdx].Params["ack"] = int(ack)
						if dataOffset > 0 {
							irLayers[tcpIdx].Params["dataofs"] = int(dataOffset)
						}
						irLayers[tcpIdx].Params["window"] = int(window)
						irLayers[tcpIdx].Params["chksum"] = int(chksum)
						if urgent != 0 {
							irLayers[tcpIdx].Params["urgptr"] = int(urgent)
						}

						// Parse TCP flags
						var flags []string
						if tcpFlags&0x01 != 0 {
							flags = append(flags, "F")
						}
						if tcpFlags&0x02 != 0 {
							flags = append(flags, "S")
						}
						if tcpFlags&0x04 != 0 {
							flags = append(flags, "R")
						}
						if tcpFlags&0x08 != 0 {
							flags = append(flags, "P")
						}
						if tcpFlags&0x10 != 0 {
							flags = append(flags, "A")
						}
						if tcpFlags&0x20 != 0 {
							flags = append(flags, "U")
						}
						if tcpFlags&0x40 != 0 {
							flags = append(flags, "E")
						}
						if tcpFlags&0x80 != 0 {
							flags = append(flags, "C")
						}
						irLayers[tcpIdx].Params["flags"] = strings.Join(flags, "")
					}

					tcpFixed = true
					break // Only fix first malformed TCP
				}
			}
		}

		// Handle case where TCP layer is completely missing (replaced by Raw)
		// This happens when IPv4 proto=6 but payload is too short for gopacket to parse TCP
		// OR when IPv6 has extension headers and TCP is at the end
		if !tcpFixed && hasDecodeFailure {
			// Case 1: Find IPv4 layer with proto=6 followed by Raw
			for i := range irLayers {
				if (irLayers[i].Type == "IPv4" || irLayers[i].Type == "IP") && i+1 < len(irLayers) {
					proto, _ := irLayers[i].Params["proto"].(int)
					if proto == 6 && irLayers[i+1].Type == "Raw" {
						// Try to parse TCP from raw bytes
						tcpOffset := 0
						if ethLayer := pkt.Layer(layers.LayerTypeEthernet); ethLayer != nil {
							tcpOffset += 14
						}
						if dot1qLayer := pkt.Layer(layers.LayerTypeDot1Q); dot1qLayer != nil {
							tcpOffset += 4
						}

						// Add IPv4 header length
						ihl, _ := irLayers[i].Params["ihl"].(int)
						if ihl == 0 {
							ihl = 5 // default
						}
						tcpOffset += ihl * 4

						// Try to parse TCP from raw bytes
						if tcpOffset+20 <= len(info.RawData) {
							tcpData := info.RawData[tcpOffset:]

							sport := uint16(tcpData[0])<<8 | uint16(tcpData[1])
							dport := uint16(tcpData[2])<<8 | uint16(tcpData[3])
							seq := uint32(tcpData[4])<<24 | uint32(tcpData[5])<<16 | uint32(tcpData[6])<<8 | uint32(tcpData[7])
							ack := uint32(tcpData[8])<<24 | uint32(tcpData[9])<<16 | uint32(tcpData[10])<<8 | uint32(tcpData[11])
							dataOffset := (tcpData[12] >> 4) & 0x0F
							tcpFlags := tcpData[13]
							window := uint16(tcpData[14])<<8 | uint16(tcpData[15])
							chksum := uint16(tcpData[16])<<8 | uint16(tcpData[17])
							urgent := uint16(tcpData[18])<<8 | uint16(tcpData[19])

							tcpParams := make(map[string]interface{})
							tcpParams["sport"] = int(sport)
							tcpParams["dport"] = int(dport)
							tcpParams["seq"] = int(seq)
							tcpParams["ack"] = int(ack)
							if dataOffset > 0 {
								tcpParams["dataofs"] = int(dataOffset)
							}
							tcpParams["window"] = int(window)
							tcpParams["chksum"] = int(chksum)
							if urgent != 0 {
								tcpParams["urgptr"] = int(urgent)
							}

							// Parse TCP flags
							var flags []string
							if tcpFlags&0x01 != 0 {
								flags = append(flags, "F")
							}
							if tcpFlags&0x02 != 0 {
								flags = append(flags, "S")
							}
							if tcpFlags&0x04 != 0 {
								flags = append(flags, "R")
							}
							if tcpFlags&0x08 != 0 {
								flags = append(flags, "P")
							}
							if tcpFlags&0x10 != 0 {
								flags = append(flags, "A")
							}
							if tcpFlags&0x20 != 0 {
								flags = append(flags, "U")
							}
							if tcpFlags&0x40 != 0 {
								flags = append(flags, "E")
							}
							if tcpFlags&0x80 != 0 {
								flags = append(flags, "C")
							}
							tcpParams["flags"] = strings.Join(flags, "")

							// Replace Raw layer with TCP
							tcpIR := IRLayer{
								Type:   "TCP",
								Params: tcpParams,
							}
							irLayers[i+1] = tcpIR
						}

						break // Only fix first missing TCP
					}
				}
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

// ipv6MalformationInfo contains information about IPv6 malformed packets
type ipv6MalformationInfo struct {
	collapseToRaw bool
	payloadBytes  []byte
	hasExtHeaders bool
}

// detectIPv6Malformation checks if an IPv6 packet has malformed payload length or extension headers
func (p *PcapAnalyzer) detectIPv6Malformation(pkt gopacket.Packet, info *PacketInfo) *ipv6MalformationInfo {
	result := &ipv6MalformationInfo{}

	ipv6Layer := pkt.Layer(layers.LayerTypeIPv6)
	if ipv6Layer == nil {
		return result
	}

	ipv6 := ipv6Layer.(*layers.IPv6)
	expectedPayloadLen := int(ipv6.Length)
	ipv6PayloadBytes := ipv6.LayerPayload()
	actualPayloadLen := len(ipv6PayloadBytes)
	result.payloadBytes = ipv6PayloadBytes

	// Determine if any transport was parsed beyond IPv6
	hasTransport := pkt.Layer(layers.LayerTypeTCP) != nil ||
		pkt.Layer(layers.LayerTypeUDP) != nil ||
		pkt.Layer(layers.LayerTypeICMPv6) != nil ||
		pkt.Layer(layers.LayerTypeICMPv4) != nil

	// Check if packet has IPv6 extension headers
	result.hasExtHeaders = pkt.Layer(layers.LayerTypeIPv6Destination) != nil ||
		pkt.Layer(layers.LayerTypeIPv6HopByHop) != nil ||
		pkt.Layer(layers.LayerTypeIPv6Routing) != nil ||
		pkt.Layer(layers.LayerTypeIPv6Fragment) != nil

	// Check if we need to extract actual payload from raw data
	if (ipv6.NextHeader == 0x1B || expectedPayloadLen != actualPayloadLen || result.hasExtHeaders) && !hasTransport {
		// Calculate the offset of IPv6 payload in raw data
		var payloadOffset int

		// Find Ethernet layer
		if pkt.Layer(layers.LayerTypeEthernet) != nil {
			payloadOffset += 14 // Ethernet header

			// Check for VLAN
			if pkt.Layer(layers.LayerTypeDot1Q) != nil {
				payloadOffset += 4 // VLAN tag
			}

			payloadOffset += 40 // IPv6 header

			// Extract payload bytes from raw data
			if payloadOffset < len(info.RawData) {
				actualPayloadFromRaw := info.RawData[payloadOffset:]
				if len(actualPayloadFromRaw) > 0 {
					result.payloadBytes = actualPayloadFromRaw
					result.collapseToRaw = true
				}
			}
		}
	}

	return result
}

// greDecodeFailureInfo contains information about GRE decode failures
type greDecodeFailureInfo struct {
	hasGRE           bool
	hasDecodeFailure bool
	hasIPv6WithGRE   bool
}

// detectGREDecodeFailure checks if a packet has GRE with decode failures
func (p *PcapAnalyzer) detectGREDecodeFailure(pkt gopacket.Packet) *greDecodeFailureInfo {
	result := &greDecodeFailureInfo{
		hasGRE: pkt.Layer(layers.LayerTypeGRE) != nil,
	}

	for _, layer := range pkt.Layers() {
		if layer.LayerType() == gopacket.LayerTypeDecodeFailure {
			result.hasDecodeFailure = true
		}
		if layer.LayerType() == layers.LayerTypeIPv6 {
			ipv6 := layer.(*layers.IPv6)
			if ipv6.NextHeader == layers.IPProtocolGRE {
				result.hasIPv6WithGRE = true
			}
		}
	}

	return result
}

// handleGREWithDecodeFailure processes GRE packets that have decode failures
func (p *PcapAnalyzer) handleGREWithDecodeFailure(greLayer *layers.GRE, info *PacketInfo, opts CodegenOpts) ([]IRLayer, error) {
	var result []IRLayer

	// Get the full packet data and find GRE layer position
	rawData := info.RawData
	greContents := greLayer.LayerContents()

	// Find GRE header in raw data
	var actualPayload []byte
	for i := 0; i < len(rawData)-len(greContents); i++ {
		// Check if we found the GRE header
		if len(greContents) >= 4 && i+len(greContents) <= len(rawData) {
			match := true
			for j := 0; j < len(greContents) && j < 4; j++ {
				if rawData[i+j] != greContents[j] {
					match = false
					break
				}
			}
			if match {
				// Found GRE header, now find IP header after it
				for j := i + 4; j < len(rawData)-MinIPv4HeaderSize && j < i+16; j++ {
					if rawData[j] == 0x45 || (rawData[j]&0xF0) == 0x40 || (rawData[j]&0xF0) == 0x60 {
						actualPayload = rawData[j:]
						break
					}
				}
				break
			}
		}
	}

	// Try to parse as IPv4 if protocol is 0x0800
	if greLayer.Protocol == layers.EthernetTypeIPv4 && len(actualPayload) > 0 {
		ipv4Packet := gopacket.NewPacket(actualPayload, layers.LayerTypeIPv4, gopacket.Default)
		for _, innerLayer := range ipv4Packet.Layers() {
			if innerLayer.LayerType() == gopacket.LayerTypeDecodeFailure {
				continue
			}
			innerConverted, err := p.convertLayerToIR(innerLayer, opts)
			if err != nil {
				return nil, fmt.Errorf("failed to convert GRE inner layer: %w", err)
			}
			for _, innerIR := range innerConverted {
				if innerIR != nil {
					result = append(result, *innerIR)
				}
			}
		}
	} else if greLayer.Protocol == layers.EthernetTypeIPv6 && len(actualPayload) > 0 {
		// Try to parse as IPv6 if protocol is 0x86DD
		ipv6Packet := gopacket.NewPacket(actualPayload, layers.LayerTypeIPv6, gopacket.Default)
		for _, innerLayer := range ipv6Packet.Layers() {
			if innerLayer.LayerType() == gopacket.LayerTypeDecodeFailure {
				continue
			}
			innerConverted, err := p.convertLayerToIR(innerLayer, opts)
			if err != nil {
				return nil, fmt.Errorf("failed to convert GRE inner layer: %w", err)
			}
			for _, innerIR := range innerConverted {
				if innerIR != nil {
					result = append(result, *innerIR)
				}
			}
		}
	}

	return result, nil
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
	case *layers.GRE:
		return []*IRLayer{p.convertGREToIR(l)}, nil
	case *layers.IPv6Fragment:
		return []*IRLayer{p.convertIPv6FragmentToIR(l)}, nil
	case *layers.IPv6Destination:
		return []*IRLayer{p.convertIPv6DestinationToIR(l)}, nil
	case *layers.IPv6HopByHop:
		return []*IRLayer{p.convertIPv6HopByHopToIR(l)}, nil
	case *layers.IPv6Routing:
		return []*IRLayer{p.convertIPv6RoutingToIR(l)}, nil
	case *layers.ARP:
		return []*IRLayer{p.convertARPToIR(l)}, nil
	case *layers.ICMPv6RouterSolicitation:
		return []*IRLayer{p.convertICMPv6RouterSolicitationToIR(l)}, nil
	case *layers.ICMPv6RouterAdvertisement:
		return []*IRLayer{p.convertICMPv6RouterAdvertisementToIR(l)}, nil
	case *layers.ICMPv6NeighborSolicitation:
		return []*IRLayer{p.convertICMPv6NeighborSolicitationToIR(l)}, nil
	case *layers.ICMPv6NeighborAdvertisement:
		return []*IRLayer{p.convertICMPv6NeighborAdvertisementToIR(l)}, nil
	case *layers.IPSecESP:
		return []*IRLayer{p.convertIPSecESPToIR(l)}, nil
	case *layers.IPSecAH:
		return []*IRLayer{p.convertIPSecAHToIR(l)}, nil
	case gopacket.Payload:
		// Always reflect application payload as a Raw layer to keep layer count parity with original
		return []*IRLayer{p.convertPayloadToIR(l)}, nil
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
	// Skip packets with missing IP addresses (happens with some GRE encapsulation issues)
	if ipv4.SrcIP == nil || ipv4.DstIP == nil {
		return nil
	}

	params := make(map[string]interface{})

	params["src"] = ipv4.SrcIP.String()
	params["dst"] = ipv4.DstIP.String()
	params["ttl"] = int(ipv4.TTL)

	if ipv4.TOS != 0 {
		params["tos"] = int(ipv4.TOS)
	}
	// Preserve IP ID even if zero to avoid defaulting to 1 in builder
	params["id"] = int(ipv4.Id)
	// Always preserve protocol field from PCAP (even if 0) for exact packet reconstruction
	params["proto"] = int(ipv4.Protocol)
	// Always preserve checksum from PCAP (even if 0) for exact reconstruction
	// Read from raw bytes to avoid gopacket's automatic corrections
	if len(ipv4.Contents) >= 12 {
		// IPv4 checksum is at bytes 10-11 of the header
		rawChecksum := int(ipv4.Contents[10])<<8 | int(ipv4.Contents[11])
		params["chksum"] = rawChecksum
	} else {
		params["chksum"] = int(ipv4.Checksum)
	}
	// Always preserve length from PCAP (even if 0) for malformed packet testing
	// Read from raw bytes to avoid gopacket's automatic corrections
	if len(ipv4.Contents) >= 4 {
		// IPv4 length is at bytes 2-3 of the header
		rawLength := int(ipv4.Contents[2])<<8 | int(ipv4.Contents[3])
		params["len"] = rawLength
	} else {
		params["len"] = int(ipv4.Length)
	}
	// Preserve IHL for packets with options
	if ipv4.IHL != 5 {
		params["ihl"] = int(ipv4.IHL)
	}
	// Extract IPv4 options as structured data
	if len(ipv4.Options) > 0 {
		var opts []map[string]interface{}
		for _, opt := range ipv4.Options {
			optMap := map[string]interface{}{
				"type": int(opt.OptionType),
			}
			if opt.OptionLength > 0 {
				optMap["len"] = int(opt.OptionLength)
			}
			if len(opt.OptionData) > 0 {
				// Store as hex string for JSON serialization
				optMap["data"] = fmt.Sprintf("%x", opt.OptionData)
			}
			opts = append(opts, optMap)
		}
		params["options"] = opts
	}
	if ipv4.Flags != 0 {
		params["flags"] = int(ipv4.Flags)
	}
	if ipv4.FragOffset != 0 {
		params["frag"] = int(ipv4.FragOffset)
	}

	return &IRLayer{
		Type:   "IPv4",
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
	// Always preserve NextHeader field from PCAP (even if 0) for exact packet reconstruction
	params["nh"] = int(ipv6.NextHeader)
	// Always preserve Length from PCAP (even if 0) for malformed packet testing
	params["plen"] = int(ipv6.Length)

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
	// Preserve checksum as seen in PCAP (including zero)
	// Read from raw bytes to avoid gopacket's automatic corrections
	if len(tcp.Contents) >= 18 {
		// TCP checksum is at bytes 16-17 of the header
		rawChecksum := int(tcp.Contents[16])<<8 | int(tcp.Contents[17])
		params["chksum"] = rawChecksum
	} else {
		params["chksum"] = int(tcp.Checksum)
	}
	// Preserve DataOffset for packets with options
	if tcp.DataOffset != 5 {
		params["dataofs"] = int(tcp.DataOffset)
	}
	// Extract TCP options as structured data
	if len(tcp.Options) > 0 {
		var opts []map[string]interface{}
		for _, opt := range tcp.Options {
			optMap := map[string]interface{}{
				"kind": int(opt.OptionType),
			}
			if opt.OptionLength > 0 {
				optMap["len"] = int(opt.OptionLength)
			}
			if len(opt.OptionData) > 0 {
				// Store as hex string for JSON serialization
				optMap["data"] = fmt.Sprintf("%x", opt.OptionData)
			}
			opts = append(opts, optMap)
		}
		params["options"] = opts
	}

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
	// Always store flags string, even if empty (to distinguish "no flags" from "not captured")
	params["flags"] = strings.Join(flags, "")

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
	// Preserve length exactly as in the PCAP header (including malformed values).
	// UDP length field is at bytes 4-5 of the header.
	if len(udp.Contents) >= 6 {
		rawLen := int(udp.Contents[4])<<8 | int(udp.Contents[5])
		params["len"] = rawLen
	} else if udp.Length != 0 {
		// Fallback to parsed Length when raw header bytes are unavailable.
		params["len"] = int(udp.Length)
	}
	// Preserve checksum as seen in PCAP (including zero)
	// Read from raw bytes to avoid gopacket's automatic corrections
	if len(udp.Contents) >= 8 {
		// UDP checksum is at bytes 6-7 of the header
		rawChecksum := int(udp.Contents[6])<<8 | int(udp.Contents[7])
		params["chksum"] = rawChecksum
	} else {
		params["chksum"] = int(udp.Checksum)
	}

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
	// Always preserve checksum from PCAP for exact reconstruction
	// Read from raw bytes to avoid gopacket's automatic corrections
	if len(icmp.Contents) >= 4 {
		// ICMP checksum is at bytes 2-3 of the header
		rawChecksum := int(icmp.Contents[2])<<8 | int(icmp.Contents[3])
		params["chksum"] = rawChecksum
	} else {
		params["chksum"] = int(icmp.Checksum)
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

	// Preserve type and code for generic ICMPv6 control messages
	params["type"] = icmpType
	if code != 0 {
		params["code"] = code
	}

	// Always preserve checksum from PCAP for exact reconstruction
	// Read from raw bytes to avoid gopacket's automatic corrections
	if len(icmp.Contents) >= 4 {
		// ICMPv6 checksum is at bytes 2-3 of the header
		rawChecksum := int(icmp.Contents[2])<<8 | int(icmp.Contents[3])
		params["chksum"] = rawChecksum
	} else {
		params["chksum"] = int(icmp.Checksum)
	}

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
	case 2: // Packet Too Big
		layerType = "ICMPv6PacketTooBig"
		// Extract MTU from first 4 bytes of payload
		pl := icmp.LayerPayload()
		if len(pl) >= 4 {
			mtu := uint32(pl[0])<<24 | uint32(pl[1])<<16 | uint32(pl[2])<<8 | uint32(pl[3])
			params["mtu"] = int(mtu)
		}
	case 3: // Time Exceeded
		layerType = "ICMPv6TimeExceeded"
		params["code"] = code
	case 4: // Parameter Problem
		layerType = "ICMPv6ParamProblem"
		params["code"] = code
		// Extract pointer from first 4 bytes of payload
		pl := icmp.LayerPayload()
		if len(pl) >= 4 {
			pointer := uint32(pl[0])<<24 | uint32(pl[1])<<16 | uint32(pl[2])<<8 | uint32(pl[3])
			params["pointer"] = int(pointer)
		}
	default:
		// Generic ICMPv6 control message (e.g., Router Solicitation, Router Advertisement)
		layerType = "ICMPv6"
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

	// Extract payload for different ICMPv6 message types
	result := []*IRLayer{icmpLayer}
	payload := icmp.LayerPayload()

	// For Echo Request/Reply, skip 4-byte header (Id+Seq) and extract remaining payload
	if icmpType == 128 || icmpType == 129 {
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

	// For error messages (Dest Unreach, Packet Too Big, Time Exceeded, Param Problem),
	// DON'T extract the embedded packet here - it will be added as ApplicationLayer
	// The packet-level logic will handle stopping after ICMPv6 error

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

// convertGREToIR converts GRE layer to IR
func (p *PcapAnalyzer) convertGREToIR(gre *layers.GRE) *IRLayer {
	params := make(map[string]interface{})

	// Always preserve protocol field from PCAP (even if 0) for exact packet reconstruction
	params["proto"] = int(gre.Protocol)
	if gre.ChecksumPresent {
		params["chksum_present"] = 1
		// Always preserve the actual checksum value from PCAP
		params["chksum"] = int(gre.Checksum)
	} else {
		params["chksum_present"] = 0
	}
	if gre.KeyPresent {
		params["key_present"] = 1
		if gre.Key != 0 {
			params["key"] = int(gre.Key)
		}
	} else {
		params["key_present"] = 0
	}
	if gre.SeqPresent {
		params["seqnum_present"] = 1
		if gre.Seq != 0 {
			params["seq"] = int(gre.Seq)
		}
	} else {
		params["seqnum_present"] = 0
	}
	if gre.RoutingPresent {
		params["routing_present"] = 1
	} else {
		params["routing_present"] = 0
	}
	if gre.Version != 0 {
		params["version"] = int(gre.Version)
	}

	// Extract raw flags from GRE header for unsupported flags (e.g., ack present)
	// GRE header: 2 bytes flags + 2 bytes protocol
	greContents := gre.LayerContents()
	if len(greContents) >= 2 {
		// First 2 bytes are flags
		flags := uint16(greContents[0])<<8 | uint16(greContents[1])
		// Store raw flags for exact reconstruction
		params["raw_flags"] = int(flags)
	}

	return &IRLayer{
		Type:   "GRE",
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
	// Always preserve NextHeader field from PCAP (even if 0) for exact packet reconstruction
	params["nh"] = int(frag.NextHeader)

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

// convertARPToIR converts ARP layer to IR
func (p *PcapAnalyzer) convertARPToIR(arp *layers.ARP) *IRLayer {
	params := make(map[string]interface{})

	params["operation"] = int(arp.Operation)
	params["hwtype"] = int(arp.AddrType)
	params["ptype"] = int(arp.Protocol)
	params["hwlen"] = int(arp.HwAddressSize)
	params["plen"] = int(arp.ProtAddressSize)
	params["hwsrc"] = net.HardwareAddr(arp.SourceHwAddress).String()
	params["psrc"] = net.IP(arp.SourceProtAddress).String()
	params["hwdst"] = net.HardwareAddr(arp.DstHwAddress).String()
	params["pdst"] = net.IP(arp.DstProtAddress).String()

	return &IRLayer{
		Type:   "ARP",
		Params: params,
	}
}

// convertICMPv6RouterSolicitationToIR converts ICMPv6 Router Solicitation to IR
func (p *PcapAnalyzer) convertICMPv6RouterSolicitationToIR(rs *layers.ICMPv6RouterSolicitation) *IRLayer {
	params := make(map[string]interface{})

	// Router Solicitation has minimal fields
	if len(rs.Options) > 0 {
		var opts []map[string]interface{}
		for _, opt := range rs.Options {
			optMap := map[string]interface{}{
				"type": int(opt.Type),
			}
			if len(opt.Data) > 0 {
				optMap["data"] = fmt.Sprintf("%x", opt.Data)
			}
			opts = append(opts, optMap)
		}
		params["options"] = opts
	}

	return &IRLayer{
		Type:   "ICMPv6RouterSolicitation",
		Params: params,
	}
}

// convertICMPv6RouterAdvertisementToIR converts ICMPv6 Router Advertisement to IR
func (p *PcapAnalyzer) convertICMPv6RouterAdvertisementToIR(ra *layers.ICMPv6RouterAdvertisement) *IRLayer {
	params := make(map[string]interface{})

	params["hoplimit"] = int(ra.HopLimit)
	params["flags"] = int(ra.Flags)
	params["routerlifetime"] = int(ra.RouterLifetime)
	params["reachabletime"] = int(ra.ReachableTime)
	params["retranstimer"] = int(ra.RetransTimer)

	if len(ra.Options) > 0 {
		var opts []map[string]interface{}
		for _, opt := range ra.Options {
			optMap := map[string]interface{}{
				"type": int(opt.Type),
			}
			if len(opt.Data) > 0 {
				optMap["data"] = fmt.Sprintf("%x", opt.Data)
			}
			opts = append(opts, optMap)
		}
		params["options"] = opts
	}

	return &IRLayer{
		Type:   "ICMPv6RouterAdvertisement",
		Params: params,
	}
}

// convertICMPv6NeighborSolicitationToIR converts ICMPv6 Neighbor Solicitation to IR
func (p *PcapAnalyzer) convertICMPv6NeighborSolicitationToIR(ns *layers.ICMPv6NeighborSolicitation) *IRLayer {
	params := make(map[string]interface{})

	params["targetaddress"] = ns.TargetAddress.String()

	if len(ns.Options) > 0 {
		var opts []map[string]interface{}
		for _, opt := range ns.Options {
			optMap := map[string]interface{}{
				"type": int(opt.Type),
			}
			if len(opt.Data) > 0 {
				optMap["data"] = fmt.Sprintf("%x", opt.Data)
			}
			opts = append(opts, optMap)
		}
		params["options"] = opts
	}

	return &IRLayer{
		Type:   "ICMPv6NeighborSolicitation",
		Params: params,
	}
}

// convertICMPv6NeighborAdvertisementToIR converts ICMPv6 Neighbor Advertisement to IR
func (p *PcapAnalyzer) convertICMPv6NeighborAdvertisementToIR(na *layers.ICMPv6NeighborAdvertisement) *IRLayer {
	params := make(map[string]interface{})

	params["flags"] = int(na.Flags)
	params["targetaddress"] = na.TargetAddress.String()

	if len(na.Options) > 0 {
		var opts []map[string]interface{}
		for _, opt := range na.Options {
			optMap := map[string]interface{}{
				"type": int(opt.Type),
			}
			if len(opt.Data) > 0 {
				optMap["data"] = fmt.Sprintf("%x", opt.Data)
			}
			opts = append(opts, optMap)
		}
		params["options"] = opts
	}

	return &IRLayer{
		Type:   "ICMPv6NeighborAdvertisement",
		Params: params,
	}
}

// convertIPSecESPToIR converts IPSec ESP layer to IR
func (p *PcapAnalyzer) convertIPSecESPToIR(esp *layers.IPSecESP) *IRLayer {
	params := make(map[string]interface{})

	params["spi"] = int(esp.SPI)
	params["seq"] = int(esp.Seq)

	// Store encrypted data as hex string
	if len(esp.Encrypted) > 0 {
		params["encrypted"] = fmt.Sprintf("%x", esp.Encrypted)
	}

	return &IRLayer{
		Type:   "IPSecESP",
		Params: params,
	}
}

// convertIPSecAHToIR converts IPSec AH layer to IR
func (p *PcapAnalyzer) convertIPSecAHToIR(ah *layers.IPSecAH) *IRLayer {
	params := make(map[string]interface{})

	params["spi"] = int(ah.SPI)
	params["seq"] = int(ah.Seq)
	params["nextheader"] = int(ah.NextHeader)
	params["reserved"] = int(ah.Reserved)

	// Store Authentication Data as hex string
	if len(ah.AuthenticationData) > 0 {
		params["authdata"] = fmt.Sprintf("%x", ah.AuthenticationData)
	}

	return &IRLayer{
		Type:   "IPSecAH",
		Params: params,
	}
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

// mplsLabel was unused and removed
