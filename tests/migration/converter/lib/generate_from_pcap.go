package lib

import (
	"fmt"

	"github.com/gopacket/gopacket/layers"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// GeneratePacketsFromPCAP reads a PCAP file and generates packet bytes using the converter pipeline.
// This mimics exactly what the converter does: PCAP → PacketInfo → IR → lib.NewPacket → bytes.
func GeneratePacketsFromPCAP(pcapPath string, opts CodegenOpts) ([][]byte, error) {
	// Create analyzer
	analyzer := NewPcapAnalyzer(false)

	// Read all packets from PCAP
	packetInfos, err := analyzer.ReadAllPacketsFromFile(pcapPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read PCAP: %w", err)
	}

	if len(packetInfos) == 0 {
		return nil, fmt.Errorf("no packets in PCAP")
	}

	// Convert PacketInfo to IR (same as converter does)
	ir, err := analyzer.ConvertPacketInfoToIR(packetInfos, "send.pcap", "expect.pcap", opts)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to IR: %w", err)
	}

	// Extract packets from IR
	var irPackets []IRPacketDef
	if len(ir.PCAPPairs) > 0 {
		if opts.IsExpect {
			irPackets = ir.PCAPPairs[0].ExpectPackets
		} else {
			irPackets = ir.PCAPPairs[0].SendPackets
		}
	}

	if len(irPackets) == 0 {
		return nil, fmt.Errorf("no IR packets generated")
	}

	// Generate packets from IR using the same logic as ScapyCodegenV2
	var result [][]byte
	for _, irPkt := range irPackets {
		pktBytes, err := generatePacketFromIR(irPkt, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to generate packet from IR: %w", err)
		}
		result = append(result, pktBytes)
	}

	return result, nil
}

// generatePacketFromIR converts an IR packet definition to bytes using lib.NewPacket
// This uses the same IR→packet logic as verify_nat64_test.go
func generatePacketFromIR(irPkt IRPacketDef, opts CodegenOpts) ([]byte, error) {
	var layerBuilders []LayerBuilder

	for _, layer := range irPkt.Layers {
		// Skip VLAN if stripVLAN is enabled
		if opts.StripVLAN && layer.Type == "Dot1Q" {
			continue
		}

		// Use the same buildLayerFromIR logic as in verify_nat64_test.go
		builder := buildLayerFromIRForGenerate(layer, opts.IsExpect)
		if builder != nil {
			layerBuilders = append(layerBuilders, builder)
		}
	}

	// Create packet using lib.NewPacket (same as generated code does)
	pkt, err := NewPacket(layerBuilders...)
	if err != nil {
		return nil, fmt.Errorf("NewPacket failed: %w", err)
	}

	return pkt.Data(), nil
}

// buildLayerFromIRForGenerate is a simplified version that delegates to verify_nat64_test.go logic
// We inline the minimal logic here to avoid circular dependencies
func buildLayerFromIRForGenerate(layer IRLayer, isExpect bool) LayerBuilder {
	switch layer.Type {
	case "Ether":
		// Prefer IR-provided MACs; fall back to framework defaults only if absent
		if dst, ok := layer.Params["dst"].(string); ok {
			if src, ok2 := layer.Params["src"].(string); ok2 {
				return Ether(EtherDst(dst), EtherSrc(src))
			}
		}
		// Fallback to framework defaults (no swapping here)
		return Ether(EtherDst(framework.DstMAC), EtherSrc(framework.SrcMAC))

	case "Dot1Q":
		if vlan, ok := layer.Params["vlan"].(float64); ok {
			return Dot1Q(VLANId(uint16(vlan)))
		}
		return Dot1Q()

	case "IP":
		var opts []IPv4Option
		if src, ok := layer.Params["src"].(string); ok {
			opts = append(opts, IPSrc(src))
		}
		if dst, ok := layer.Params["dst"].(string); ok {
			opts = append(opts, IPDst(dst))
		}
		if ttl, ok := layer.Params["ttl"].(float64); ok {
			opts = append(opts, IPTTL(uint8(ttl)))
		}
		if tos, ok := layer.Params["tos"].(float64); ok {
			opts = append(opts, IPTOS(uint8(tos)))
		}
		if id, ok := layer.Params["id"].(float64); ok {
			opts = append(opts, IPId(uint16(id)))
		}
		if proto, ok := layer.Params["proto"].(float64); ok {
			opts = append(opts, IPProto(layers.IPProtocol(proto)))
		}
		if flags, ok := layer.Params["flags"].(float64); ok {
			opts = append(opts, IPFlags(layers.IPv4Flag(flags)))
		}
		if frag, ok := layer.Params["frag"].(float64); ok {
			opts = append(opts, IPFragOffset(uint16(frag)))
		}
		return IP(opts...)

	case "IPv6":
		var opts []IPv6Option
		if src, ok := layer.Params["src"].(string); ok {
			opts = append(opts, IPv6Src(src))
		}
		if dst, ok := layer.Params["dst"].(string); ok {
			opts = append(opts, IPv6Dst(dst))
		}
		if hlim, ok := layer.Params["hlim"].(float64); ok {
			opts = append(opts, IPv6HopLimit(uint8(hlim)))
		}
		if tc, ok := layer.Params["tc"].(float64); ok {
			opts = append(opts, IPv6TrafficClass(uint8(tc)))
		}
		if fl, ok := layer.Params["fl"].(float64); ok {
			opts = append(opts, IPv6FlowLabel(uint32(fl)))
		}
		return IPv6(opts...)

	case "TCP":
		var opts []TCPOption
		if sport, ok := layer.Params["sport"].(float64); ok {
			opts = append(opts, TCPSport(uint16(sport)))
		}
		if dport, ok := layer.Params["dport"].(float64); ok {
			opts = append(opts, TCPDport(uint16(dport)))
		}
		if seq, ok := layer.Params["seq"].(float64); ok {
			opts = append(opts, TCPSeq(uint32(seq)))
		}
		if ack, ok := layer.Params["ack"].(float64); ok {
			opts = append(opts, TCPAck(uint32(ack)))
		}
		if flags, ok := layer.Params["flags"].(string); ok {
			opts = append(opts, TCPFlags(flags))
		}
		if win, ok := layer.Params["window"].(float64); ok {
			opts = append(opts, TCPWindow(uint16(win)))
		}
		if urg, ok := layer.Params["urg"].(float64); ok {
			opts = append(opts, TCPUrgent(uint16(urg)))
		}
		return TCP(opts...)

	case "UDP":
		var opts []UDPOption
		if sport, ok := layer.Params["sport"].(float64); ok {
			opts = append(opts, UDPSport(uint16(sport)))
		}
		if dport, ok := layer.Params["dport"].(float64); ok {
			opts = append(opts, UDPDport(uint16(dport)))
		}
		if chksum, ok := layer.Params["chksum"].(float64); ok {
			opts = append(opts, UDPChecksumRaw(uint16(chksum)))
		}
		return UDP(opts...)

	case "ICMP":
		var opts []ICMPOption
		if typeVal, ok := layer.Params["type"].(float64); ok {
			code := 0
			if codeVal, ok := layer.Params["code"].(float64); ok {
				code = int(codeVal)
			}
			opts = append(opts, ICMPTypeCode(uint8(typeVal), uint8(code)))
		}
		if id, ok := layer.Params["id"].(float64); ok {
			opts = append(opts, ICMPId(uint16(id)))
		}
		if seq, ok := layer.Params["seq"].(float64); ok {
			opts = append(opts, ICMPSeq(uint16(seq)))
		}
		return ICMP(opts...)

	case "ICMPv6EchoRequest":
		var opts []ICMPv6Option
		if id, ok := layer.Params["id"].(float64); ok {
			opts = append(opts, ICMPv6Id(uint16(id)))
		}
		if seq, ok := layer.Params["seq"].(float64); ok {
			opts = append(opts, ICMPv6Seq(uint16(seq)))
		}
		return ICMPv6EchoRequest(opts...)

	case "ICMPv6EchoReply":
		var opts []ICMPv6Option
		if id, ok := layer.Params["id"].(float64); ok {
			opts = append(opts, ICMPv6Id(uint16(id)))
		}
		if seq, ok := layer.Params["seq"].(float64); ok {
			opts = append(opts, ICMPv6Seq(uint16(seq)))
		}
		return ICMPv6EchoReply(opts...)

	case "ICMPv6DestUnreach":
		var opts []ICMPv6Option
		if code, ok := layer.Params["code"].(float64); ok {
			opts = append(opts, ICMPv6Code(uint8(code)))
		}
		return ICMPv6DestUnreach(opts...)

	case "ICMPv6Echo":
		var opts []ICMPv6EchoOption
		if id, ok := layer.Params["id"].(float64); ok {
			opts = append(opts, ICMPv6EchoId(uint16(id)))
		}
		if seq, ok := layer.Params["seq"].(float64); ok {
			opts = append(opts, ICMPv6EchoSeq(uint16(seq)))
		}
		return ICMPv6Echo(opts...)

	case "IPv6ExtHdrFragment":
		var opts []IPv6FragmentOption
		if id, ok := layer.Params["id"].(float64); ok {
			opts = append(opts, IPv6FragId(uint32(id)))
		}
		if offset, ok := layer.Params["offset"].(float64); ok {
			opts = append(opts, IPv6FragOffset(uint16(offset)))
		}
		if m, ok := layer.Params["m"].(float64); ok {
			opts = append(opts, IPv6FragM(m != 0))
		}
		if nh, ok := layer.Params["nh"].(float64); ok {
			opts = append(opts, IPv6FragNextHeader(layers.IPProtocol(nh)))
		}
		return IPv6ExtHdrFragment(opts...)

	case "Raw":
		if arg0, ok := layer.Params["_arg0"].(string); ok {
			return Raw([]byte(arg0))
		}
		return Raw([]byte{})

	case "MPLS":
		var opts []MPLSOption
		if label, ok := layer.Params["label"].(float64); ok {
			opts = append(opts, MPLSLabel(uint32(label)))
		}
		if ttl, ok := layer.Params["ttl"].(float64); ok {
			opts = append(opts, MPLSTTL(uint8(ttl)))
		}
		if s, ok := layer.Params["s"].(float64); ok {
			opts = append(opts, MPLSStackBit(s == 1))
		}
		if cos, ok := layer.Params["cos"].(float64); ok {
			opts = append(opts, MPLSTrafficClass(uint8(cos)))
		}
		return MPLS(opts...)

	default:
		return nil
	}
}
