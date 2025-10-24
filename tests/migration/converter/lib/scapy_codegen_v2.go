package lib

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// IRLayer represents a layer in the IR JSON
type IRLayer struct {
	Type   string                 `json:"type"`
	Params map[string]interface{} `json:"params"`
}

// IRPacketDef represents a packet definition in IR
type IRPacketDef struct {
	Layers          []IRLayer              `json:"layers"`
	SpecialHandling map[string]interface{} `json:"special_handling"`
}

// IRPCAPPair represents a PCAP file pair in IR
type IRPCAPPair struct {
	SendFile      string        `json:"send_file"`
	ExpectFile    string        `json:"expect_file"`
	SendPackets   []IRPacketDef `json:"send_packets"`
	ExpectPackets []IRPacketDef `json:"expect_packets"`
}

// IRJSON represents the complete IR from Python parser
type IRJSON struct {
	PCAPPairs       []IRPCAPPair `json:"pcap_pairs"`
	HelperFunctions []string     `json:"helper_functions"`
}

// ToJSON converts IRJSON to JSON string
func (ir *IRJSON) ToJSON() (string, error) {
	jsonBytes, err := json.Marshal(ir)
	if err != nil {
		return "", err
	}
	return string(jsonBytes), nil
}

// IRPacketPattern represents a detected pattern in packet definitions
type IRPacketPattern struct {
	CommonLayers  []IRLayer        // Layers with constant params
	VaryingParams []IRVaryingParam // Parameters that change across packets
	StartIndex    int              // Index of first packet in the pattern group
	EndIndex      int              // Index of last packet in the pattern group (exclusive)
}

// IRVaryingParam represents a parameter that varies across packets
type IRVaryingParam struct {
	LayerIndex int           // Which layer (0-based)
	LayerType  string        // "IP", "IPv6", etc.
	ParamName  string        // "dst", "src", etc.
	Values     []interface{} // All values across packets
}

// ScapyCodegenV2 generates Go code from IR JSON
type ScapyCodegenV2 struct {
	stripVLAN bool
}

// NewScapyCodegenV2 creates a new code generator
func NewScapyCodegenV2(stripVLAN bool) *ScapyCodegenV2 {
	return &ScapyCodegenV2{
		stripVLAN: stripVLAN,
	}
}

// GenerateFromIR generates Go code from IR JSON string
func (cg *ScapyCodegenV2) GenerateFromIR(irJSON string) (string, error) {
	var ir IRJSON
	if err := json.Unmarshal([]byte(irJSON), &ir); err != nil {
		return "", fmt.Errorf("failed to parse IR JSON: %w", err)
	}

	var code strings.Builder

	// Package and imports
	code.WriteString("package converted\n\n")
	code.WriteString("import (\n")
	code.WriteString("\t\"testing\"\n")
	code.WriteString("\t\"time\"\n\n")
	code.WriteString("\t\"github.com/gopacket/gopacket\"\n")
	code.WriteString("\t\"github.com/gopacket/gopacket/layers\"\n")
	code.WriteString("\t\"github.com/stretchr/testify/require\"\n\n")
	code.WriteString("\t\"github.com/yanet-platform/yanet2/tests/migration/converter/lib\"\n")
	code.WriteString(")\n\n")

	// Generate functions for each PCAP pair
	for i, pair := range ir.PCAPPairs {
		if len(pair.SendPackets) > 0 {
			funcName := fmt.Sprintf("Generate%sSend", sanitizeName(pair.SendFile))
			code.WriteString(cg.GeneratePacketFunction(funcName, pair.SendPackets, false))
			code.WriteString("\n")
		}

		if len(pair.ExpectPackets) > 0 {
			funcName := fmt.Sprintf("Generate%sExpect", sanitizeName(pair.ExpectFile))
			code.WriteString(cg.GeneratePacketFunction(funcName, pair.ExpectPackets, true))
			code.WriteString("\n")
		}

		_ = i // unused
	}

	return code.String(), nil
}

// GeneratePacketFunction generates a function that creates packets
func (cg *ScapyCodegenV2) GeneratePacketFunction(funcName string, packets []IRPacketDef, isExpect bool) string {
	// Try pattern detection first (for ≥10 packets with same structure)
	pattern := cg.detectIRPacketPattern(packets)

	if pattern != nil {
		// Generate mixed code: helper for pattern group + inline for others
		return cg.generateMixedCode(funcName, pattern, packets, isExpect)
	}

	// Fall back to existing inline generation
	var code strings.Builder

	code.WriteString(fmt.Sprintf("// %s generates packets\n", funcName))
	code.WriteString(fmt.Sprintf("func %s(t *testing.T) []gopacket.Packet {\n", funcName))
	code.WriteString("\tvar packets []gopacket.Packet\n\n")

	for i, pkt := range packets {
		code.WriteString(fmt.Sprintf("\t// Packet %d\n", i))

		// Check for special handling
		if pkt.SpecialHandling != nil {
			if handlingType, ok := pkt.SpecialHandling["type"].(string); ok {
				switch handlingType {
				case "fragment6", "fragment":
					code.WriteString(cg.generateFragmentedPacket(pkt, i, handlingType))
					continue
				}
			}
		}

		// Check for CIDR expansion in layers
		cidrLayer, cidrField := cg.findCIDRExpansion(pkt)
		if cidrLayer != nil {
			code.WriteString(cg.generateCIDRExpansionPackets(pkt, i, cidrLayer, cidrField))
			continue
		}

		// Check for port ranges in layers
		portRangeLayer, portRangeField := cg.findPortRange(pkt)
		if portRangeLayer != nil {
			code.WriteString(cg.generatePortRangePackets(pkt, i, portRangeLayer, portRangeField))
			continue
		}

		// Check for parameter arrays in layers
		paramArrayLayer, paramArrayField := cg.findParamArray(pkt)
		if paramArrayLayer != nil {
			code.WriteString(cg.generateParamArrayPackets(pkt, i, paramArrayLayer, paramArrayField))
			continue
		}

		// Regular packet - use template for better readability
		packetTemplate := `%s
		require.NoError(t, err)
		packets = append(packets, pkt)
	}

`
		code.WriteString("\t{\n")
		code.WriteString(cg.generatePacketConstruction(pkt, isExpect))
		code.WriteString(fmt.Sprintf(packetTemplate, ""))
	}

	code.WriteString("\treturn packets\n")
	code.WriteString("}\n")

	return code.String()
}

// generatePacketConstruction generates the NewPacket call
func (cg *ScapyCodegenV2) generatePacketConstruction(pkt IRPacketDef, isExpect bool) string {
	var code strings.Builder

	code.WriteString("\t\tpkt, err := lib.NewPacket(\n")

	for _, layer := range pkt.Layers {
		// Skip VLAN if stripVLAN is enabled
		if cg.stripVLAN && layer.Type == "Dot1Q" {
			continue
		}

		code.WriteString(cg.generateLayerCall(layer, isExpect))
	}

	// Add Raw layer for unknown/invalid next headers
	if cg.needsRawLayer(pkt) {
		code.WriteString("\t\t\tlib.Raw([]byte{}),\n")
	}

	code.WriteString("\t\t)\n")

	return code.String()
}

// needsRawLayer checks if a packet needs a Raw layer added for unknown protocols
func (cg *ScapyCodegenV2) needsRawLayer(pkt IRPacketDef) bool {
	// Find the last layer (could be IPv6, IP, etc.)
	if len(pkt.Layers) == 0 {
		return false
	}

	lastLayer := pkt.Layers[len(pkt.Layers)-1]

	// Check if it's IPv6 with unknown next header
	if lastLayer.Type == "IPv6" {
		if nh, ok := lastLayer.Params["nh"]; ok {
			nhValue := int(formatValueToInt(nh))
			// Known protocols that have layers: TCP(6), UDP(17), ICMP(1), ICMPv6(58)
			// Unknown protocols like RUDP(27) need Raw layer
			switch nhValue {
			case 6, 17, 1, 58: // TCP, UDP, ICMP, ICMPv6
				return false
			default:
				return true // Unknown protocol, needs Raw layer
			}
		}
	}

	// Check if it's IPv4 with unknown protocol
	if lastLayer.Type == "IP" {
		if proto, ok := lastLayer.Params["proto"]; ok {
			protoValue := int(formatValueToInt(proto))
			switch protoValue {
			case 6, 17, 1: // TCP, UDP, ICMP
				return false
			default:
				return true // Unknown protocol, needs Raw layer
			}
		}
	}

	return false
}

// generateLayerCall generates a single layer constructor call
func (cg *ScapyCodegenV2) generateLayerCall(layer IRLayer, isExpect bool) string {
	var code strings.Builder

	code.WriteString(fmt.Sprintf("\t\t\tlib.%s(\n", layer.Type))

	// Generate options based on layer type
	switch layer.Type {
	case "Ether":
		code.WriteString(cg.generateEtherOptions(layer, isExpect))
	case "Dot1Q":
		code.WriteString(cg.generateDot1QOptions(layer))
	case "IP":
		code.WriteString(cg.generateIPOptions(layer))
	case "IPv6":
		code.WriteString(cg.generateIPv6Options(layer))
	case "TCP":
		code.WriteString(cg.generateTCPOptions(layer))
	case "UDP":
		code.WriteString(cg.generateUDPOptions(layer))
	case "ICMP":
		code.WriteString(cg.generateICMPOptions(layer))
	case "ICMPv6EchoRequest", "ICMPv6EchoReply", "ICMPv6DestUnreach":
		code.WriteString(cg.generateICMPv6Options(layer))
	case "IPv6ExtHdrFragment":
		code.WriteString(cg.generateIPv6FragmentOptions(layer))
	case "GRE":
		code.WriteString(cg.generateGREOptions(layer))
	case "Raw":
		code.WriteString(cg.generateRawOptions(layer))
	default:
		// Unknown layer, add comment
		code.WriteString(fmt.Sprintf("\t\t\t\t// TODO: Unsupported layer %s\n", layer.Type))
	}

	code.WriteString("\t\t\t),\n")

	return code.String()
}

// generateEtherOptions generates Ethernet layer options
func (cg *ScapyCodegenV2) generateEtherOptions(layer IRLayer, isExpect bool) string {
	var code strings.Builder

	// Framework standard MACs
	srcMAC := "52:54:00:6b:ff:a1" // client
	dstMAC := "52:54:00:6b:ff:a5" // yanet

	if isExpect {
		// Swap for expect packets
		srcMAC, dstMAC = dstMAC, srcMAC
	}

	code.WriteString(fmt.Sprintf("\t\t\t\tlib.EtherDst(%q),\n", dstMAC))
	code.WriteString(fmt.Sprintf("\t\t\t\tlib.EtherSrc(%q),\n", srcMAC))

	return code.String()
}

// generateDot1QOptions generates VLAN options
func (cg *ScapyCodegenV2) generateDot1QOptions(layer IRLayer) string {
	var code strings.Builder

	if vlan, ok := layer.Params["vlan"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.VLANId(%v),\n", formatValue(vlan)))
	}

	return code.String()
}

// generateIPOptions generates IPv4 options
func (cg *ScapyCodegenV2) generateIPOptions(layer IRLayer) string {
	var code strings.Builder

	if src, ok := layer.Params["src"]; ok {
		srcStr := fmt.Sprintf("%v", src)
		// Check if this is a loop variable (from CIDR expansion)
		if srcStr == "ip" {
			code.WriteString("\t\t\t\tlib.IPSrc(ip),\n")
		} else {
			// Just use the value as-is, CIDR will be stripped
			// CIDR expansion is handled at packet level via _special marker
			cleanIP := stripCIDR(srcStr)
			if err := validateIP(cleanIP); err != nil {
				code.WriteString(fmt.Sprintf("\t\t\t\t// WARNING: %s\n", err.Error()))
			}
			code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPSrc(%q),\n", cleanIP))
		}
	}
	if dst, ok := layer.Params["dst"]; ok {
		dstStr := fmt.Sprintf("%v", dst)
		// Check if this is a loop variable (from CIDR expansion)
		if dstStr == "ip" {
			code.WriteString("\t\t\t\tlib.IPDst(ip),\n")
		} else {
			// Just use the value as-is, CIDR will be stripped
			// CIDR expansion is handled at packet level via _special marker
			cleanIP := stripCIDR(dstStr)
			if err := validateIP(cleanIP); err != nil {
				code.WriteString(fmt.Sprintf("\t\t\t\t// WARNING: %s\n", err.Error()))
			}
			code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPDst(%q),\n", cleanIP))
		}
	}
	if ttl, ok := layer.Params["ttl"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPTTL(%v),\n", formatValue(ttl)))
	}
	if tos, ok := layer.Params["tos"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPTOS(%v),\n", formatValue(tos)))
	}
	if proto, ok := layer.Params["proto"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPProto(layers.IPProtocol(%v)),\n", formatValue(proto)))
	}
	if id, ok := layer.Params["id"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPId(%v),\n", formatValue(id)))
	}

	return code.String()
}

// generateIPv6Options generates IPv6 options
func (cg *ScapyCodegenV2) generateIPv6Options(layer IRLayer) string {
	var code strings.Builder

	if src, ok := layer.Params["src"]; ok {
		srcStr := fmt.Sprintf("%v", src)
		// Check if this is a loop variable (from CIDR expansion)
		if srcStr == "ip" {
			code.WriteString("\t\t\t\tlib.IPv6Src(ip),\n")
		} else {
			// CIDR expansion is handled at packet level via _special marker
			cleanIP := stripCIDR(srcStr)
			if err := validateIP(cleanIP); err != nil {
				code.WriteString(fmt.Sprintf("\t\t\t\t// WARNING: %s\n", err.Error()))
			}
			code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPv6Src(%q),\n", cleanIP))
		}
	}
	if dst, ok := layer.Params["dst"]; ok {
		dstStr := fmt.Sprintf("%v", dst)
		// Check if this is a loop variable (from CIDR expansion)
		if dstStr == "ip" {
			code.WriteString("\t\t\t\tlib.IPv6Dst(ip),\n")
		} else {
			// CIDR expansion is handled at packet level via _special marker
			cleanIP := stripCIDR(dstStr)
			if err := validateIP(cleanIP); err != nil {
				code.WriteString(fmt.Sprintf("\t\t\t\t// WARNING: %s\n", err.Error()))
			}
			code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPv6Dst(%q),\n", cleanIP))
		}
	}
	if hlim, ok := layer.Params["hlim"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPv6HopLimit(%v),\n", formatValue(hlim)))
	}
	if tc, ok := layer.Params["tc"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPv6TrafficClass(%v),\n", formatValue(tc)))
	}
	if fl, ok := layer.Params["fl"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPv6FlowLabel(%v),\n", formatValue(fl)))
	}
	if nh, ok := layer.Params["nh"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPv6NextHeader(layers.IPProtocol(%v)),\n", formatValue(nh)))
	}
	// Add plen if specified (for testing invalid packets with wrong payload length)
	if plen, ok := layer.Params["plen"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\t// TODO: Set payload length to %v (not supported by lib yet)\n", formatValue(plen)))
	}

	return code.String()
}

// generateTCPOptions generates TCP options
func (cg *ScapyCodegenV2) generateTCPOptions(layer IRLayer) string {
	var code strings.Builder

	if sport, ok := layer.Params["sport"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.TCPSport(%v),\n", formatValue(sport)))
	}
	if dport, ok := layer.Params["dport"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.TCPDport(%v),\n", formatValue(dport)))
	}
	if flags, ok := layer.Params["flags"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.TCPFlags(%q),\n", flags))
	}
	if seq, ok := layer.Params["seq"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.TCPSeq(%v),\n", formatValue(seq)))
	}
	if ack, ok := layer.Params["ack"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.TCPAck(%v),\n", formatValue(ack)))
	}

	return code.String()
}

// generateUDPOptions generates UDP options
func (cg *ScapyCodegenV2) generateUDPOptions(layer IRLayer) string {
	var code strings.Builder

	if sport, ok := layer.Params["sport"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.UDPSport(%v),\n", formatValue(sport)))
	}
	if dport, ok := layer.Params["dport"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.UDPDport(%v),\n", formatValue(dport)))
	}

	return code.String()
}

// generateICMPOptions generates ICMP options
func (cg *ScapyCodegenV2) generateICMPOptions(layer IRLayer) string {
	var code strings.Builder

	// Parse type field
	if typeVal, ok := layer.Params["type"]; ok {
		if codeVal, ok2 := layer.Params["code"]; ok2 {
			code.WriteString(fmt.Sprintf("\t\t\t\tlib.ICMPTypeCode(%v, %v),\n",
				formatValue(typeVal), formatValue(codeVal)))
		} else {
			code.WriteString(fmt.Sprintf("\t\t\t\tlib.ICMPTypeCode(%v, 0),\n", formatValue(typeVal)))
		}
	}
	if id, ok := layer.Params["id"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.ICMPId(%v),\n", formatValue(id)))
	}
	if seq, ok := layer.Params["seq"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.ICMPSeq(%v),\n", formatValue(seq)))
	}

	return code.String()
}

// generateICMPv6Options generates ICMPv6 options
func (cg *ScapyCodegenV2) generateICMPv6Options(layer IRLayer) string {
	var code strings.Builder

	if id, ok := layer.Params["id"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.ICMPv6Id(%v),\n", formatValue(id)))
	}
	if seq, ok := layer.Params["seq"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.ICMPv6Seq(%v),\n", formatValue(seq)))
	}
	if codeVal, ok := layer.Params["code"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.ICMPv6Code(%v),\n", formatValue(codeVal)))
	}

	return code.String()
}

// generateIPv6FragmentOptions generates IPv6 Fragment header options
func (cg *ScapyCodegenV2) generateIPv6FragmentOptions(layer IRLayer) string {
	var code strings.Builder

	if id, ok := layer.Params["id"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPv6FragId(%v),\n", formatValue(id)))
	}
	if offset, ok := layer.Params["offset"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPv6FragOffset(%v),\n", formatValue(offset)))
	}
	if m, ok := layer.Params["m"]; ok {
		mVal := formatValue(m) != "0"
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPv6FragM(%v),\n", mVal))
	}

	return code.String()
}

// generateGREOptions generates GRE options
func (cg *ScapyCodegenV2) generateGREOptions(layer IRLayer) string {
	var code strings.Builder

	if chksum, ok := layer.Params["chksum_present"]; ok && formatValue(chksum) != "0" {
		code.WriteString("\t\t\t\tlib.GREChecksumPresent(true),\n")
	}
	if key, ok := layer.Params["key_present"]; ok && formatValue(key) != "0" {
		code.WriteString("\t\t\t\tlib.GREKeyPresent(true),\n")
	}
	if seq, ok := layer.Params["seqnum_present"]; ok && formatValue(seq) != "0" {
		code.WriteString("\t\t\t\tlib.GRESeqPresent(true),\n")
	}
	if ver, ok := layer.Params["version"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.GREVersion(%v),\n", formatValue(ver)))
	}
	if keyVal, ok := layer.Params["key"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.GREKey(%v),\n", formatValue(keyVal)))
	}

	return code.String()
}

// generateRawOptions generates Raw/Payload options
func (cg *ScapyCodegenV2) generateRawOptions(layer IRLayer) string {
	// Check for payload in special handling
	if special, ok := layer.Params["_special"].(map[string]interface{}); ok {
		if payload, ok := special["payload"].(map[string]interface{}); ok {
			if payloadType, ok := payload["type"].(string); ok && payloadType == "string_mult" {
				content := payload["content"].(string)
				count := payload["count"]
				return fmt.Sprintf("\t\t\t\tlib.Raw(lib.Payload(%q, %v)),\n", content, formatValue(count))
			}
		}
	}

	// Direct string payloads
	for key, val := range layer.Params {
		if strings.HasPrefix(key, "_arg") {
			return fmt.Sprintf("\t\t\t\tlib.Raw([]byte(%q)),\n", val)
		}
	}

	return ""
}

// findPortRange finds if any layer has a port range
func (cg *ScapyCodegenV2) findPortRange(pkt IRPacketDef) (*IRLayer, string) {
	for i := range pkt.Layers {
		layer := &pkt.Layers[i]
		if special, ok := layer.Params["_special"].(map[string]interface{}); ok {
			for field, handling := range special {
				if handlingMap, ok := handling.(map[string]interface{}); ok {
					if handlingType, ok := handlingMap["type"].(string); ok && handlingType == "port_range" {
						return layer, field
					}
				}
			}
		}
	}
	return nil, ""
}

// findParamArray finds if any layer has a parameter array
func (cg *ScapyCodegenV2) findParamArray(pkt IRPacketDef) (*IRLayer, string) {
	for i := range pkt.Layers {
		layer := &pkt.Layers[i]
		if special, ok := layer.Params["_special"].(map[string]interface{}); ok {
			for field, handling := range special {
				if handlingMap, ok := handling.(map[string]interface{}); ok {
					if handlingType, ok := handlingMap["type"].(string); ok && handlingType == "param_array" {
						return layer, field
					}
				}
			}
		}
	}
	return nil, ""
}

// findCIDRExpansion finds CIDR expansion in packet layers
func (cg *ScapyCodegenV2) findCIDRExpansion(pkt IRPacketDef) (*IRLayer, string) {
	for i := range pkt.Layers {
		layer := &pkt.Layers[i]
		if special, ok := layer.Params["_special"].(map[string]interface{}); ok {
			for field, handling := range special {
				if handlingMap, ok := handling.(map[string]interface{}); ok {
					if handlingType, ok := handlingMap["type"].(string); ok && handlingType == "cidr_expansion" {
						return layer, field
					}
				}
			}
		}
	}
	return nil, ""
}

// generatePortRangePackets generates packets with port ranges
func (cg *ScapyCodegenV2) generatePortRangePackets(pkt IRPacketDef, idx int, portLayer *IRLayer, field string) string {
	var code strings.Builder

	// Extract range from special handling
	special := portLayer.Params["_special"].(map[string]interface{})
	rangeInfo := special[field].(map[string]interface{})
	rangeVals := rangeInfo["range"].([]interface{})
	start := int(rangeVals[0].(float64))
	end := int(rangeVals[1].(float64))

	code.WriteString(fmt.Sprintf("\tfor _, port := range lib.PortRange(%d, %d) {\n", start, end))

	// Temporarily set the port value
	originalValue := portLayer.Params[field]
	portLayer.Params[field] = "port"

	// Use template for better readability
	portRangeTemplate := `%s
		require.NoError(t, err)
		packets = append(packets, pkt)
	}

`
	code.WriteString(cg.generatePacketConstruction(pkt, false))
	code.WriteString(fmt.Sprintf(portRangeTemplate, ""))

	// Restore original
	portLayer.Params[field] = originalValue

	return code.String()
}

// generateParamArrayPackets generates multiple packets from a parameter array
func (cg *ScapyCodegenV2) generateParamArrayPackets(pkt IRPacketDef, idx int, paramLayer *IRLayer, field string) string {
	var code strings.Builder

	// Extract values from special handling
	special := paramLayer.Params["_special"].(map[string]interface{})
	arrayInfo := special[field].(map[string]interface{})
	values := arrayInfo["values"].([]interface{})

	code.WriteString(fmt.Sprintf("\t// Generate packets with different %s values\n", field))
	code.WriteString("\tfor _, val := range []int{")

	// Write all values
	for i, val := range values {
		if i > 0 {
			code.WriteString(", ")
		}
		code.WriteString(fmt.Sprintf("%v", formatValue(val)))
	}
	code.WriteString("} {\n")

	// Temporarily set the parameter value to the loop variable
	originalValue := paramLayer.Params[field]
	paramLayer.Params[field] = "val"

	// Use template for better readability
	paramArrayTemplate := `%s
		require.NoError(t, err)
		packets = append(packets, pkt)
	}

`
	code.WriteString(cg.generatePacketConstruction(pkt, false))
	code.WriteString(fmt.Sprintf(paramArrayTemplate, ""))

	// Restore original
	paramLayer.Params[field] = originalValue

	// Remove the _special entry to avoid issues in subsequent processing
	delete(special, field)
	if len(special) == 0 {
		delete(paramLayer.Params, "_special")
	}

	return code.String()
}

// generateCIDRExpansionPackets generates packets for all IPs in CIDR subnet
func (cg *ScapyCodegenV2) generateCIDRExpansionPackets(pkt IRPacketDef, idx int, cidrLayer *IRLayer, field string) string {
	var code strings.Builder

	// Extract CIDR from special handling
	special := cidrLayer.Params["_special"].(map[string]interface{})
	cidrInfo := special[field].(map[string]interface{})
	cidrStr := cidrInfo["cidr"].(string)

	code.WriteString(fmt.Sprintf("\t// Generate packets for all IPs in CIDR %s\n", cidrStr))
	code.WriteString(fmt.Sprintf("\tfor _, ip := range lib.ExpandCIDR(%q) {\n", cidrStr))

	// Temporarily replace the IP value with loop variable "ip"
	originalValue := cidrLayer.Params[field]
	cidrLayer.Params[field] = "ip"

	// Generate packet construction with loop variable
	code.WriteString(cg.generatePacketConstruction(pkt, false))

	// Use template for better readability
	cidrTemplate := `
		require.NoError(t, err)
		packets = append(packets, pkt)
	}

`
	code.WriteString(cidrTemplate)

	// Restore original
	cidrLayer.Params[field] = originalValue

	// Remove the _special entry to avoid issues in subsequent processing
	delete(special, field)
	if len(special) == 0 {
		delete(cidrLayer.Params, "_special")
	}

	return code.String()
}

// generateFragmentedPacket generates fragmented packet code
func (cg *ScapyCodegenV2) generateFragmentedPacket(pkt IRPacketDef, idx int, fragType string) string {
	var code strings.Builder

	fragSize := 1280 // default
	if pkt.SpecialHandling != nil {
		if size, ok := pkt.SpecialHandling["frag_size"]; ok {
			if sizeInt, ok := size.(float64); ok {
				fragSize = int(sizeInt)
			}
		}
	}

	// Use template for fragmentation with better readability
	fragTemplate := `	{
		// Base packet for fragmentation
%s
		require.NoError(t, err)
		frags, err := lib.%s(pkt, %d)
		require.NoError(t, err)
%s
	}

`
	var fragAppend string
	// Check if we need specific fragment index
	if pkt.SpecialHandling != nil {
		if fragIdx, ok := pkt.SpecialHandling["fragment_index"]; ok && fragIdx != nil {
			fragAppend = fmt.Sprintf("\t\tpackets = append(packets, frags[%v])", formatValue(fragIdx))
		} else {
			fragAppend = "\t\tpackets = append(packets, frags...)"
		}
	} else {
		fragAppend = "\t\tpackets = append(packets, frags...)"
	}

	code.WriteString(fmt.Sprintf(fragTemplate,
		cg.generatePacketConstruction(pkt, false),
		cases.Title(language.English).String(fragType), fragSize,
		fragAppend))

	return code.String()
}

// stripCIDR removes CIDR notation from IP addresses
func stripCIDR(s string) string {
	if idx := strings.Index(s, "/"); idx != -1 {
		return s[:idx]
	}
	return s
}

// formatValueToInt converts a value to int for protocol checking
func formatValueToInt(val interface{}) int64 {
	switch v := val.(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case string:
		// Try to parse as int
		if i, err := strconv.ParseInt(v, 0, 64); err == nil {
			return i
		}
		return 0
	default:
		return 0
	}
}

// formatValue formats a value for Go code
func formatValue(val interface{}) string {
	switch v := val.(type) {
	case string:
		// Check if it's a variable reference
		if strings.HasPrefix(v, "$") {
			return v[1:] // Remove $ prefix
		}
		// Check if it's hex
		if strings.HasPrefix(v, "0x") || strings.HasPrefix(v, "0X") {
			return v
		}
		return v
	case float64:
		// Check if it's an integer
		if v == float64(int(v)) {
			return fmt.Sprintf("%d", int(v))
		}
		return fmt.Sprintf("%v", v)
	case int:
		return fmt.Sprintf("%d", v)
	case bool:
		return fmt.Sprintf("%v", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// sanitizeName sanitizes filename to valid Go identifier
func sanitizeName(filename string) string {
	name := strings.ReplaceAll(filename, ".pcap", "")
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, ".", "_")

	// Capitalize each part after underscore
	parts := strings.Split(name, "_")
	for i := range parts {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][0:1]) + parts[i][1:]
		}
	}
	name = strings.Join(parts, "_")

	return name
}

// validateIP validates IP address format
func validateIP(ip string) error {
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("invalid IP address %q", ip)
	}
	return nil
}

// detectIRPacketPattern analyzes packets to find common structure and varying parameters
// It tries to find the largest group of consecutive packets with the same layer structure
func (cg *ScapyCodegenV2) detectIRPacketPattern(packets []IRPacketDef) *IRPacketPattern {
	if len(packets) < 3 {
		return nil
	}

	// Find the largest consecutive group with the same layer structure
	bestStart := 0
	bestEnd := 0
	bestCount := 0

	for start := 0; start < len(packets); start++ {
		refPacket := packets[start]

		// Skip packets with special handling
		if len(refPacket.SpecialHandling) > 0 {
			continue
		}

		// Find how many consecutive packets match this structure
		end := start
		for end < len(packets) {
			pkt := packets[end]

			// Check special handling
			if len(pkt.SpecialHandling) > 0 {
				break
			}

			// Check layer structure
			if len(pkt.Layers) != len(refPacket.Layers) {
				break
			}

			matches := true
			for i := range pkt.Layers {
				if pkt.Layers[i].Type != refPacket.Layers[i].Type {
					matches = false
					break
				}
			}

			if !matches {
				break
			}

			end++
		}

		count := end - start
		if count > bestCount {
			bestCount = count
			bestStart = start
			bestEnd = end
		}
	}

	// Need at least 3 packets in the group
	if bestCount < 3 {
		return nil
	}

	// Use the best group for pattern detection
	groupPackets := packets[bestStart:bestEnd]
	refPacket := groupPackets[0]

	// Now analyze each layer to find varying parameters
	pattern := &IRPacketPattern{
		CommonLayers:  make([]IRLayer, len(refPacket.Layers)),
		VaryingParams: []IRVaryingParam{},
		StartIndex:    bestStart,
		EndIndex:      bestEnd,
	}

	// For each layer
	for layerIdx, refLayer := range refPacket.Layers {
		commonParams := make(map[string]interface{})
		varyingParamNames := make(map[string]bool)

		// Check each parameter
		for paramName, refValue := range refLayer.Params {
			isConstant := true
			values := []interface{}{refValue}

			// Compare with all other packets in the group
			for _, pkt := range groupPackets[1:] {
				if pkt.Layers[layerIdx].Params[paramName] != refValue {
					// Check if values are comparable (both exist and different)
					otherValue, exists := pkt.Layers[layerIdx].Params[paramName]
					if !exists {
						// Parameter missing in some packets - can't pattern match
						return nil
					}

					// Values differ - this is a varying parameter
					isConstant = false
					values = append(values, otherValue)
				} else {
					values = append(values, refValue)
				}
			}

			if isConstant {
				commonParams[paramName] = refValue
			} else {
				varyingParamNames[paramName] = true
				pattern.VaryingParams = append(pattern.VaryingParams, IRVaryingParam{
					LayerIndex: layerIdx,
					LayerType:  refLayer.Type,
					ParamName:  paramName,
					Values:     values,
				})
			}
		}

		// Create common layer with only constant parameters
		pattern.CommonLayers[layerIdx] = IRLayer{
			Type:   refLayer.Type,
			Params: commonParams,
		}
	}

	// If no varying parameters found, no point in pattern matching
	if len(pattern.VaryingParams) == 0 {
		return nil
	}

	return pattern
}

// generateMixedCode generates code that uses a helper for the pattern group and inline for other packets
func (cg *ScapyCodegenV2) generateMixedCode(funcName string, pattern *IRPacketPattern, packets []IRPacketDef, isExpect bool) string {
	var code strings.Builder

	// Generate helper function first
	helperCode := cg.generateHelperFunction(funcName, pattern, packets, isExpect)
	code.WriteString(helperCode)
	code.WriteString("\n")

	// Generate main function
	code.WriteString(fmt.Sprintf("// %s generates packets\n", funcName))
	code.WriteString(fmt.Sprintf("func %s(t *testing.T) []gopacket.Packet {\n", funcName))
	code.WriteString("\tvar packets []gopacket.Packet\n\n")

	// Generate inline code for packets before the pattern group
	for i := 0; i < pattern.StartIndex; i++ {
		pkt := packets[i]
		code.WriteString(fmt.Sprintf("\t// Packet %d\n", i))
		code.WriteString("\t{\n")
		code.WriteString(cg.generateSinglePacketCode(pkt, isExpect))
		code.WriteString("\t}\n\n")
	}

	// Generate helper call for the pattern group
	if pattern.StartIndex < pattern.EndIndex {
		code.WriteString(fmt.Sprintf("\t// Packets %d-%d (using helper)\n", pattern.StartIndex, pattern.EndIndex-1))
		code.WriteString(cg.generateHelperCallCode(funcName, pattern, packets))
		code.WriteString("\n")
	}

	// Generate inline code for packets after the pattern group
	for i := pattern.EndIndex; i < len(packets); i++ {
		pkt := packets[i]
		code.WriteString(fmt.Sprintf("\t// Packet %d\n", i))
		code.WriteString("\t{\n")
		code.WriteString(cg.generateSinglePacketCode(pkt, isExpect))
		code.WriteString("\t}\n\n")
	}

	code.WriteString("\treturn packets\n")
	code.WriteString("}\n")

	return code.String()
}

// generateHelperFunction generates only the helper function for pattern-based packets
func (cg *ScapyCodegenV2) generateHelperFunction(funcName string, pattern *IRPacketPattern, packets []IRPacketDef, isExpect bool) string {
	var code strings.Builder

	helperName := funcName + "Helper"

	// Count active varying parameters (excluding stripped layers)
	activeParams := 0
	for _, vp := range pattern.VaryingParams {
		if cg.stripVLAN && vp.LayerType == "Dot1Q" {
			continue
		}
		activeParams++
	}

	// Determine if we need a struct or simple parameters
	useStruct := activeParams > 1

	// Generate parameter struct if needed
	if useStruct {
		structName := funcName + "Params"
		code.WriteString(fmt.Sprintf("// %s holds varying parameters for packet generation\n", structName))
		code.WriteString(fmt.Sprintf("type %s struct {\n", structName))
		for _, vp := range pattern.VaryingParams {
			// Skip parameters for layers that will be stripped
			if cg.stripVLAN && vp.LayerType == "Dot1Q" {
				continue
			}
			fieldName := cg.makeFieldName(vp.LayerType, vp.ParamName)
			fieldType := cg.inferFieldTypeForParam(vp.LayerType, vp.ParamName, vp.Values)
			code.WriteString(fmt.Sprintf("\t%s %s\n", fieldName, fieldType))
		}
		code.WriteString("}\n\n")
	}

	// Generate helper function
	code.WriteString(fmt.Sprintf("// %s generates a single packet with varying parameters\n", helperName))
	if useStruct {
		structName := funcName + "Params"
		code.WriteString(fmt.Sprintf("func %s(t *testing.T, params %s) gopacket.Packet {\n", helperName, structName))
	} else {
		// Single parameter - find the active one
		var activeVP *IRVaryingParam
		for i, vp := range pattern.VaryingParams {
			if cg.stripVLAN && vp.LayerType == "Dot1Q" {
				continue
			}
			activeVP = &pattern.VaryingParams[i]
			break
		}
		if activeVP != nil {
			paramName := cg.makeParamName(activeVP.LayerType, activeVP.ParamName)
			paramType := cg.inferFieldTypeForParam(activeVP.LayerType, activeVP.ParamName, activeVP.Values)
			code.WriteString(fmt.Sprintf("func %s(t *testing.T, %s %s) gopacket.Packet {\n", helperName, paramName, paramType))
		}
	}

	// Generate lib.NewPacket call with layers
	code.WriteString("\tpkt, err := lib.NewPacket(\n")

	for layerIdx, layer := range pattern.CommonLayers {
		// Skip VLAN if stripVLAN is enabled
		if cg.stripVLAN && layer.Type == "Dot1Q" {
			continue
		}

		code.WriteString(cg.generateLayerCallWithPattern(layer, layerIdx, pattern, isExpect, useStruct, funcName))
	}

	code.WriteString("\t)\n")
	code.WriteString("\trequire.NoError(t, err)\n")
	code.WriteString("\treturn pkt\n")
	code.WriteString("}\n")

	return code.String()
}

// generateHelperCallCode generates the code that calls the helper function for the pattern group
func (cg *ScapyCodegenV2) generateHelperCallCode(funcName string, pattern *IRPacketPattern, packets []IRPacketDef) string {
	var code strings.Builder

	helperName := funcName + "Helper"

	// Count active varying parameters (excluding stripped layers)
	activeParams := 0
	for _, vp := range pattern.VaryingParams {
		if cg.stripVLAN && vp.LayerType == "Dot1Q" {
			continue
		}
		activeParams++
	}

	useStruct := activeParams > 1

	if useStruct {
		// Generate slice of structs
		structName := funcName + "Params"
		code.WriteString(fmt.Sprintf("\tparamsList := []%s{\n", structName))
		for i := pattern.StartIndex; i < pattern.EndIndex; i++ {
			code.WriteString("\t\t{")
			firstField := true
			for _, vp := range pattern.VaryingParams {
				// Skip parameters for layers that will be stripped
				if cg.stripVLAN && vp.LayerType == "Dot1Q" {
					continue
				}
				if !firstField {
					code.WriteString(", ")
				}
				firstField = false
				fieldName := cg.makeFieldName(vp.LayerType, vp.ParamName)
				code.WriteString(fmt.Sprintf("%s: %s", fieldName, cg.formatValueForCode(vp.Values[i-pattern.StartIndex])))
			}
			code.WriteString("},\n")
		}
		code.WriteString("\t}\n\n")
		code.WriteString("\tfor _, params := range paramsList {\n")
		code.WriteString(fmt.Sprintf("\t\tpackets = append(packets, %s(t, params))\n", helperName))
		code.WriteString("\t}\n")
	} else {
		// Generate slice of values - find the active parameter
		var activeVP *IRVaryingParam
		for i, vp := range pattern.VaryingParams {
			if cg.stripVLAN && vp.LayerType == "Dot1Q" {
				continue
			}
			activeVP = &pattern.VaryingParams[i]
			break
		}

		if activeVP != nil {
			varName := cg.makeParamName(activeVP.LayerType, activeVP.ParamName) + "s"
			varType := cg.inferFieldTypeForParam(activeVP.LayerType, activeVP.ParamName, activeVP.Values)
			code.WriteString(fmt.Sprintf("\t%s := []%s{\n", varName, varType))
			for _, val := range activeVP.Values {
				code.WriteString(fmt.Sprintf("\t\t%s,\n", cg.formatValueForCode(val)))
			}
			code.WriteString("\t}\n\n")
			code.WriteString(fmt.Sprintf("\tfor _, val := range %s {\n", varName))
			code.WriteString(fmt.Sprintf("\t\tpackets = append(packets, %s(t, val))\n", helperName))
			code.WriteString("\t}\n")
		}
	}

	return code.String()
}

// generateSinglePacketCode generates inline code for a single packet
func (cg *ScapyCodegenV2) generateSinglePacketCode(pkt IRPacketDef, isExpect bool) string {
	var code strings.Builder

	// Check for special handling
	if len(pkt.SpecialHandling) > 0 {
		code.WriteString(fmt.Sprintf("\t\t// Special handling: %v\n", pkt.SpecialHandling))
		code.WriteString("\t\t// TODO: implement special handling\n")
		code.WriteString("\t\tpackets = append(packets, nil) // placeholder\n")
		return code.String()
	}

	code.WriteString("\t\tpkt, err := lib.NewPacket(\n")

	for _, layer := range pkt.Layers {
		code.WriteString(cg.generateLayerCall(layer, isExpect))
	}

	code.WriteString("\t\t)\n\n")
	code.WriteString("\t\trequire.NoError(t, err)\n")
	code.WriteString("\t\tpackets = append(packets, pkt)\n")

	return code.String()
}

// generateLayerCallWithPattern generates layer call with pattern-based parameters
func (cg *ScapyCodegenV2) generateLayerCallWithPattern(layer IRLayer, layerIdx int, pattern *IRPacketPattern, isExpect bool, useStruct bool, funcName string) string {
	var code strings.Builder

	code.WriteString(fmt.Sprintf("\t\tlib.%s(\n", layer.Type))

	// Find varying params for this layer
	varyingParams := make(map[string]IRVaryingParam)
	for _, vp := range pattern.VaryingParams {
		if vp.LayerIndex == layerIdx {
			varyingParams[vp.ParamName] = vp
		}
	}

	// Generate options based on layer type
	switch layer.Type {
	case "Ether":
		code.WriteString(cg.generateEtherOptionsWithPattern(layer, varyingParams, isExpect, useStruct, funcName))
	case "Dot1Q":
		code.WriteString(cg.generateDot1QOptionsWithPattern(layer, varyingParams, useStruct, funcName))
	case "IP":
		code.WriteString(cg.generateIPOptionsWithPattern(layer, varyingParams, useStruct, funcName))
	case "IPv6":
		code.WriteString(cg.generateIPv6OptionsWithPattern(layer, varyingParams, useStruct, funcName))
	case "TCP":
		code.WriteString(cg.generateTCPOptionsWithPattern(layer, varyingParams, useStruct, funcName))
	case "UDP":
		code.WriteString(cg.generateUDPOptionsWithPattern(layer, varyingParams, useStruct, funcName))
	case "ICMP":
		code.WriteString(cg.generateICMPOptionsWithPattern(layer, varyingParams, useStruct, funcName))
	case "ICMPv6EchoRequest", "ICMPv6EchoReply", "ICMPv6DestUnreach":
		code.WriteString(cg.generateICMPv6OptionsWithPattern(layer, varyingParams, useStruct, funcName))
	case "IPv6ExtHdrFragment":
		code.WriteString(cg.generateIPv6FragmentOptionsWithPattern(layer, varyingParams, useStruct, funcName))
	case "GRE":
		code.WriteString(cg.generateGREOptionsWithPattern(layer, varyingParams, useStruct, funcName))
	case "Raw":
		code.WriteString(cg.generateRawOptionsWithPattern(layer, varyingParams, useStruct, funcName))
	}

	code.WriteString("\t\t),\n")

	return code.String()
}

// Helper functions for pattern-based generation
func (cg *ScapyCodegenV2) makeFieldName(layerType, paramName string) string {
	// Capitalize first letter
	caser := cases.Title(language.English)
	return caser.String(layerType) + caser.String(paramName)
}

func (cg *ScapyCodegenV2) makeParamName(layerType, paramName string) string {
	// Keep lowercase for parameter name
	return strings.ToLower(layerType) + strings.Title(paramName)
}

// inferFieldTypeForParam infers the type based on layer type, parameter name, and values
func (cg *ScapyCodegenV2) inferFieldTypeForParam(layerType, paramName string, values []interface{}) string {
	// Special cases for known parameter types
	if layerType == "TCP" || layerType == "UDP" {
		if paramName == "sport" || paramName == "dport" {
			return "uint16" // Ports are always uint16
		}
	}

	// Fall back to value-based inference
	return cg.inferFieldTypeFromValues(values)
}

// inferFieldTypeFromValues infers the type from all values (not just the first one)
func (cg *ScapyCodegenV2) inferFieldTypeFromValues(values []interface{}) string {
	if len(values) == 0 {
		return "interface{}"
	}

	// Check if all values are strings
	allStrings := true
	allBools := true
	maxInt := int64(0)
	minInt := int64(0)
	allInts := true

	for _, val := range values {
		switch v := val.(type) {
		case string:
			allBools = false
			allInts = false
		case bool:
			allStrings = false
			allInts = false
		case int:
			allStrings = false
			allBools = false
			if int64(v) > maxInt {
				maxInt = int64(v)
			}
			if int64(v) < minInt {
				minInt = int64(v)
			}
		case int64:
			allStrings = false
			allBools = false
			if v > maxInt {
				maxInt = v
			}
			if v < minInt {
				minInt = v
			}
		case float64:
			allStrings = false
			allBools = false
			if v == float64(int(v)) {
				if int64(v) > maxInt {
					maxInt = int64(v)
				}
				if int64(v) < minInt {
					minInt = int64(v)
				}
			} else {
				allInts = false
			}
		default:
			allStrings = false
			allBools = false
			allInts = false
		}
	}

	if allStrings {
		return "string"
	}
	if allBools {
		return "bool"
	}
	if allInts {
		// Choose type based on range
		if minInt >= 0 && maxInt <= 255 {
			return "uint8"
		} else if minInt >= 0 && maxInt <= 65535 {
			return "uint16"
		} else if minInt >= 0 && maxInt <= 4294967295 {
			return "uint32"
		}
		return "int"
	}

	return "interface{}"
}

func (cg *ScapyCodegenV2) inferFieldType(value interface{}) string {
	return cg.inferFieldTypeFromValues([]interface{}{value})
}

func (cg *ScapyCodegenV2) formatValueForCode(value interface{}) string {
	switch v := value.(type) {
	case string:
		return fmt.Sprintf("%q", v)
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case float64:
		if v == float64(int(v)) {
			return fmt.Sprintf("%d", int(v))
		}
		return fmt.Sprintf("%v", v)
	case bool:
		return fmt.Sprintf("%v", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Pattern-based option generators
func (cg *ScapyCodegenV2) generateEtherOptionsWithPattern(layer IRLayer, varyingParams map[string]IRVaryingParam, isExpect bool, useStruct bool, funcName string) string {
	var code strings.Builder

	// Framework standard MACs
	srcMAC := "52:54:00:6b:ff:a1" // client
	dstMAC := "52:54:00:6b:ff:a5" // yanet

	if isExpect {
		srcMAC, dstMAC = dstMAC, srcMAC
	}

	code.WriteString(fmt.Sprintf("\t\t\tlib.EtherDst(%q),\n", dstMAC))
	code.WriteString(fmt.Sprintf("\t\t\tlib.EtherSrc(%q),\n", srcMAC))

	return code.String()
}

func (cg *ScapyCodegenV2) generateDot1QOptionsWithPattern(layer IRLayer, varyingParams map[string]IRVaryingParam, useStruct bool, funcName string) string {
	var code strings.Builder

	if vp, ok := varyingParams["vlan"]; ok {
		code.WriteString(cg.generateVaryingParamRef("VLANId", vp, useStruct, funcName))
	} else if vlan, ok := layer.Params["vlan"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.VLANId(%v),\n", formatValue(vlan)))
	}

	return code.String()
}

func (cg *ScapyCodegenV2) generateIPOptionsWithPattern(layer IRLayer, varyingParams map[string]IRVaryingParam, useStruct bool, funcName string) string {
	var code strings.Builder

	// Source IP
	if vp, ok := varyingParams["src"]; ok {
		code.WriteString(cg.generateVaryingParamRef("IPSrc", vp, useStruct, funcName))
	} else if src, ok := layer.Params["src"]; ok {
		srcStr := fmt.Sprintf("%v", src)
		cleanIP := stripCIDR(srcStr)
		code.WriteString(fmt.Sprintf("\t\t\tlib.IPSrc(%q),\n", cleanIP))
	}

	// Destination IP
	if vp, ok := varyingParams["dst"]; ok {
		code.WriteString(cg.generateVaryingParamRef("IPDst", vp, useStruct, funcName))
	} else if dst, ok := layer.Params["dst"]; ok {
		dstStr := fmt.Sprintf("%v", dst)
		cleanIP := stripCIDR(dstStr)
		code.WriteString(fmt.Sprintf("\t\t\tlib.IPDst(%q),\n", cleanIP))
	}

	// TTL
	if vp, ok := varyingParams["ttl"]; ok {
		code.WriteString(cg.generateVaryingParamRef("IPTTL", vp, useStruct, funcName))
	} else if ttl, ok := layer.Params["ttl"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.IPTTL(%v),\n", formatValue(ttl)))
	}

	// TOS
	if vp, ok := varyingParams["tos"]; ok {
		code.WriteString(cg.generateVaryingParamRef("IPTOS", vp, useStruct, funcName))
	} else if tos, ok := layer.Params["tos"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.IPTOS(%v),\n", formatValue(tos)))
	}

	// ID
	if id, ok := layer.Params["id"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.IPId(%v),\n", formatValue(id)))
	}

	// Flags
	if flags, ok := layer.Params["flags"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.IPFlags(%v),\n", formatValue(flags)))
	}

	return code.String()
}

func (cg *ScapyCodegenV2) generateIPv6OptionsWithPattern(layer IRLayer, varyingParams map[string]IRVaryingParam, useStruct bool, funcName string) string {
	var code strings.Builder

	// Source IP
	if vp, ok := varyingParams["src"]; ok {
		code.WriteString(cg.generateVaryingParamRef("IPv6Src", vp, useStruct, funcName))
	} else if src, ok := layer.Params["src"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.IPv6Src(%q),\n", fmt.Sprintf("%v", src)))
	}

	// Destination IP
	if vp, ok := varyingParams["dst"]; ok {
		code.WriteString(cg.generateVaryingParamRef("IPv6Dst", vp, useStruct, funcName))
	} else if dst, ok := layer.Params["dst"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.IPv6Dst(%q),\n", fmt.Sprintf("%v", dst)))
	}

	// Hop Limit
	if vp, ok := varyingParams["hlim"]; ok {
		code.WriteString(cg.generateVaryingParamRef("IPv6HopLimit", vp, useStruct, funcName))
	} else if hlim, ok := layer.Params["hlim"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.IPv6HopLimit(%v),\n", formatValue(hlim)))
	}

	// Traffic Class
	if tc, ok := layer.Params["tc"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.IPv6TrafficClass(%v),\n", formatValue(tc)))
	}

	// Flow Label
	if fl, ok := layer.Params["fl"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.IPv6FlowLabel(%v),\n", formatValue(fl)))
	}

	return code.String()
}

func (cg *ScapyCodegenV2) generateTCPOptionsWithPattern(layer IRLayer, varyingParams map[string]IRVaryingParam, useStruct bool, funcName string) string {
	var code strings.Builder

	// Source Port
	if vp, ok := varyingParams["sport"]; ok {
		code.WriteString(cg.generateVaryingParamRef("TCPSport", vp, useStruct, funcName))
	} else if sport, ok := layer.Params["sport"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.TCPSport(%v),\n", formatValue(sport)))
	}

	// Destination Port
	if vp, ok := varyingParams["dport"]; ok {
		code.WriteString(cg.generateVaryingParamRef("TCPDport", vp, useStruct, funcName))
	} else if dport, ok := layer.Params["dport"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.TCPDport(%v),\n", formatValue(dport)))
	}

	// Flags
	if flags, ok := layer.Params["flags"]; ok {
		flagsStr := fmt.Sprintf("%v", flags)
		code.WriteString(fmt.Sprintf("\t\t\tlib.TCPFlags(%q),\n", flagsStr))
	}

	// Seq
	if seq, ok := layer.Params["seq"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.TCPSeq(%v),\n", formatValue(seq)))
	}

	// Ack
	if ack, ok := layer.Params["ack"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.TCPAck(%v),\n", formatValue(ack)))
	}

	return code.String()
}

func (cg *ScapyCodegenV2) generateUDPOptionsWithPattern(layer IRLayer, varyingParams map[string]IRVaryingParam, useStruct bool, funcName string) string {
	var code strings.Builder

	// Source Port
	if vp, ok := varyingParams["sport"]; ok {
		code.WriteString(cg.generateVaryingParamRef("UDPSport", vp, useStruct, funcName))
	} else if sport, ok := layer.Params["sport"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.UDPSport(%v),\n", formatValue(sport)))
	}

	// Destination Port
	if vp, ok := varyingParams["dport"]; ok {
		code.WriteString(cg.generateVaryingParamRef("UDPDport", vp, useStruct, funcName))
	} else if dport, ok := layer.Params["dport"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.UDPDport(%v),\n", formatValue(dport)))
	}

	return code.String()
}

func (cg *ScapyCodegenV2) generateICMPOptionsWithPattern(layer IRLayer, varyingParams map[string]IRVaryingParam, useStruct bool, funcName string) string {
	var code strings.Builder

	// Type
	if vp, ok := varyingParams["type"]; ok {
		code.WriteString(cg.generateVaryingParamRef("ICMPType", vp, useStruct, funcName))
	} else if icmpType, ok := layer.Params["type"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.ICMPType(%v),\n", formatValue(icmpType)))
	}

	// Code - ICMP uses TypeCode, not separate Code
	if icmpCode, ok := layer.Params["code"]; ok {
		// Code is part of TypeCode, so we skip it if Type is already set
		// The TypeCode value should include both type and code
		_ = icmpCode
	}

	return code.String()
}

func (cg *ScapyCodegenV2) generateICMPv6OptionsWithPattern(layer IRLayer, varyingParams map[string]IRVaryingParam, useStruct bool, funcName string) string {
	var code strings.Builder

	// Type
	if vp, ok := varyingParams["type"]; ok {
		code.WriteString(cg.generateVaryingParamRef("ICMPv6Type", vp, useStruct, funcName))
	} else if icmpType, ok := layer.Params["type"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.ICMPv6Type(%v),\n", formatValue(icmpType)))
	}

	// Code - ICMPv6 uses TypeCode, not separate Code
	if icmpCode, ok := layer.Params["code"]; ok {
		// Code is part of TypeCode, so we skip it if Type is already set
		_ = icmpCode
	}

	return code.String()
}

func (cg *ScapyCodegenV2) generateIPv6FragmentOptionsWithPattern(layer IRLayer, varyingParams map[string]IRVaryingParam, useStruct bool, funcName string) string {
	var code strings.Builder

	if offset, ok := layer.Params["offset"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.IPv6FragmentOffset(%v),\n", formatValue(offset)))
	}

	if m, ok := layer.Params["m"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.IPv6FragmentM(%v),\n", formatValue(m)))
	}

	if id, ok := layer.Params["id"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.IPv6FragmentID(%v),\n", formatValue(id)))
	}

	return code.String()
}

func (cg *ScapyCodegenV2) generateGREOptionsWithPattern(layer IRLayer, varyingParams map[string]IRVaryingParam, useStruct bool, funcName string) string {
	var code strings.Builder

	if proto, ok := layer.Params["proto"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.GREProto(%v),\n", formatValue(proto)))
	}

	return code.String()
}

func (cg *ScapyCodegenV2) generateRawOptionsWithPattern(layer IRLayer, varyingParams map[string]IRVaryingParam, useStruct bool, funcName string) string {
	var code strings.Builder

	if load, ok := layer.Params["load"]; ok {
		code.WriteString(fmt.Sprintf("\t\t\tlib.RawLoad(%v),\n", formatValue(load)))
	}

	return code.String()
}

// generateVaryingParamRef generates reference to varying parameter
func (cg *ScapyCodegenV2) generateVaryingParamRef(optionFunc string, vp IRVaryingParam, useStruct bool, funcName string) string {
	if useStruct {
		fieldName := cg.makeFieldName(vp.LayerType, vp.ParamName)
		return fmt.Sprintf("\t\t\tlib.%s(params.%s),\n", optionFunc, fieldName)
	} else {
		// Use parameter name from function signature
		paramName := cg.makeParamName(vp.LayerType, vp.ParamName)
		return fmt.Sprintf("\t\t\tlib.%s(%s),\n", optionFunc, paramName)
	}
}

// expandCIDR expands a CIDR notation to all IP addresses in the subnet
func (cg *ScapyCodegenV2) expandCIDR(cidr string) ([]string, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	var ips []string
	for ip := ipNet.IP.Mask(ipNet.Mask); ipNet.Contains(ip); incIP(ip) {
		ips = append(ips, ip.String())
	}

	return ips, nil
}
