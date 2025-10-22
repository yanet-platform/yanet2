package internal

import (
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// BuildPacketBytes builds serialized bytes for a ScapyPacketDef using gopacket.
// This is used by tests to validate that parsed Scapy packets can be reproduced.
func BuildPacketBytes(def ScapyPacketDef) ([]byte, error) {
	if def.IsFragmented || defRequiresRaw(def) {
		return nil, fmt.Errorf("requires raw handling")
	}

	var serialLayers []gopacket.SerializableLayer

	// We will adjust EthernetType and Dot1Q.Type after we know next layer
	var ethLayer *layers.Ethernet
	var dot1qLayer *layers.Dot1Q

	// Helper to append layer
	appendLayer := func(l gopacket.SerializableLayer) {
		serialLayers = append(serialLayers, l)
	}

	// Build each layer in order
	for idx, l := range def.Layers {
		switch l.Name {
		case "Ether":
			eth := &layers.Ethernet{}
			if v, ok := l.Params["src"]; ok && v != "" {
				mac, _ := net.ParseMAC(v)
				eth.SrcMAC = mac
			}
			if v, ok := l.Params["dst"]; ok && v != "" {
				mac, _ := net.ParseMAC(v)
				eth.DstMAC = mac
			}
			// Set default, updated later
			eth.EthernetType = layers.EthernetTypeIPv4
			ethLayer = eth
			appendLayer(eth)

		case "Dot1Q":
			d := &layers.Dot1Q{}
			if v, ok := l.Params["vlan"]; ok && v != "" {
				var vid uint16
				fmt.Sscanf(v, "%d", &vid)
				d.VLANIdentifier = vid
			}
			d.Type = layers.EthernetTypeIPv4
			dot1qLayer = d
			appendLayer(d)

		case "IP":
			ip := &layers.IPv4{Version: 4, IHL: 5}
			if v, ok := l.Params["src"]; ok && v != "" {
				ip.SrcIP = net.ParseIP(v)
			}
			if v, ok := l.Params["dst"]; ok && v != "" {
				ip.DstIP = net.ParseIP(v)
			}
			if v, ok := l.Params["ttl"]; ok && v != "" {
				var ttl uint8
				fmt.Sscanf(v, "%d", &ttl)
				ip.TTL = ttl
			} else {
				ip.TTL = 64
			}
			if v, ok := l.Params["tos"]; ok && v != "" {
				var tos uint8
				if _, err := fmt.Sscanf(v, "0x%x", &tos); err != nil {
					fmt.Sscanf(v, "%d", &tos)
				}
				ip.TOS = tos
			}
			if v, ok := l.Params["proto"]; ok && v != "" {
				var p uint8
				if _, err := fmt.Sscanf(v, "0x%x", &p); err != nil {
					fmt.Sscanf(v, "%d", &p)
				}
				ip.Protocol = layers.IPProtocol(p)
			} else {
				// Infer from next layer when possible
				if idx+1 < len(def.Layers) {
					switch def.Layers[idx+1].Name {
					case "TCP":
						ip.Protocol = layers.IPProtocolTCP
					case "UDP":
						ip.Protocol = layers.IPProtocolUDP
					case "ICMP":
						ip.Protocol = layers.IPProtocolICMPv4
					case "IP": // IPIP tunneling
						ip.Protocol = layers.IPProtocolIPv4
					case "IPv6": // IPv6 over IPv4
						ip.Protocol = layers.IPProtocolIPv6
					default:
						ip.Protocol = layers.IPProtocolTCP
					}
				}
			}
			appendLayer(ip)

		case "IPv6":
			ip6 := &layers.IPv6{Version: 6}
			if v, ok := l.Params["src"]; ok && v != "" {
				ip6.SrcIP = net.ParseIP(v)
			}
			if v, ok := l.Params["dst"]; ok && v != "" {
				ip6.DstIP = net.ParseIP(v)
			}
			if v, ok := l.Params["hlim"]; ok && v != "" {
				var hl uint8
				fmt.Sscanf(v, "%d", &hl)
				ip6.HopLimit = hl
			} else {
				ip6.HopLimit = 64
			}
			if v, ok := l.Params["tc"]; ok && v != "" {
				var tc uint8
				if _, err := fmt.Sscanf(v, "0x%x", &tc); err != nil {
					fmt.Sscanf(v, "%d", &tc)
				}
				ip6.TrafficClass = tc
			}
			if v, ok := l.Params["fl"]; ok && v != "" {
				var fl uint32
				if _, err := fmt.Sscanf(v, "0x%x", &fl); err != nil {
					fmt.Sscanf(v, "%d", &fl)
				}
				ip6.FlowLabel = fl
			}
			if v, ok := l.Params["nh"]; ok && v != "" {
				var p uint8
				if _, err := fmt.Sscanf(v, "0x%x", &p); err != nil {
					fmt.Sscanf(v, "%d", &p)
				}
				ip6.NextHeader = layers.IPProtocol(p)
			} else if idx+1 < len(def.Layers) {
				switch def.Layers[idx+1].Name {
				case "TCP":
					ip6.NextHeader = layers.IPProtocolTCP
				case "UDP":
					ip6.NextHeader = layers.IPProtocolUDP
				case "ICMPv6EchoRequest", "ICMPv6EchoReply":
					ip6.NextHeader = layers.IPProtocolICMPv6
				case "IPv6ExtHdrFragment":
					ip6.NextHeader = layers.IPProtocolIPv6Fragment
				case "IPv6ExtHdrDestOpt":
					ip6.NextHeader = layers.IPProtocolIPv6Destination
				case "IP": // IPv4 over IPv6
					ip6.NextHeader = layers.IPProtocolIPv4
				default:
					ip6.NextHeader = layers.IPProtocolTCP
				}
			}
			appendLayer(ip6)

		case "IPv6ExtHdrFragment":
			frag := &layers.IPv6Fragment{}
			if v, ok := l.Params["id"]; ok && v != "" {
				var id uint32
				if _, err := fmt.Sscanf(v, "0x%x", &id); err != nil {
					fmt.Sscanf(v, "%d", &id)
				}
				frag.Identification = id
			}
			if v, ok := l.Params["offset"]; ok && v != "" {
				var off uint16
				fmt.Sscanf(v, "%d", &off)
				frag.FragmentOffset = off
			}
			if v, ok := l.Params["m"]; ok && v == "0" {
				frag.MoreFragments = false
			} else {
				frag.MoreFragments = true
			}
			// NextHeader will be corrected based on following layer
			appendLayer(frag)

		case "IPv6ExtHdrDestOpt":
			dest := &layers.IPv6Destination{}
			appendLayer(dest)

		case "TCP":
			tcp := &layers.TCP{}
			if v, ok := l.Params["sport"]; ok && v != "" {
				var p uint16
				fmt.Sscanf(v, "%d", &p)
				tcp.SrcPort = layers.TCPPort(p)
			}
			if v, ok := l.Params["dport"]; ok && v != "" {
				var p uint16
				fmt.Sscanf(v, "%d", &p)
				tcp.DstPort = layers.TCPPort(p)
			}
			// Set checksum network layer
			for i := len(serialLayers) - 1; i >= 0; i-- {
				if nl, ok := serialLayers[i].(gopacket.NetworkLayer); ok {
					tcp.SetNetworkLayerForChecksum(nl)
					break
				}
			}
			appendLayer(tcp)

		case "UDP":
			udp := &layers.UDP{}
			if v, ok := l.Params["sport"]; ok && v != "" {
				var p uint16
				fmt.Sscanf(v, "%d", &p)
				udp.SrcPort = layers.UDPPort(p)
			}
			if v, ok := l.Params["dport"]; ok && v != "" {
				var p uint16
				fmt.Sscanf(v, "%d", &p)
				udp.DstPort = layers.UDPPort(p)
			}
			for i := len(serialLayers) - 1; i >= 0; i-- {
				if nl, ok := serialLayers[i].(gopacket.NetworkLayer); ok {
					udp.SetNetworkLayerForChecksum(nl)
					break
				}
			}
			appendLayer(udp)

		case "ICMPv6EchoRequest":
			ic6 := &layers.ICMPv6{TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0)}
			for i := len(serialLayers) - 1; i >= 0; i-- {
				if nl, ok := serialLayers[i].(gopacket.NetworkLayer); ok {
					ic6.SetNetworkLayerForChecksum(nl)
					break
				}
			}
			appendLayer(ic6)
			echo := &layers.ICMPv6Echo{}
			if v, ok := l.Params["id"]; ok && v != "" {
				var id uint16
				if _, err := fmt.Sscanf(v, "0x%x", &id); err != nil {
					fmt.Sscanf(v, "%d", &id)
				}
				echo.Identifier = id
			}
			if v, ok := l.Params["seq"]; ok && v != "" {
				var sq uint16
				if _, err := fmt.Sscanf(v, "0x%x", &sq); err != nil {
					fmt.Sscanf(v, "%d", &sq)
				}
				echo.SeqNumber = sq
			}
			appendLayer(echo)

		case "ICMPv6EchoReply":
			ic6 := &layers.ICMPv6{TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoReply, 0)}
			for i := len(serialLayers) - 1; i >= 0; i-- {
				if nl, ok := serialLayers[i].(gopacket.NetworkLayer); ok {
					ic6.SetNetworkLayerForChecksum(nl)
					break
				}
			}
			appendLayer(ic6)
			appendLayer(&layers.ICMPv6Echo{})

		case "ICMP":
			ic := &layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}
			appendLayer(ic)
		}
	}

	// Fix L2 types based on following layer positions
	if ethLayer != nil {
		// Default: set based on the first network layer after Ethernet or Dot1Q
		nextIdx := 1
		if dot1qLayer != nil {
			// Next after Dot1Q
			nextIdx = 2
		}
		if nextIdx < len(serialLayers) {
			switch serialLayers[nextIdx].(type) {
			case *layers.IPv4:
				if dot1qLayer != nil {
					ethLayer.EthernetType = layers.EthernetTypeDot1Q
					dot1qLayer.Type = layers.EthernetTypeIPv4
				} else {
					ethLayer.EthernetType = layers.EthernetTypeIPv4
				}
			case *layers.IPv6:
				if dot1qLayer != nil {
					ethLayer.EthernetType = layers.EthernetTypeDot1Q
					dot1qLayer.Type = layers.EthernetTypeIPv6
				} else {
					ethLayer.EthernetType = layers.EthernetTypeIPv6
				}
			}
		}
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
		// Try to avoid padding by setting minimum frame size to 0
		// Note: gopacket may still add padding for very small frames
	}
	if err := gopacket.SerializeLayers(buf, opts, serialLayers...); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func defRequiresRaw(def ScapyPacketDef) bool {
	if def.IsFragmented {
		return true
	}
	for _, l := range def.Layers {
		// Extension headers require raw handling
		if l.Name == "IPv6ExtHdrDestOpt" || l.Name == "IPv6ExtHdrRouting" ||
			l.Name == "IPv6ExtHdrFragment" {
			return true
		}
		// Malformed packet indicators
		if _, ok := l.Params["options"]; ok {
			return true
		}
		if _, ok := l.Params["len"]; ok {
			return true
		}
		if _, ok := l.Params["plen"]; ok {
			return true // IPv6 payload length override
		}
		// Special protocols
		if _, ok := l.Params["nh"]; ok {
			if val := l.Params["nh"]; val == "0x1B" || val == "27" {
				return true // RUDP protocol
			}
		}
		if _, ok := l.Params["proto"]; ok {
			if val := l.Params["proto"]; val == "0x1B" || val == "27" {
				return true // RUDP protocol
			}
		}
	}
	return false
}
