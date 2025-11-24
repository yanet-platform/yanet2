package lib

import (
	"fmt"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// ApplyFrameworkMapping applies framework MAC/IP address mappings to a packet
// and returns the reserialized bytes. This preserves VLAN and TTL/HopLimit.
// isExpect=true swaps MACs (yanet→client direction)
func ApplyFrameworkMapping(pkt gopacket.Packet, isExpect bool) ([]byte, error) {
	// Extract layers
	var layerBuilders []LayerBuilder

	// Process Ethernet layer
	if ethLayer := pkt.Layer(layers.LayerTypeEthernet); ethLayer != nil {
		// Apply framework MAC mapping
		var srcMAC, dstMAC string
		if isExpect {
			// Expect: yanet → client
			srcMAC = framework.DstMAC // yanet
			dstMAC = framework.SrcMAC // client
		} else {
			// Send: client → yanet
			srcMAC = framework.SrcMAC // client
			dstMAC = framework.DstMAC // yanet
		}

		layerBuilders = append(layerBuilders, Ether(
			EtherSrc(srcMAC),
			EtherDst(dstMAC),
		))
	}

	// Process VLAN layer (preserve it)
	if vlanLayer := pkt.Layer(layers.LayerTypeDot1Q); vlanLayer != nil {
		vlan := vlanLayer.(*layers.Dot1Q)
		layerBuilders = append(layerBuilders, Dot1Q(
			VLANId(vlan.VLANIdentifier),
		))
	}

	// Process IPv4 layer
	if ipv4Layer := pkt.Layer(layers.LayerTypeIPv4); ipv4Layer != nil {
		ipv4 := ipv4Layer.(*layers.IPv4)

		// Apply IP address mapping
		srcIP := adaptIPAddress(ipv4.SrcIP.String())
		dstIP := adaptIPAddress(ipv4.DstIP.String())

		opts := []IPv4Option{
			IPSrc(srcIP),
			IPDst(dstIP),
			IPTTL(ipv4.TTL), // Preserve TTL
		}

		if ipv4.TOS != 0 {
			opts = append(opts, IPTOS(ipv4.TOS))
		}
		if ipv4.Id != 0 {
			opts = append(opts, IPId(ipv4.Id))
		}
		if ipv4.Flags != 0 {
			opts = append(opts, IPFlags(ipv4.Flags))
		}
		if ipv4.FragOffset != 0 {
		opts = append(opts, IPFragOffset(ipv4.FragOffset))
	}

	layerBuilders = append(layerBuilders, IPv4(opts...))
}

	// Process IPv6 layer
	if ipv6Layer := pkt.Layer(layers.LayerTypeIPv6); ipv6Layer != nil {
		ipv6 := ipv6Layer.(*layers.IPv6)

		// Apply IP address mapping
		srcIP := adaptIPAddress(ipv6.SrcIP.String())
		dstIP := adaptIPAddress(ipv6.DstIP.String())

		opts := []IPv6Option{
			IPv6Src(srcIP),
			IPv6Dst(dstIP),
			IPv6HopLimit(ipv6.HopLimit), // Preserve HopLimit
		}

		if ipv6.TrafficClass != 0 {
			opts = append(opts, IPv6TrafficClass(ipv6.TrafficClass))
		}
		if ipv6.FlowLabel != 0 {
			opts = append(opts, IPv6FlowLabel(ipv6.FlowLabel))
		}

		layerBuilders = append(layerBuilders, IPv6(opts...))
	}

	// Process TCP layer
	if tcpLayer := pkt.Layer(layers.LayerTypeTCP); tcpLayer != nil {
		tcp := tcpLayer.(*layers.TCP)

		opts := []TCPOption{
			TCPSport(uint16(tcp.SrcPort)),
			TCPDport(uint16(tcp.DstPort)),
		}

		if tcp.Seq != 0 {
			opts = append(opts, TCPSeq(tcp.Seq))
		}
		if tcp.Ack != 0 {
			opts = append(opts, TCPAck(tcp.Ack))
		}

		// Build flags string
		var flags string
		if tcp.FIN {
			flags += "F"
		}
		if tcp.SYN {
			flags += "S"
		}
		if tcp.RST {
			flags += "R"
		}
		if tcp.PSH {
			flags += "P"
		}
		if tcp.ACK {
			flags += "A"
		}
		if tcp.URG {
			flags += "U"
		}
		if len(flags) > 0 {
			opts = append(opts, TCPFlags(flags))
		}

		layerBuilders = append(layerBuilders, TCP(opts...))
	}

	// Process UDP layer
	if udpLayer := pkt.Layer(layers.LayerTypeUDP); udpLayer != nil {
		udp := udpLayer.(*layers.UDP)

		layerBuilders = append(layerBuilders, UDP(
			UDPSport(uint16(udp.SrcPort)),
			UDPDport(uint16(udp.DstPort)),
		))
	}

	// Process ICMPv4 layer
	if icmpLayer := pkt.Layer(layers.LayerTypeICMPv4); icmpLayer != nil {
		icmp := icmpLayer.(*layers.ICMPv4)

		opts := []ICMPOption{
			ICMPTypeCode(uint8(icmp.TypeCode>>8), uint8(icmp.TypeCode&0xff)),
		}

		if icmp.Id != 0 {
			opts = append(opts, ICMPId(icmp.Id))
		}
		if icmp.Seq != 0 {
			opts = append(opts, ICMPSeq(icmp.Seq))
		}

		layerBuilders = append(layerBuilders, ICMP(opts...))
	}

	// Process ICMPv6 layer
	if icmpv6Layer := pkt.Layer(layers.LayerTypeICMPv6); icmpv6Layer != nil {
		icmpv6 := icmpv6Layer.(*layers.ICMPv6)
		icmpType := uint8(icmpv6.TypeCode >> 8)

		switch icmpType {
		case 128: // Echo Request
			opts := []ICMPv6Option{}
			if echoLayer := pkt.Layer(layers.LayerTypeICMPv6Echo); echoLayer != nil {
				echo := echoLayer.(*layers.ICMPv6Echo)
				if echo.Identifier != 0 {
					opts = append(opts, ICMPv6Id(echo.Identifier))
				}
				if echo.SeqNumber != 0 {
					opts = append(opts, ICMPv6Seq(echo.SeqNumber))
				}
			}
			layerBuilders = append(layerBuilders, ICMPv6EchoRequest(opts...))

		case 129: // Echo Reply
			opts := []ICMPv6Option{}
			if echoLayer := pkt.Layer(layers.LayerTypeICMPv6Echo); echoLayer != nil {
				echo := echoLayer.(*layers.ICMPv6Echo)
				if echo.Identifier != 0 {
					opts = append(opts, ICMPv6Id(echo.Identifier))
				}
				if echo.SeqNumber != 0 {
					opts = append(opts, ICMPv6Seq(echo.SeqNumber))
				}
			}
			layerBuilders = append(layerBuilders, ICMPv6EchoReply(opts...))

		case 1: // Destination Unreachable
			code := uint8(icmpv6.TypeCode & 0xff)
			layerBuilders = append(layerBuilders, ICMPv6DestUnreach(
				ICMPv6Code(code),
			))

		default:
			// Generic ICMPv6
			layerBuilders = append(layerBuilders, ICMPv6EchoRequest())
		}
	}

	// Process IPv6 Fragment header
	if fragLayer := pkt.Layer(layers.LayerTypeIPv6Fragment); fragLayer != nil {
		frag := fragLayer.(*layers.IPv6Fragment)

		layerBuilders = append(layerBuilders, IPv6ExtHdrFragment(
			IPv6FragId(frag.Identification),
			IPv6FragOffset(frag.FragmentOffset),
			IPv6FragM(frag.MoreFragments),
		))
	}

	// Process payload
	if appLayer := pkt.ApplicationLayer(); appLayer != nil {
		payload := appLayer.Payload()
		if len(payload) > 0 {
			layerBuilders = append(layerBuilders, Raw(payload))
		}
	}

	// Rebuild packet using lib.NewPacket
	newPkt, err := NewPacket(nil, layerBuilders...)
	if err != nil {
		return nil, fmt.Errorf("failed to rebuild packet: %w", err)
	}

	return newPkt.Data(), nil
}

// adaptIPAddress applies yanet1→yanet2 IP address mappings.
// This function delegates to the unified AdaptIPAddress function.
func adaptIPAddress(ipAddr string) string {
	return AdaptIPAddress(ipAddr)
}
