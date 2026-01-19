package utils

import (
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// MakeTCPPacket creates a TCP packet with the specified parameters.
// Supports both IPv4 and IPv6.
func MakeTCPPacket(
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
	tcp *layers.TCP,
) []gopacket.SerializableLayer {
	// Ensure both addresses are the same IP version
	if srcIP.Is4() != dstIP.Is4() {
		panic(fmt.Sprintf("IP version mismatch: src=%v dst=%v", srcIP, dstIP))
	}

	src := net.IP(srcIP.AsSlice())
	dst := net.IP(dstIP.AsSlice())

	var ip gopacket.NetworkLayer
	ethernetType := layers.EthernetTypeIPv6
	if srcIP.Is4() {
		ethernetType = layers.EthernetTypeIPv4
		ip = &layers.IPv4{
			Version:  4,
			IHL:      5,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    src,
			DstIP:    dst,
			TOS:      214,
		}
	} else {
		ip = &layers.IPv6{
			Version:      6,
			NextHeader:   layers.IPProtocolTCP,
			HopLimit:     64,
			SrcIP:        src,
			DstIP:        dst,
			TrafficClass: 139,
		}
	}

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		EthernetType: ethernetType,
	}

	tcp.SrcPort = layers.TCPPort(srcPort)
	tcp.DstPort = layers.TCPPort(dstPort)
	tcp.SetNetworkLayerForChecksum(ip)

	payload := []byte("BALANCER TEST PAYLOAD 12345678910")
	packetLayers := []gopacket.SerializableLayer{
		eth,
		ip.(gopacket.SerializableLayer),
		tcp,
		gopacket.Payload(payload),
	}

	return packetLayers
}

// MakeUDPPacket creates a UDP packet with the specified parameters.
// Supports both IPv4 and IPv6.
func MakeUDPPacket(
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
) []gopacket.SerializableLayer {
	// Ensure both addresses are the same IP version
	if srcIP.Is4() != dstIP.Is4() {
		panic(fmt.Sprintf("IP version mismatch: src=%v dst=%v", srcIP, dstIP))
	}

	src := net.IP(srcIP.AsSlice())
	dst := net.IP(dstIP.AsSlice())

	var ip gopacket.NetworkLayer
	ethernetType := layers.EthernetTypeIPv6
	if srcIP.Is4() {
		ethernetType = layers.EthernetTypeIPv4
		ip = &layers.IPv4{
			Version:  4,
			IHL:      5,
			TTL:      64,
			Protocol: layers.IPProtocolUDP,
			SrcIP:    src,
			DstIP:    dst,
			TOS:      123,
		}
	} else {
		ip = &layers.IPv6{
			Version:      6,
			NextHeader:   layers.IPProtocolUDP,
			HopLimit:     64,
			SrcIP:        src,
			DstIP:        dst,
			TrafficClass: 212,
		}
	}

	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		DstMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		EthernetType: ethernetType,
	}

	udp := &layers.UDP{
		SrcPort: layers.UDPPort(srcPort),
		DstPort: layers.UDPPort(dstPort),
	}
	udp.SetNetworkLayerForChecksum(ip)

	payload := []byte("PING TEST PAYLOAD 1234567890")
	packetLayers := []gopacket.SerializableLayer{
		eth,
		ip.(gopacket.SerializableLayer),
		udp,
		gopacket.Payload(payload),
	}

	return packetLayers
}

// MakePacketLayers creates packet layers based on whether TCP or UDP is specified.
// If tcp is nil, creates a UDP packet; otherwise creates a TCP packet.
func MakePacketLayers(
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
	tcp *layers.TCP,
) []gopacket.SerializableLayer {
	if tcp == nil {
		return MakeUDPPacket(srcIP, srcPort, dstIP, dstPort)
	}
	return MakeTCPPacket(srcIP, srcPort, dstIP, dstPort, tcp)
}

// padTCPOptions pads TCP options to 4-byte boundary with NOPs
func padTCPOptions(opts []layers.TCPOption) ([]layers.TCPOption, error) {
	// Compute current options length (bytes)
	length := 0
	for _, o := range opts {
		switch o.OptionType {
		case layers.TCPOptionKindEndList, layers.TCPOptionKindNop:
			length += 1
		default:
			if o.OptionLength == 0 {
				return nil, errors.New("TCP option with zero length")
			}
			length += int(o.OptionLength)
		}
	}
	if length > 40 {
		return nil, fmt.Errorf("TCP options exceed 40 bytes (%d)", length)
	}
	// Pad with NOPs to 4-byte boundary
	for (length % 4) != 0 {
		opts = append(
			opts,
			layers.TCPOption{OptionType: layers.TCPOptionKindNop},
		)
		length++
	}
	return opts, nil
}

// InsertOrUpdateMSS inserts or updates the MSS option in a TCP packet
func InsertOrUpdateMSS(
	p gopacket.Packet,
	newMSS uint16,
) (*gopacket.Packet, error) {
	tcpL := p.Layer(layers.LayerTypeTCP)
	if tcpL == nil {
		return nil, errors.New("no TCP layer")
	}
	ip4L := p.Layer(layers.LayerTypeIPv4)
	ip6L := p.Layer(layers.LayerTypeIPv6)
	if ip4L == nil && ip6L == nil {
		return nil, errors.New("no IPv4/IPv6 layer")
	}

	tcp := *tcpL.(*layers.TCP)
	if !tcp.SYN {
		return nil, errors.New("MSS option is only valid on SYN/SYN-ACK")
	}

	// Update existing MSS or insert a new one
	found := false
	for i, o := range tcp.Options {
		if o.OptionType == layers.TCPOptionKindMSS && o.OptionLength >= 4 {
			tcp.Options[i].OptionData = []byte{byte(newMSS >> 8), byte(newMSS)}
			found = true
			break
		}
	}
	if !found {
		mssOpt := layers.TCPOption{
			OptionType:   layers.TCPOptionKindMSS,
			OptionLength: 4,
			OptionData:   []byte{byte(newMSS >> 8), byte(newMSS)},
		}
		// Conventionally MSS is first
		tcp.Options = append([]layers.TCPOption{mssOpt}, tcp.Options...)
	}

	// Pad options and check size
	var err error
	tcp.Options, err = padTCPOptions(tcp.Options)
	if err != nil {
		return nil, err
	}

	var serLayers []gopacket.SerializableLayer

	var netBeforeTCP gopacket.NetworkLayer

	for _, l := range p.Layers() {
		if l.LayerType() == layers.LayerTypeTCP {
			break
		}
		if nl, ok := l.(gopacket.NetworkLayer); ok {
			netBeforeTCP = nl
		}
		if sl, ok := l.(gopacket.SerializableLayer); ok {
			// Make a value-copy for common layers to avoid mutating the original packet
			switch v := l.(type) {
			case *layers.Ethernet:
				c := *v
				serLayers = append(serLayers, &c)
			case *layers.Dot1Q:
				c := *v
				serLayers = append(serLayers, &c)
			case *layers.IPv4:
				c := *v
				serLayers = append(serLayers, &c)
			case *layers.IPv6:
				c := *v
				serLayers = append(serLayers, &c)
			case *layers.IPv6HopByHop:
				c := *v
				serLayers = append(serLayers, &c)
			case *layers.IPv6Fragment:
				c := *v
				serLayers = append(serLayers, &c)
			case *layers.UDP:
				c := *v
				serLayers = append(serLayers, &c)
			default:
				// Fallback: use as-is (most gopacket layers are already SerializableLayer)
				serLayers = append(serLayers, sl)
			}
		}
	}

	tcp.SetNetworkLayerForChecksum(netBeforeTCP)
	serLayers = append(serLayers, &tcp)
	serLayers = append(serLayers, gopacket.Payload(tcp.Payload))

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	if err := gopacket.SerializeLayers(buf, opts, serLayers...); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	p2 := gopacket.NewPacket(out, layers.LayerTypeEthernet, gopacket.Default)
	return &p2, nil
}
