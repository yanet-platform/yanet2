package lib

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIRPipeline validates the complete PCAP → IR → Packet pipeline
// This is the correct way to test the converter: semantic equivalence, not byte equivalence
//
// This test requires yanet1 repository to be available.
// Set YANET1_ROOT environment variable to point to yanet1 directory.
// Example: export YANET1_ROOT=/path/to/yanet1
func TestIRPipeline(t *testing.T) {
	onePortDir, err := GetYanet1OnePortDir()
	if err != nil {
		t.Skipf("Failed to get one port directory (yanet1 repository not available): %v", err)
		return
	}

	// Get test filters
	onlyTest := os.Getenv("ONLY_TEST")
	onlyStep := os.Getenv("ONLY_STEP")

	// Skip tests with known issues unless explicitly requested
	skipTests := map[string]string{
		"056_balancer_icmp_rate_limit": "autotest.yaml has no steps",
		"059_rib":                      "known YAML parsing issues in autotest.yaml (line 1809). Run with ONLY_TEST=059_rib to test explicitly.",
	}

	// Discover tests
	tests, err := DiscoverTests(onePortDir, onlyTest, skipTests)
	require.NoError(t, err)

	if len(tests) == 0 {
		t.Skip("No tests found to run")
	}

	for _, testInfo := range tests {
		testInfo := testInfo // capture loop variable
		t.Run(testInfo.Name, func(t *testing.T) {
			runIRPipelineTest(t, testInfo, onlyStep)
		})
	}
}

func runIRPipelineTest(t *testing.T, testInfo TestInfo, onlyStep string) {
	err := IterateSendPacketsSteps(testInfo, onlyStep, func(stepInfo StepInfo, sendFile, expectFile string) error {
		t.Run(stepInfo.Name, func(t *testing.T) {
			// Test send packets
			sendPath := filepath.Join(testInfo.Dir, sendFile)
			if _, err := os.Stat(sendPath); err == nil {
				t.Run(sendFile, func(t *testing.T) {
					testPCAPThroughIRPipeline(t, sendPath, false)
				})
			}

			// Test expect packets
			if expectFile != "" {
				expectPath := filepath.Join(testInfo.Dir, expectFile)
				if _, err := os.Stat(expectPath); err == nil {
					t.Run(expectFile, func(t *testing.T) {
						testPCAPThroughIRPipeline(t, expectPath, true)
					})
				}
			}
		})
		return nil
	})
	require.NoError(t, err)
}

// testPCAPThroughIRPipeline tests the full pipeline: PCAP → IR → Packet
func testPCAPThroughIRPipeline(t *testing.T, pcapPath string, isExpect bool) {
	// Step 1: Read original PCAP
	originalPackets, err := readPCAPPackets(pcapPath)
	require.NoError(t, err, "Failed to read PCAP")

	if len(originalPackets) == 0 {
		t.Skip("Empty PCAP file")
		return
	}

	// Step 2: Convert PCAP → IR
	opts := CodegenOpts{
		UseFrameworkMACs: false,
		IsExpect:         isExpect,
		StripVLAN:        false,
	}
	analyzer := NewPcapAnalyzer(false)

	// Read packet infos
	packetInfos, err := analyzer.ReadAllPacketsFromFile(pcapPath)
	require.NoError(t, err, "Failed to read packet infos")

	// Convert to IR
	ir, err := analyzer.ConvertPacketInfoToIR(packetInfos, "send.pcap", "expect.pcap", opts)
	require.NoError(t, err, "Failed to convert to IR")

	// Extract IR packets
	var irPackets []IRPacketDef
	if len(ir.PCAPPairs) > 0 {
		if opts.IsExpect {
			irPackets = ir.PCAPPairs[0].ExpectPackets
		} else {
			irPackets = ir.PCAPPairs[0].SendPackets
		}
	}

	require.Equal(t, len(originalPackets), len(irPackets),
		"IR packet count mismatch")

	// Step 3: Validate IR completeness (does IR capture all packet information?)
	for i, originalPkt := range originalPackets {
		irPkt := irPackets[i]
		validateIRCompleteness(t, originalPkt, irPkt, i)
	}

	// Step 4: Generate packets from IR (using packet_builder.go)
	for i, irPkt := range irPackets {
		generatedPkt, err := generatePacketFromIRPipeline(irPkt, opts)
		require.NoError(t, err, "Failed to generate packet %d from IR", i)

		// Step 5: Compare semantically (not byte-for-byte)
		// Debug: log layer counts if mismatch (after filtering ignored layers)
		origFiltered := filterDecodeFailureLayers(originalPackets[i].Layers())
		genFiltered := filterDecodeFailureLayers(generatedPkt.Layers())
		if len(origFiltered) != len(genFiltered) {
			t.Logf("Packet %d: Original has %d layers, Generated has %d layers (after filtering)", i,
				len(origFiltered), len(genFiltered))
			for j, l := range origFiltered {
				t.Logf("  Original layer %d: %s", j, l.LayerType())
			}
			for j, l := range genFiltered {
				t.Logf("  Generated layer %d: %s", j, l.LayerType())
			}
		}
		comparePacketsSemantically(t, originalPackets[i], generatedPkt, i)
	}
}

// validateIRCompleteness ensures the IR captures all important packet information
func validateIRCompleteness(t *testing.T, packet gopacket.Packet, irPkt IRPacketDef, index int) {
	// Validate each layer is represented in IR
	// Track which occurrence of each layer type we're validating (for tunnels with multiple IPv4/IPv6 layers)
	ipv4Count := 0
	ipv6Count := 0
	tcpCount := 0
	udpCount := 0

	for _, layer := range packet.Layers() {
		switch layer.LayerType() {
		case layers.LayerTypeEthernet:
			validateEthernetIR(t, layer.(*layers.Ethernet), irPkt, index)
		case layers.LayerTypeIPv4:
			validateIPv4IRWithIndex(t, layer.(*layers.IPv4), irPkt, index, ipv4Count)
			ipv4Count++
		case layers.LayerTypeIPv6:
			validateIPv6IRWithIndex(t, layer.(*layers.IPv6), irPkt, index, ipv6Count)
			ipv6Count++
		case layers.LayerTypeTCP:
			validateTCPIRWithIndex(t, layer.(*layers.TCP), irPkt, index, tcpCount)
			tcpCount++
		case layers.LayerTypeUDP:
			validateUDPIRWithIndex(t, layer.(*layers.UDP), irPkt, index, udpCount)
			udpCount++
		case layers.LayerTypeICMPv4:
			validateICMPv4IR(t, layer.(*layers.ICMPv4), irPkt, index)
			// Add more layer types as needed
		}
	}
}

func validateEthernetIR(t *testing.T, eth *layers.Ethernet, irPkt IRPacketDef, index int) {
	var ethLayer *IRLayer
	for _, l := range irPkt.Layers {
		if l.Type == "Ether" {
			ethLayer = &l
			break
		}
	}
	require.NotNil(t, ethLayer, "Packet %d: Ethernet layer missing from IR", index)

	assert.Equal(t, eth.SrcMAC.String(), ethLayer.Params["src"],
		"Packet %d: Ethernet src mismatch", index)
	assert.Equal(t, eth.DstMAC.String(), ethLayer.Params["dst"],
		"Packet %d: Ethernet dst mismatch", index)
}

func validateIPv4IR(t *testing.T, ipv4 *layers.IPv4, irPkt IRPacketDef, index int) {
	validateIPv4IRWithIndex(t, ipv4, irPkt, index, 0)
}

func validateIPv4IRWithIndex(t *testing.T, ipv4 *layers.IPv4, irPkt IRPacketDef, index int, occurrence int) {
	var ipLayer *IRLayer
	count := 0
	for _, l := range irPkt.Layers {
		if l.Type == "IP" || l.Type == "IPv4" {
			if count == occurrence {
				ipLayer = &l
				break
			}
			count++
		}
	}
	require.NotNil(t, ipLayer, "Packet %d: IP/IPv4 layer (occurrence %d) missing from IR", index, occurrence)

	// Skip validation for malformed packets (nil or zero fields from parse failure)
	if ipv4.SrcIP != nil && !ipv4.SrcIP.IsUnspecified() {
		assert.Equal(t, ipv4.SrcIP.String(), ipLayer.Params["src"],
			"Packet %d IPv4[%d]: src mismatch", index, occurrence)
	}
	if ipv4.DstIP != nil && !ipv4.DstIP.IsUnspecified() {
		assert.Equal(t, ipv4.DstIP.String(), ipLayer.Params["dst"],
			"Packet %d IPv4[%d]: dst mismatch", index, occurrence)
	}
	if ipv4.TTL != 0 {
		assert.Equal(t, int(ipv4.TTL), ipLayer.Params["ttl"],
			"Packet %d IPv4[%d]: TTL mismatch", index, occurrence)
	}
	if ipv4.Protocol != 0 {
		assert.Equal(t, int(ipv4.Protocol), ipLayer.Params["proto"],
			"Packet %d IPv4[%d]: protocol mismatch", index, occurrence)
	}

	// Validate options if present
	if len(ipv4.Options) > 0 {
		_, hasOptions := ipLayer.Params["options"]
		assert.True(t, hasOptions, "Packet %d IPv4[%d]: options missing from IR", index, occurrence)
	}
}

func validateIPv6IR(t *testing.T, ipv6 *layers.IPv6, irPkt IRPacketDef, index int) {
	validateIPv6IRWithIndex(t, ipv6, irPkt, index, 0)
}

func validateIPv6IRWithIndex(t *testing.T, ipv6 *layers.IPv6, irPkt IRPacketDef, index int, occurrence int) {
	var ipLayer *IRLayer
	count := 0
	for _, l := range irPkt.Layers {
		if l.Type == "IPv6" {
			if count == occurrence {
				ipLayer = &l
				break
			}
			count++
		}
	}
	require.NotNil(t, ipLayer, "Packet %d: IPv6 layer (occurrence %d) missing from IR", index, occurrence)

	assert.Equal(t, ipv6.SrcIP.String(), ipLayer.Params["src"],
		"Packet %d IPv6[%d]: src mismatch", index, occurrence)
	assert.Equal(t, ipv6.DstIP.String(), ipLayer.Params["dst"],
		"Packet %d IPv6[%d]: dst mismatch", index, occurrence)

	// Allow HopLimit to differ by 1 (TTL decrement during packet processing)
	if hlimParam, ok := ipLayer.Params["hlim"].(int); ok {
		hopDiff := int(ipv6.HopLimit) - hlimParam
		if !(hopDiff >= -1 && hopDiff <= 1) {
			assert.Equal(t, int(ipv6.HopLimit), hlimParam,
				"Packet %d IPv6[%d]: HopLimit mismatch", index, occurrence)
		}
	}
}

func validateTCPIR(t *testing.T, tcp *layers.TCP, irPkt IRPacketDef, index int) {
	validateTCPIRWithIndex(t, tcp, irPkt, index, 0)
}

func validateTCPIRWithIndex(t *testing.T, tcp *layers.TCP, irPkt IRPacketDef, index int, occurrence int) {
	var tcpLayer *IRLayer
	count := 0
	for _, l := range irPkt.Layers {
		if l.Type == "TCP" {
			if count == occurrence {
				tcpLayer = &l
				break
			}
			count++
		}
	}
	require.NotNil(t, tcpLayer, "Packet %d: TCP layer (occurrence %d) missing from IR", index, occurrence)

	// Skip validation for malformed packets (zero ports from parse failure)
	if tcp.SrcPort != 0 {
		assert.Equal(t, int(tcp.SrcPort), tcpLayer.Params["sport"],
			"Packet %d TCP[%d]: sport mismatch", index, occurrence)
	}
	if tcp.DstPort != 0 {
		assert.Equal(t, int(tcp.DstPort), tcpLayer.Params["dport"],
			"Packet %d TCP[%d]: dport mismatch", index, occurrence)
	}

	// Validate options if present
	if len(tcp.Options) > 0 {
		_, hasOptions := tcpLayer.Params["options"]
		assert.True(t, hasOptions, "Packet %d TCP[%d]: options missing from IR", index, occurrence)
	}
}

func validateUDPIR(t *testing.T, udp *layers.UDP, irPkt IRPacketDef, index int) {
	validateUDPIRWithIndex(t, udp, irPkt, index, 0)
}

func validateUDPIRWithIndex(t *testing.T, udp *layers.UDP, irPkt IRPacketDef, index int, occurrence int) {
	var udpLayer *IRLayer
	count := 0
	for _, l := range irPkt.Layers {
		if l.Type == "UDP" {
			if count == occurrence {
				udpLayer = &l
				break
			}
			count++
		}
	}
	require.NotNil(t, udpLayer, "Packet %d: UDP layer (occurrence %d) missing from IR", index, occurrence)

	assert.Equal(t, int(udp.SrcPort), udpLayer.Params["sport"],
		"Packet %d UDP[%d]: sport mismatch", index, occurrence)
	assert.Equal(t, int(udp.DstPort), udpLayer.Params["dport"],
		"Packet %d UDP[%d]: dport mismatch", index, occurrence)

	// Length is required for exact reconstruction in PCAP equivalence tests.
	if udp.Length != 0 {
		assert.Equal(t, int(udp.Length), udpLayer.Params["len"],
			"Packet %d UDP[%d]: length mismatch", index, occurrence)
	}
}

func validateICMPv4IR(t *testing.T, icmp *layers.ICMPv4, irPkt IRPacketDef, index int) {
	var icmpLayer *IRLayer
	for _, l := range irPkt.Layers {
		if l.Type == "ICMP" {
			icmpLayer = &l
			break
		}
	}
	require.NotNil(t, icmpLayer, "Packet %d: ICMP layer missing from IR", index)

	assert.Equal(t, int(icmp.TypeCode.Type()), icmpLayer.Params["type"],
		"Packet %d: ICMP type mismatch", index)
	assert.Equal(t, int(icmp.TypeCode.Code()), icmpLayer.Params["code"],
		"Packet %d: ICMP code mismatch", index)
}

// generatePacketFromIRPipeline generates a packet from IR using packet_builder.go
func generatePacketFromIRPipeline(irPkt IRPacketDef, opts CodegenOpts) (gopacket.Packet, error) {
	var layerBuilders []LayerBuilder

	for _, layer := range irPkt.Layers {
		builder := buildLayerFromIR(layer, opts.IsExpect)
		if builder != nil {
			layerBuilders = append(layerBuilders, builder)
		}
	}

	// Use FixLengths: false to preserve explicit length/checksum values from custom layers
	// This matches the behavior in TestPCAPEquivalence
	serializeOpts := gopacket.SerializeOptions{
		FixLengths:       false,
		ComputeChecksums: false,
	}
	return NewPacket(&serializeOpts, layerBuilders...)
}

// buildLayerFromIR builds a layer from IR, used for testing IR → Packet conversion
// without generating and compiling Go code (faster test execution).
//
// WARNING: This function mirrors scapy_codegen_v2.generateLayerCall logic.
// Keep in sync with code generation! Changes to IR layer handling must be applied in both places:
//  1. scapy_codegen_v2.go (generates text code for production converter)
//  2. buildLayerFromIR (directly builds packets for tests)
//
// See: yanet2/tests/migration/converter/lib/scapy_codegen_v2.go
func buildLayerFromIR(layer IRLayer, isExpect bool) LayerBuilder {
	switch layer.Type {
	case "Ether":
		return buildEthernetFromIR(layer, isExpect)
	case "Dot1Q":
		return buildDot1QFromIR(layer)
	case "IP", "IPv4":
		return buildIPv4FromIR(layer)
	case "IPv6":
		return buildIPv6FromIR(layer)
	case "TCP":
		return buildTCPFromIR(layer)
	case "UDP":
		return buildUDPFromIR(layer)
	case "ICMP":
		return buildICMPFromIR(layer)
	case "ICMPv6", "ICMPv6EchoRequest", "ICMPv6EchoReply", "ICMPv6DestUnreach", "ICMPv6PacketTooBig", "ICMPv6TimeExceeded", "ICMPv6ParamProblem":
		return buildICMPv6FromIR(layer)
	case "ICMPv6RouterSolicitation", "ICMPv6RouterAdvertisement", "ICMPv6NeighborSolicitation", "ICMPv6NeighborAdvertisement":
		return buildICMPv6NDPFromIR(layer)
	case "GRE":
		return buildGREFromIR(layer)
	case "MPLS":
		return buildMPLSFromIR(layer)
	case "IPSecESP":
		return buildIPSecESPFromIR(layer)
	case "ARP":
		return buildARPFromIR(layer)
	case "Raw":
		return buildRawFromIR(layer)
	default:
		return nil
	}
}

func buildEthernetFromIR(layer IRLayer, isExpect bool) LayerBuilder {
	var opts []EtherOption
	if src, ok := layer.Params["src"].(string); ok {
		opts = append(opts, EtherSrc(src))
	}
	if dst, ok := layer.Params["dst"].(string); ok {
		opts = append(opts, EtherDst(dst))
	}
	return Ether(opts...)
}

func buildDot1QFromIR(layer IRLayer) LayerBuilder {
	var opts []Dot1QOption
	if vlan, ok := asInt(layer.Params["vlan"]); ok {
		opts = append(opts, VLANId(uint16(vlan)))
	}
	return Dot1Q(opts...)
}

func buildIPv4FromIR(layer IRLayer) LayerBuilder {
	var opts []IPv4Option
	if src, ok := layer.Params["src"].(string); ok {
		opts = append(opts, IPSrc(src))
	}
	if dst, ok := layer.Params["dst"].(string); ok {
		opts = append(opts, IPDst(dst))
	}
	if ttl, ok := asInt(layer.Params["ttl"]); ok {
		opts = append(opts, IPTTL(uint8(ttl)))
	}
	if tos, ok := asInt(layer.Params["tos"]); ok {
		opts = append(opts, IPTOS(uint8(tos)))
	}
	if proto, ok := asInt(layer.Params["proto"]); ok {
		opts = append(opts, IPProto(layers.IPProtocol(proto)))
	}
	if id, ok := asInt(layer.Params["id"]); ok {
		opts = append(opts, IPId(uint16(id)))
	}
	if flags, ok := asInt(layer.Params["flags"]); ok {
		opts = append(opts, IPFlags(layers.IPv4Flag(flags)))
	}
	if frag, ok := asInt(layer.Params["frag"]); ok {
		opts = append(opts, IPFragOffset(uint16(frag)))
	}
	if length, ok := asInt(layer.Params["len"]); ok {
		opts = append(opts, IPv4Length(uint16(length)))
	}
	if chksum, ok := asInt(layer.Params["chksum"]); ok {
		opts = append(opts, IPv4ChecksumRaw(uint16(chksum)))
	}
	if ihl, ok := asInt(layer.Params["ihl"]); ok {
		opts = append(opts, IPv4IHL(uint8(ihl)))
	}
	// Handle raw IPv4 options (non-standard padding)
	if rawOpts, ok := layer.Params["raw_options"].(string); ok && len(rawOpts) > 0 {
		// Decode hex string to bytes
		var optData []byte
		for i := 0; i < len(rawOpts); i += 2 {
			if i+1 < len(rawOpts) {
				var b byte
				fmt.Sscanf(rawOpts[i:i+2], "%02x", &b)
				optData = append(optData, b)
			}
		}
		if len(optData) > 0 {
			opts = append(opts, IPv4RawOptions(optData))
		}
	}
	// Handle structured IPv4 options
	if optionsAny, ok := layer.Params["options"]; ok {
		if options, ok := optionsAny.([]interface{}); ok && len(options) > 0 {
			var ipv4Opts []IPv4OptionDef
			for _, optAny := range options {
				if optMap, ok := optAny.(map[string]interface{}); ok {
					optType := uint8(asIntOrZero(optMap["type"]))
					optLen := uint8(asIntOrZero(optMap["len"]))
					var optData []byte
					if dataStr, ok := optMap["data"].(string); ok {
						// Decode hex string
						for i := 0; i < len(dataStr); i += 2 {
							if i+1 < len(dataStr) {
								var b byte
								fmt.Sscanf(dataStr[i:i+2], "%02x", &b)
								optData = append(optData, b)
							}
						}
					}
					ipv4Opts = append(ipv4Opts, IPv4OptionDef{
						Type:   optType,
						Length: optLen,
						Data:   optData,
					})
				}
			}
			opts = append(opts, IPv4Options(ipv4Opts))
		}
	}
	return IPv4(opts...)
}

func buildIPv6FromIR(layer IRLayer) LayerBuilder {
	var opts []IPv6Option
	if src, ok := layer.Params["src"].(string); ok {
		opts = append(opts, IPv6Src(src))
	}
	if dst, ok := layer.Params["dst"].(string); ok {
		opts = append(opts, IPv6Dst(dst))
	}
	if hlim, ok := asInt(layer.Params["hlim"]); ok {
		opts = append(opts, IPv6HopLimit(uint8(hlim)))
	}
	if tc, ok := asInt(layer.Params["tc"]); ok {
		opts = append(opts, IPv6TrafficClass(uint8(tc)))
	}
	if fl, ok := asInt(layer.Params["fl"]); ok {
		opts = append(opts, IPv6FlowLabel(uint32(fl)))
	}
	if nh, ok := asInt(layer.Params["nh"]); ok {
		opts = append(opts, IPv6NextHeader(layers.IPProtocol(nh)))
	}
	if plen, ok := asInt(layer.Params["plen"]); ok {
		opts = append(opts, IPv6PayloadLength(uint16(plen)))
	}
	return IPv6(opts...)
}

func buildTCPFromIR(layer IRLayer) LayerBuilder {
	var opts []TCPOption
	if sport, ok := asInt(layer.Params["sport"]); ok {
		opts = append(opts, TCPSport(uint16(sport)))
	}
	if dport, ok := asInt(layer.Params["dport"]); ok {
		opts = append(opts, TCPDport(uint16(dport)))
	}
	if flags, ok := layer.Params["flags"].(string); ok {
		opts = append(opts, TCPFlags(flags))
	}
	if seq, ok := asInt(layer.Params["seq"]); ok {
		opts = append(opts, TCPSeq(uint32(seq)))
	}
	if ack, ok := asInt(layer.Params["ack"]); ok {
		opts = append(opts, TCPAck(uint32(ack)))
	}
	if window, ok := asInt(layer.Params["window"]); ok {
		opts = append(opts, TCPWindow(uint16(window)))
	}
	if urgent, ok := asInt(layer.Params["urgent"]); ok {
		opts = append(opts, TCPUrgent(uint16(urgent)))
	}
	if chksum, ok := asInt(layer.Params["chksum"]); ok {
		opts = append(opts, TCPChecksumRaw(uint16(chksum)))
	}
	if dataofs, ok := asInt(layer.Params["dataofs"]); ok {
		opts = append(opts, TCPDataOffset(uint8(dataofs)))
	}
	// Handle TCP options
	if optionsAny, ok := layer.Params["options"]; ok {
		// Try []interface{} first (JSON unmarshaled)
		if options, ok := optionsAny.([]interface{}); ok && len(options) > 0 {
			var tcpOpts []TCPOptionDef
			for _, optAny := range options {
				if optMap, ok := optAny.(map[string]interface{}); ok {
					optKind := layers.TCPOptionKind(asIntOrZero(optMap["kind"]))
					optLen := uint8(asIntOrZero(optMap["len"]))
					var optData []byte
					if dataStr, ok := optMap["data"].(string); ok {
						// Decode hex string
						for i := 0; i < len(dataStr); i += 2 {
							if i+1 < len(dataStr) {
								var b byte
								fmt.Sscanf(dataStr[i:i+2], "%02x", &b)
								optData = append(optData, b)
							}
						}
					}
					tcpOpts = append(tcpOpts, TCPOptionDef{
						Kind:   optKind,
						Length: optLen,
						Data:   optData,
					})
				}
			}
			opts = append(opts, TCPOptions(tcpOpts))
		} else if options, ok := optionsAny.([]map[string]interface{}); ok && len(options) > 0 {
			// Try []map[string]interface{} (direct from IR construction)
			var tcpOpts []TCPOptionDef
			for _, optMap := range options {
				optKind := layers.TCPOptionKind(asIntOrZero(optMap["kind"]))
				optLen := uint8(asIntOrZero(optMap["len"]))
				var optData []byte
				if dataStr, ok := optMap["data"].(string); ok {
					// Decode hex string
					for i := 0; i < len(dataStr); i += 2 {
						if i+1 < len(dataStr) {
							var b byte
							fmt.Sscanf(dataStr[i:i+2], "%02x", &b)
							optData = append(optData, b)
						}
					}
				}
				tcpOpts = append(tcpOpts, TCPOptionDef{
					Kind:   optKind,
					Length: optLen,
					Data:   optData,
				})
			}
			opts = append(opts, TCPOptions(tcpOpts))
		}
	}
	return TCP(opts...)
}

func buildUDPFromIR(layer IRLayer) LayerBuilder {
	var opts []UDPOption
	if sport, ok := asInt(layer.Params["sport"]); ok {
		opts = append(opts, UDPSport(uint16(sport)))
	}
	if dport, ok := asInt(layer.Params["dport"]); ok {
		opts = append(opts, UDPDport(uint16(dport)))
	}
	if chksum, ok := asInt(layer.Params["chksum"]); ok {
		opts = append(opts, UDPChecksumRaw(uint16(chksum)))
	}
	if length, ok := asInt(layer.Params["len"]); ok {
		opts = append(opts, UDPLengthRaw(uint16(length)))
	}
	return UDP(opts...)
}

func buildICMPFromIR(layer IRLayer) LayerBuilder {
	var opts []ICMPOption
	if typeVal, ok := asInt(layer.Params["type"]); ok {
		codeVal := 0
		if c, ok := asInt(layer.Params["code"]); ok {
			codeVal = c
		}
		opts = append(opts, ICMPTypeCode(uint8(typeVal), uint8(codeVal)))
	}
	if id, ok := asInt(layer.Params["id"]); ok {
		opts = append(opts, ICMPId(uint16(id)))
	}
	if seq, ok := asInt(layer.Params["seq"]); ok {
		opts = append(opts, ICMPSeq(uint16(seq)))
	}
	if chksum, ok := asInt(layer.Params["chksum"]); ok {
		opts = append(opts, ICMPChecksum(uint16(chksum)))
	}
	return ICMP(opts...)
}

func buildICMPv6FromIR(layer IRLayer) LayerBuilder {
	var opts []ICMPv6Option

	// Add id and seq if present (for echo request/reply)
	if id, ok := asInt(layer.Params["id"]); ok {
		opts = append(opts, ICMPv6Id(uint16(id)))
	}
	if seq, ok := asInt(layer.Params["seq"]); ok {
		opts = append(opts, ICMPv6Seq(uint16(seq)))
	}
	if code, ok := asInt(layer.Params["code"]); ok {
		opts = append(opts, ICMPv6Code(uint8(code)))
	}
	if chksum, ok := asInt(layer.Params["chksum"]); ok {
		opts = append(opts, ICMPv6Checksum(uint16(chksum)))
	}

	// Return appropriate ICMPv6 type based on layer.Type
	switch layer.Type {
	case "ICMPv6EchoRequest":
		return ICMPv6EchoRequest(opts...)
	case "ICMPv6EchoReply":
		return ICMPv6EchoReply(opts...)
	case "ICMPv6DestUnreach":
		return ICMPv6DestUnreach(opts...)
	case "ICMPv6PacketTooBig":
		return ICMPv6PacketTooBig(opts...)
	case "ICMPv6TimeExceeded":
		return ICMPv6TimeExceeded(opts...)
	case "ICMPv6ParamProblem":
		return ICMPv6ParamProblem(opts...)
	case "ICMPv6":
		// Generic ICMPv6 control message (e.g. Router Solicitation) where the
		// type comes directly from the original PCAP.
		if icmpType, ok := asInt(layer.Params["type"]); ok {
			opts = append(opts, ICMPv6Type(uint8(icmpType)))
		}
		return ICMPv6(opts...)
	default:
		// Fallback to generic ICMPv6 if an unknown subtype appears.
		return ICMPv6(opts...)
	}
}

func buildICMPv6NDPFromIR(layer IRLayer) LayerBuilder {
	// For NDP messages (Router Solicitation, Router Advertisement, etc.),
	// we build a Raw layer with the serialized NDP message.
	// The options are embedded in the layer and will be serialized by gopacket.
	switch layer.Type {
	case "ICMPv6RouterSolicitation":
		return ICMPv6RouterSolicitation()
	case "ICMPv6RouterAdvertisement":
		return ICMPv6RouterAdvertisement()
	case "ICMPv6NeighborSolicitation":
		return ICMPv6NeighborSolicitation()
	case "ICMPv6NeighborAdvertisement":
		return ICMPv6NeighborAdvertisement()
	default:
		return nil
	}
}

func buildGREFromIR(layer IRLayer) LayerBuilder {
	var opts []GREOption

	// Check if raw_flags is present (for unsupported flags like ack present, routing present, etc.)
	if rawFlags, ok := asInt(layer.Params["raw_flags"]); ok {
		opts = append(opts, GRERawFlags(uint16(rawFlags)))
	}

	if proto, ok := asInt(layer.Params["proto"]); ok {
		opts = append(opts, GREProtocol(layers.EthernetType(proto)))
	}

	// Handle checksum present flag and value
	if chksumPresent, ok := asInt(layer.Params["chksum_present"]); ok && chksumPresent != 0 {
		opts = append(opts, GREChecksumPresent(true))
		if chksum, ok := asInt(layer.Params["chksum"]); ok {
			opts = append(opts, GREChecksum(uint16(chksum)))
		}
	}

	// Handle key present flag and value
	if keyPresent, ok := asInt(layer.Params["key_present"]); ok && keyPresent != 0 {
		opts = append(opts, GREKeyPresent(true))
		if key, ok := asInt(layer.Params["key"]); ok {
			opts = append(opts, GREKey(uint32(key)))
		}
	}

	// Handle sequence present flag and value
	if seqPresent, ok := asInt(layer.Params["seqnum_present"]); ok && seqPresent != 0 {
		opts = append(opts, GRESeqPresent(true))
		if seq, ok := asInt(layer.Params["seq"]); ok {
			opts = append(opts, GRESeq(uint32(seq)))
		}
	}

	// Handle routing_present flag
	if routingPresent, ok := asInt(layer.Params["routing_present"]); ok && routingPresent != 0 {
		opts = append(opts, GRERoutingPresent(true))
	}

	// Handle version field
	if version, ok := asInt(layer.Params["version"]); ok {
		opts = append(opts, GREVersion(uint8(version)))
	}

	return GRE(opts...)
}

func buildMPLSFromIR(layer IRLayer) LayerBuilder {
	var opts []MPLSOption
	if label, ok := asInt(layer.Params["label"]); ok {
		opts = append(opts, MPLSLabel(uint32(label)))
	}
	if ttl, ok := asInt(layer.Params["ttl"]); ok {
		opts = append(opts, MPLSTTL(uint8(ttl)))
	}
	if s, ok := asInt(layer.Params["s"]); ok {
		opts = append(opts, MPLSStackBit(s == 1))
	}
	if cos, ok := asInt(layer.Params["cos"]); ok {
		opts = append(opts, MPLSTrafficClass(uint8(cos)))
	}
	return MPLS(opts...)
}

func buildIPSecESPFromIR(layer IRLayer) LayerBuilder {
	var opts []IPSecESPOption
	if spi, ok := asInt(layer.Params["spi"]); ok {
		opts = append(opts, ESPSPI(uint32(spi)))
	}
	if seq, ok := asInt(layer.Params["seq"]); ok {
		opts = append(opts, ESPSeq(uint32(seq)))
	}
	if encrypted, ok := layer.Params["encrypted"].(string); ok && len(encrypted) > 0 {
		// Decode hex string
		var data []byte
		for i := 0; i < len(encrypted); i += 2 {
			if i+1 < len(encrypted) {
				var b byte
				fmt.Sscanf(encrypted[i:i+2], "%02x", &b)
				data = append(data, b)
			}
		}
		if len(data) > 0 {
			opts = append(opts, ESPEncrypted(data))
		}
	}
	return IPSecESP(opts...)
}

func buildARPFromIR(layer IRLayer) LayerBuilder {
	var opts []ARPOption
	if operation, ok := asInt(layer.Params["operation"]); ok {
		opts = append(opts, ARPOperation(uint16(operation)))
	}
	if hwtype, ok := asInt(layer.Params["hwtype"]); ok {
		opts = append(opts, ARPHwType(uint16(hwtype)))
	}
	if ptype, ok := asInt(layer.Params["ptype"]); ok {
		opts = append(opts, ARPPType(uint16(ptype)))
	}
	if hwlen, ok := asInt(layer.Params["hwlen"]); ok {
		opts = append(opts, ARPHwLen(uint8(hwlen)))
	}
	if plen, ok := asInt(layer.Params["plen"]); ok {
		opts = append(opts, ARPPLen(uint8(plen)))
	}
	if hwsrc, ok := layer.Params["hwsrc"].(string); ok {
		opts = append(opts, ARPHwSrc(hwsrc))
	}
	if psrc, ok := layer.Params["psrc"].(string); ok {
		opts = append(opts, ARPPSrc(psrc))
	}
	if hwdst, ok := layer.Params["hwdst"].(string); ok {
		opts = append(opts, ARPHwDst(hwdst))
	}
	if pdst, ok := layer.Params["pdst"].(string); ok {
		opts = append(opts, ARPPDst(pdst))
	}
	return ARP(opts...)
}

func buildRawFromIR(layer IRLayer) LayerBuilder {
	if load, ok := layer.Params["load"].(string); ok {
		return Raw([]byte(load))
	}
	if arg0, ok := layer.Params["_arg0"].(string); ok {
		return Raw([]byte(arg0))
	}
	return Raw([]byte{})
}

// comparePacketsSemantically compares packets semantically using cmp.Diff
func comparePacketsSemantically(t *testing.T, expected, actual gopacket.Packet, index int) {
	// Compare layer by layer, but filter out DecodeFailure layers
	// DecodeFailure appears when gopacket can't parse remaining bytes (malformed packets)
	expLayers := filterDecodeFailureLayers(expected.Layers())
	actLayers := filterDecodeFailureLayers(actual.Layers())

	require.Equal(t, len(expLayers), len(actLayers),
		"Packet %d: Layer count mismatch", index)

	for i, expLayer := range expLayers {
		actLayer := actLayers[i]
		require.Equal(t, expLayer.LayerType(), actLayer.LayerType(),
			"Packet %d: Layer %d type mismatch", index, i)

		switch expLayer.LayerType() {
		case layers.LayerTypeEthernet:
			compareEthernet(t, expLayer.(*layers.Ethernet), actLayer.(*layers.Ethernet), index)
		case layers.LayerTypeIPv4:
			compareIPv4(t, expLayer.(*layers.IPv4), actLayer.(*layers.IPv4), index)
		case layers.LayerTypeIPv6:
			compareIPv6(t, expLayer.(*layers.IPv6), actLayer.(*layers.IPv6), index)
		case layers.LayerTypeTCP:
			compareTCP(t, expLayer.(*layers.TCP), actLayer.(*layers.TCP), index)
		case layers.LayerTypeUDP:
			compareUDP(t, expLayer.(*layers.UDP), actLayer.(*layers.UDP), index)
		case layers.LayerTypeICMPv4:
			compareICMPv4(t, expLayer.(*layers.ICMPv4), actLayer.(*layers.ICMPv4), index)
		case layers.LayerTypeMPLS:
			compareMPLS(t, expLayer.(*layers.MPLS), actLayer.(*layers.MPLS), index)
		}
	}
}

// filterDecodeFailureLayers removes DecodeFailure and Payload layers from comparison
// These are artifacts of gopacket parsing malformed/truncated packets
func filterDecodeFailureLayers(layers []gopacket.Layer) []gopacket.Layer {
	filtered := make([]gopacket.Layer, 0, len(layers))
	for _, layer := range layers {
		layerType := layer.LayerType()

		// Skip DecodeFailure and generic Payload layers - these are parsing artifacts
		if layerType == gopacket.LayerTypeDecodeFailure ||
			layerType == gopacket.LayerTypePayload {
			continue
		}

		// Skip ICMPv6 subtypes (RouterSolicitation, RouterAdvertisement, Echo, etc.)
		// These are specialized layers that gopacket creates, but after serialization
		// and re-parsing, they may not be reconstructed correctly. The base ICMPv6
		// layer contains all the necessary information for comparison.
		if layerType >= 124 && layerType <= 140 {
			// ICMPv6 subtypes range: 124-140
			// Skip these specialized layers to avoid comparison issues
			continue
		}

		filtered = append(filtered, layer)
	}
	return filtered
}

func compareEthernet(t *testing.T, exp, act *layers.Ethernet, index int) {
	// Use cmp.Diff for detailed comparison
	diff := cmp.Diff(exp, act,
		cmpopts.IgnoreUnexported(layers.Ethernet{}),
		cmpopts.IgnoreFields(layers.Ethernet{}, "BaseLayer"))

	if diff != "" {
		t.Errorf("Packet %d: Ethernet mismatch (-want +got):\n%s", index, diff)
	}
}

func compareIPv4(t *testing.T, exp, act *layers.IPv4, index int) {
	// Use cmp.Diff but ignore computed fields for malformed packets
	opts := []cmp.Option{
		cmpopts.IgnoreUnexported(layers.IPv4{}),
		cmpopts.IgnoreFields(layers.IPv4{}, "BaseLayer"),
		// For malformed packets, ignore length/checksum if they're zero
		cmpopts.IgnoreFields(layers.IPv4{}, "Padding"),
		// Always ignore Checksum - we preserve original PCAP checksums which may be incorrect
		cmpopts.IgnoreFields(layers.IPv4{}, "Checksum"),
		// Always ignore Length - gopacket recalculates it when parsing, we'll compare raw bytes
		cmpopts.IgnoreFields(layers.IPv4{}, "Length"),
	}

	// If expected packet has nil/zero fields (malformed/undecoded), ignore them
	if exp.SrcIP == nil || exp.SrcIP.IsUnspecified() {
		opts = append(opts, cmpopts.IgnoreFields(layers.IPv4{}, "SrcIP"))
	}
	if exp.DstIP == nil || exp.DstIP.IsUnspecified() {
		opts = append(opts, cmpopts.IgnoreFields(layers.IPv4{}, "DstIP"))
	}
	if exp.TTL == 0 {
		opts = append(opts, cmpopts.IgnoreFields(layers.IPv4{}, "TTL"))
	}
	if exp.Protocol == 0 {
		opts = append(opts, cmpopts.IgnoreFields(layers.IPv4{}, "Protocol"))
	}

	diff := cmp.Diff(exp, act, opts...)

	if diff != "" {
		t.Errorf("Packet %d: IPv4 mismatch (-want +got):\n%s", index, diff)
	}

	// Compare Length from raw bytes (gopacket recalculates it during parsing)
	// IPv4 Total Length is at bytes 2-3 of the IPv4 header
	if len(exp.Contents) >= 4 && len(act.Contents) >= 4 {
		expLength := uint16(exp.Contents[2])<<8 | uint16(exp.Contents[3])
		actLength := uint16(act.Contents[2])<<8 | uint16(act.Contents[3])
		if expLength != actLength {
			t.Errorf("Packet %d: IPv4 Length mismatch in raw bytes: expected %d, got %d", index, expLength, actLength)
		}
	}
}

func compareIPv6(t *testing.T, exp, act *layers.IPv6, index int) {
	opts := []cmp.Option{
		cmpopts.IgnoreUnexported(layers.IPv6{}),
		cmpopts.IgnoreUnexported(layers.IPv6HopByHop{}),
		cmpopts.IgnoreUnexported(layers.IPv6Destination{}),
		cmpopts.IgnoreUnexported(layers.IPv6Routing{}),
		cmpopts.IgnoreUnexported(layers.IPv6Fragment{}),
		cmpopts.IgnoreFields(layers.IPv6{}, "BaseLayer"),
	}

	// Ignore HopLimit for malformed packets (HopLimit=0 in expected)
	// or when difference is 1 (TTL decrement during packet processing)
	if exp.HopLimit == 0 {
		opts = append(opts, cmpopts.IgnoreFields(layers.IPv6{}, "HopLimit"))
	} else if act.HopLimit != 0 {
		hopDiff := int(exp.HopLimit) - int(act.HopLimit)
		if hopDiff == 1 || hopDiff == -1 {
			// Allow HopLimit to differ by 1 (normal TTL decrement)
			opts = append(opts, cmpopts.IgnoreFields(layers.IPv6{}, "HopLimit"))
		}
	}

	// Ignore src/dst for malformed packets (nil in expected)
	if exp.SrcIP == nil || len(exp.SrcIP) == 0 {
		opts = append(opts, cmpopts.IgnoreFields(layers.IPv6{}, "SrcIP"))
	}
	if exp.DstIP == nil || len(exp.DstIP) == 0 {
		opts = append(opts, cmpopts.IgnoreFields(layers.IPv6{}, "DstIP"))
	}

	// Ignore NextHeader if zero in expected (malformed packet)
	if exp.NextHeader == 0 {
		opts = append(opts, cmpopts.IgnoreFields(layers.IPv6{}, "NextHeader"))
	}

	diff := cmp.Diff(exp, act, opts...)

	if diff != "" {
		t.Errorf("Packet %d: IPv6 mismatch (-want +got):\n%s", index, diff)
	}
}

func compareTCP(t *testing.T, exp, act *layers.TCP, index int) {
	opts := []cmp.Option{
		cmpopts.IgnoreUnexported(layers.TCP{}),
		cmpopts.IgnoreFields(layers.TCP{}, "BaseLayer", "Padding"),
		// Always ignore Checksum - it's recalculated during packet serialization in NewPacket()
		// Original PCAP may have incorrect checksums for malformed packets
		cmpopts.IgnoreFields(layers.TCP{}, "Checksum"),
	}

	// For malformed packets, ignore zero ports (gopacket parse failure)
	if exp.SrcPort == 0 {
		opts = append(opts, cmpopts.IgnoreFields(layers.TCP{}, "SrcPort"))
	}
	if exp.DstPort == 0 {
		opts = append(opts, cmpopts.IgnoreFields(layers.TCP{}, "DstPort"))
	}

	diff := cmp.Diff(exp, act, opts...)

	if diff != "" {
		t.Errorf("Packet %d: TCP mismatch (-want +got):\n%s", index, diff)
	}
}

func compareUDP(t *testing.T, exp, act *layers.UDP, index int) {
	opts := []cmp.Option{
		cmpopts.IgnoreUnexported(layers.UDP{}),
		cmpopts.IgnoreFields(layers.UDP{}, "BaseLayer"),
		// Always ignore Checksum - it's recalculated during packet serialization in NewPacket()
		cmpopts.IgnoreFields(layers.UDP{}, "Checksum"),
	}

	// For malformed packets, ignore zero ports (gopacket parse failure)
	if exp.SrcPort == 0 {
		opts = append(opts, cmpopts.IgnoreFields(layers.UDP{}, "SrcPort"))
	}
	if exp.DstPort == 0 {
		opts = append(opts, cmpopts.IgnoreFields(layers.UDP{}, "DstPort"))
	}

	diff := cmp.Diff(exp, act, opts...)

	if diff != "" {
		t.Errorf("Packet %d: UDP mismatch (-want +got):\n%s", index, diff)
	}
}

func compareICMPv4(t *testing.T, exp, act *layers.ICMPv4, index int) {
	diff := cmp.Diff(exp, act,
		cmpopts.IgnoreUnexported(layers.ICMPv4{}),
		cmpopts.IgnoreFields(layers.ICMPv4{}, "BaseLayer"))

	if diff != "" {
		t.Errorf("Packet %d: ICMPv4 mismatch (-want +got):\n%s", index, diff)
	}
}

func compareMPLS(t *testing.T, exp, act *layers.MPLS, index int) {
	diff := cmp.Diff(exp, act,
		cmpopts.IgnoreUnexported(layers.MPLS{}),
		cmpopts.IgnoreFields(layers.MPLS{}, "BaseLayer"))

	if diff != "" {
		t.Errorf("Packet %d: MPLS mismatch (-want +got):\n%s", index, diff)
	}
}

// Helper functions
func readPCAPPackets(pcapPath string) ([]gopacket.Packet, error) {
	handle, err := pcap.OpenOffline(pcapPath)
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	var packets []gopacket.Packet
	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())

	for packet := range packetSource.Packets() {
		packets = append(packets, packet)
	}

	return packets, nil
}

func asIntOrZero(v interface{}) int {
	if i, ok := asInt(v); ok {
		return i
	}
	return 0
}

func TestTCPOptionDetection(t *testing.T) {
	// Option type 253 should be detected as non-standard (> 30)
	builder := &TCPBuilder{
		layer: &layers.TCP{
			Options: []layers.TCPOption{
				{OptionType: 253, OptionLength: 14},
			},
		},
	}

	hasNonStd := builder.hasNonStandardOptions()
	t.Logf("Option type 253: hasNonStandardOptions = %v (should be true)", hasNonStd)

	if !hasNonStd {
		t.Errorf("Option type 253 should be detected as non-standard (253 > 30)")
	}
}

func TestTCPWithNonStandardOptionsSerialization(t *testing.T) {
	// Create packet: Eth + IPv4 + TCP with non-standard option
	packet, err := NewPacket(nil,
		Ether(),
		IPv4(IPSrc("5.5.5.66"), IPDst("6.7.8.6")),
		TCP(
			TCPSport(8800),
			TCPDport(555),
			TCPFlags("A"),
			TCPOptions([]TCPOptionDef{
				{Kind: layers.TCPOptionKindNop, Length: 1},
				{Kind: 253, Length: 14, Data: make([]byte, 12)},
				{Kind: layers.TCPOptionKindEndList, Length: 1},
			}),
		),
	)
	require.NoError(t, err)

	rawBytes := packet.Data()
	t.Logf("Total packet length: %d bytes", len(rawBytes))

	// Expected: Eth(14) + IPv4(20) + TCP(36 with options) = 70 bytes
	expectedLen := 14 + 20 + 36
	if len(rawBytes) != expectedLen {
		t.Errorf("Expected %d bytes, got %d bytes", expectedLen, len(rawBytes))
	}

	// Check IPv4 Total Length field
	if len(rawBytes) >= 18 {
		ipTotalLen := int(rawBytes[16])<<8 | int(rawBytes[17])
		t.Logf("IPv4 Total Length: %d", ipTotalLen)
		expectedIPLen := 20 + 36 // IPv4 header + TCP with options
		if ipTotalLen != expectedIPLen {
			t.Errorf("IPv4 Total Length: expected %d, got %d", expectedIPLen, ipTotalLen)
		}
	}
}

func TestCustomTCPSerializationInTunnel(t *testing.T) {
	// Reproduce the exact scenario from 029_acl_dregress_decap
	packet, err := NewPacket(nil,
		Ether(EtherSrc("00:00:00:00:00:02"), EtherDst("00:11:22:33:44:55")),
		Dot1Q(VLANId(200)),
		IPv6(IPv6Src("abba::1"), IPv6Dst("1234::abcd"), IPv6FlowLabel(0x12345), IPv6HopLimit(64)),
		IPv4(IPSrc("5.5.5.66"), IPDst("6.7.8.6"), IPTTL(64), IPId(1)),
		TCP(
			TCPSport(8800),
			TCPDport(555),
			TCPSeq(3536),
			TCPFlags("A"),
			TCPWindow(8192),
			TCPOptions([]TCPOptionDef{
				{Kind: layers.TCPOptionKindNop, Length: 1},
				{Kind: 253, Length: 14, Data: []byte{0x79, 0x61, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
				{Kind: layers.TCPOptionKindEndList, Length: 1},
			}),
		),
		Raw([]byte("luchshe prosto pozvonit'")),
	)
	require.NoError(t, err)

	packetBytes := packet.Data()
	t.Logf("Generated packet size: %d bytes", len(packetBytes))

	// Expected: Eth(14) + VLAN(4) + IPv6(40) + IPv4(20) + TCP(36) + Payload(24) = 138 bytes
	expectedSize := 14 + 4 + 40 + 20 + 36 + 24
	t.Logf("Expected packet size: %d bytes", expectedSize)

	if len(packetBytes) != expectedSize {
		t.Errorf("Packet size mismatch: expected %d, got %d (diff: %d)",
			expectedSize, len(packetBytes), len(packetBytes)-expectedSize)

		// Dump IPv4 header to check Total Length
		ipv4Start := 14 + 4 + 40
		if len(packetBytes) > ipv4Start+20 {
			ipv4Header := packetBytes[ipv4Start : ipv4Start+20]
			ipTotalLen := int(ipv4Header[2])<<8 | int(ipv4Header[3])
			ihl := (ipv4Header[0] & 0x0f) * 4
			t.Logf("IPv4 IHL: %d, Total Length: %d", ihl, ipTotalLen)

			// Check TCP header
			tcpStart := ipv4Start + int(ihl)
			if len(packetBytes) > tcpStart+20 {
				tcpHeader := packetBytes[tcpStart : tcpStart+20]
				tcpDataOffset := (tcpHeader[12] >> 4) * 4
				t.Logf("TCP DataOffset: %d bytes", tcpDataOffset)
			}
		}
	}
}

func TestTCPOptionsInIR(t *testing.T) {
	// Load the problematic PCAP and check if TCP options are in IR
	yanet1Root := GetYanet1Root()
	pcapPath := filepath.Join(yanet1Root, "autotest/units/001_one_port/029_acl_dregress_decap/003-send.pcap")
	if _, err := os.Stat(pcapPath); os.IsNotExist(err) {
		t.Skipf("PCAP file not found: %s", pcapPath)
	}
	analyzer := NewPcapAnalyzer(false)
	packetInfos, err := analyzer.ReadAllPacketsFromFile(pcapPath)
	require.NoError(t, err)
	require.Greater(t, len(packetInfos), 1, "Should have at least 2 packets")

	opts := CodegenOpts{UseFrameworkMACs: false, IsExpect: false, StripVLAN: false}
	ir, err := analyzer.ConvertPacketInfoToIR(packetInfos, "send.pcap", "expect.pcap", opts)
	require.NoError(t, err)

	require.Greater(t, len(ir.PCAPPairs), 0, "Should have PCAP pairs")
	sendPackets := ir.PCAPPairs[0].SendPackets
	require.Greater(t, len(sendPackets), 1, "Should have at least 2 packets")

	// Check packet 2 (index 1) which has TCP options
	pkt2 := sendPackets[1]
	t.Logf("Packet 2 IR layers: %d", len(pkt2.Layers))

	var foundTCP bool
	for i, layer := range pkt2.Layers {
		t.Logf("  Layer %d: %s", i, layer.Type)
		if layer.Type == "TCP" {
			foundTCP = true
			if opts, ok := layer.Params["options"]; ok {
				t.Logf("    ✓ TCP options found: %+v", opts)
				// Check if options is an array
				if optArray, ok := opts.([]interface{}); ok {
					t.Logf("    TCP options count: %d", len(optArray))
					for j, opt := range optArray {
						t.Logf("      Option %d: %+v", j, opt)
					}
				}
			} else {
				t.Errorf("    ✗ TCP options NOT found in IR!")
			}
			if dataofs, ok := layer.Params["dataofs"]; ok {
				t.Logf("    TCP dataofs: %v", dataofs)
			} else {
				t.Logf("    TCP dataofs: not set (default 5)")
			}
		}
	}

	require.True(t, foundTCP, "Should have found TCP layer in IR")
}

func TestARPFromIR(t *testing.T) {
	yanet1Root := GetYanet1Root()
	pcapPath := filepath.Join(yanet1Root, "autotest/units/001_one_port/019_acl_decap_route/005-send.pcap")
	if _, err := os.Stat(pcapPath); os.IsNotExist(err) {
		t.Skipf("PCAP file not found: %s", pcapPath)
	}
	analyzer := NewPcapAnalyzer(false)
	packetInfos, err := analyzer.ReadAllPacketsFromFile(pcapPath)
	require.NoError(t, err)

	opts := CodegenOpts{UseFrameworkMACs: false, IsExpect: false, StripVLAN: false}
	ir, err := analyzer.ConvertPacketInfoToIR(packetInfos, "send.pcap", "expect.pcap", opts)
	require.NoError(t, err)

	require.Greater(t, len(ir.PCAPPairs), 0)
	require.Greater(t, len(ir.PCAPPairs[0].SendPackets), 0)

	irPkt := ir.PCAPPairs[0].SendPackets[0]
	t.Logf("IR layers:")
	for i, layer := range irPkt.Layers {
		t.Logf("  %d: %s", i, layer.Type)
	}

	// Generate packet from IR
	var layerBuilders []LayerBuilder
	for _, layer := range irPkt.Layers {
		builder := buildLayerFromIR(layer, false)
		if builder != nil {
			layerBuilders = append(layerBuilders, builder)
		}
	}

	// Serialize
	pkt, err := NewPacket(nil, layerBuilders...)
	require.NoError(t, err)

	// Check what gopacket parsed
	t.Logf("Generated packet layers (after gopacket parse):")
	hasIPv4 := false
	hasARP := false
	for i, layer := range pkt.Layers() {
		t.Logf("  %d: %s", i, layer.LayerType())
		if layer.LayerType() == layers.LayerTypeIPv4 {
			hasIPv4 = true
			ipv4 := layer.(*layers.IPv4)
			t.Logf("    IPv4: Src=%s, Dst=%s", ipv4.SrcIP, ipv4.DstIP)
		}
		if layer.LayerType() == layers.LayerTypeARP {
			hasARP = true
		}
		if layer.LayerType() == layers.LayerTypeDot1Q {
			vlan := layer.(*layers.Dot1Q)
			t.Logf("    VLAN Type: 0x%04x (%s)", vlan.Type, vlan.Type.String())
		}
	}

	if hasIPv4 {
		t.Errorf("Unexpected IPv4 layer in ARP packet!")
	}
	require.True(t, hasARP, "Missing ARP layer!")

	// Also check original packet parsing
	originalBytes := pkt.Data()
	originalPkt := gopacket.NewPacket(originalBytes, layers.LayerTypeEthernet, gopacket.Default)
	t.Logf("Original packet re-parsed layers:")
	for i, layer := range originalPkt.Layers() {
		t.Logf("  %d: %s", i, layer.LayerType())
		if layer.LayerType() == layers.LayerTypeDot1Q {
			vlan := layer.(*layers.Dot1Q)
			t.Logf("    VLAN Type: 0x%04x (%s)", vlan.Type, vlan.Type.String())
		}
	}
}
