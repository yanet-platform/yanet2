package lib

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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

		// Regular packet
		code.WriteString("\t{\n")
		code.WriteString(cg.generatePacketConstruction(pkt, isExpect))
		code.WriteString("\t\trequire.NoError(t, err)\n")
		code.WriteString("\t\tpackets = append(packets, pkt)\n")
		code.WriteString("\t}\n\n")
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
		srcStr := stripCIDR(fmt.Sprintf("%v", src))
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPSrc(%q),\n", srcStr))
	}
	if dst, ok := layer.Params["dst"]; ok {
		dstStr := stripCIDR(fmt.Sprintf("%v", dst))
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPDst(%q),\n", dstStr))
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
		srcStr := stripCIDR(fmt.Sprintf("%v", src))
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPv6Src(%q),\n", srcStr))
	}
	if dst, ok := layer.Params["dst"]; ok {
		dstStr := stripCIDR(fmt.Sprintf("%v", dst))
		code.WriteString(fmt.Sprintf("\t\t\t\tlib.IPv6Dst(%q),\n", dstStr))
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

	code.WriteString(cg.generatePacketConstruction(pkt, false))
	code.WriteString("\t\trequire.NoError(t, err)\n")
	code.WriteString("\t\tpackets = append(packets, pkt)\n")
	code.WriteString("\t}\n\n")

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

	code.WriteString(cg.generatePacketConstruction(pkt, false))
	code.WriteString("\t\trequire.NoError(t, err)\n")
	code.WriteString("\t\tpackets = append(packets, pkt)\n")
	code.WriteString("\t}\n\n")

	// Restore original
	paramLayer.Params[field] = originalValue

	// Remove the _special entry to avoid issues in subsequent processing
	delete(special, field)
	if len(special) == 0 {
		delete(paramLayer.Params, "_special")
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

	code.WriteString("\t{\n")
	code.WriteString("\t\t// Base packet for fragmentation\n")
	code.WriteString(cg.generatePacketConstruction(pkt, false))
	code.WriteString("\t\trequire.NoError(t, err)\n")
	code.WriteString(fmt.Sprintf("\t\tfrags, err := lib.%s(pkt, %d)\n",
		strings.Title(fragType), fragSize))
	code.WriteString("\t\trequire.NoError(t, err)\n")

	// Check if we need specific fragment index
	if pkt.SpecialHandling != nil {
		if fragIdx, ok := pkt.SpecialHandling["fragment_index"]; ok && fragIdx != nil {
			code.WriteString(fmt.Sprintf("\t\tpackets = append(packets, frags[%v])\n", formatValue(fragIdx)))
		} else {
			code.WriteString("\t\tpackets = append(packets, frags...)\n")
		}
	} else {
		code.WriteString("\t\tpackets = append(packets, frags...)\n")
	}

	code.WriteString("\t}\n\n")

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
