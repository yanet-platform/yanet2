package framework

import (
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// TCPPacketOpts configures optional fields for CreateTCPIPv4Packet and
// CreateTCPIPv6Packet. Zero values use sensible defaults matching the
// most common test pattern.
type TCPPacketOpts struct {
	SrcMAC  string // default: framework.SrcMAC
	DstMAC  string // default: framework.DstMAC
	SrcPort uint16 // default: 12345
	DstPort uint16 // default: 80
	SYN     bool   // default: false
	ACK     bool   // default: true
	PSH     bool   // default: true
	Seq     uint32 // default: 1
	Ack     uint32 // default: 1
	Window  uint16 // default: 1024
}

// resolveTCPOpts fills in defaults for unset fields.
func resolveTCPOpts(opts *TCPPacketOpts) *TCPPacketOpts {
	if opts == nil {
		opts = &TCPPacketOpts{}
	}
	if opts.SrcMAC == "" {
		opts.SrcMAC = SrcMAC
	}
	if opts.DstMAC == "" {
		opts.DstMAC = DstMAC
	}
	if opts.SrcPort == 0 {
		opts.SrcPort = 12345
	}
	if opts.DstPort == 0 {
		opts.DstPort = 80
	}
	if opts.Seq == 0 && !opts.SYN {
		opts.Seq = 1
	}
	if opts.Ack == 0 && !opts.SYN {
		opts.Ack = 1
	}
	if opts.Window == 0 && !opts.SYN {
		opts.Window = 1024
	}
	// For ACK and PSH: if SYN is set, treat zero-value as intentionally
	// disabled. Otherwise default to true (matching the common test pattern).
	if !opts.SYN && !opts.ACK && !opts.PSH {
		opts.ACK = true
		opts.PSH = true
	}
	return opts
}

// buildTCPLayer constructs the TCP layer from resolved opts.
func buildTCPLayer(opts *TCPPacketOpts) layers.TCP {
	return layers.TCP{
		SrcPort: layers.TCPPort(opts.SrcPort),
		DstPort: layers.TCPPort(opts.DstPort),
		Seq:     opts.Seq,
		Ack:     opts.Ack,
		Window:  opts.Window,
		SYN:     opts.SYN,
		ACK:     opts.ACK,
		PSH:     opts.PSH,
	}
}

// CreateTCPIPv4Packet builds a serialized Ethernet/IPv4/TCP packet.
// Pass nil for opts to use defaults (SrcMAC/DstMAC from framework
// constants, SrcPort 12345, DstPort 80, PSH+ACK flags).
func CreateTCPIPv4Packet(srcIP, dstIP net.IP, payload []byte, opts *TCPPacketOpts) []byte {
	opts = resolveTCPOpts(opts)

	eth := layers.Ethernet{
		SrcMAC:       MustParseMAC(opts.SrcMAC),
		DstMAC:       MustParseMAC(opts.DstMAC),
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip4 := layers.IPv4{
		Version:  4,
		IHL:      5,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}

	tcp := buildTCPLayer(opts)
	err := tcp.SetNetworkLayerForChecksum(&ip4)
	if err != nil {
		panic(err)
	}

	buf := gopacket.NewSerializeBuffer()
	serOpts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err = gopacket.SerializeLayers(buf, serOpts, &eth, &ip4, &tcp, gopacket.Payload(payload))
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// CreateTCPIPv6Packet builds a serialized Ethernet/IPv6/TCP packet.
// Pass nil for opts to use defaults (same as CreateTCPIPv4Packet).
func CreateTCPIPv6Packet(srcIP, dstIP net.IP, payload []byte, opts *TCPPacketOpts) []byte {
	opts = resolveTCPOpts(opts)

	eth := layers.Ethernet{
		SrcMAC:       MustParseMAC(opts.SrcMAC),
		DstMAC:       MustParseMAC(opts.DstMAC),
		EthernetType: layers.EthernetTypeIPv6,
	}

	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolTCP,
		SrcIP:      srcIP,
		DstIP:      dstIP,
	}

	tcp := buildTCPLayer(opts)
	err := tcp.SetNetworkLayerForChecksum(&ip6)
	if err != nil {
		panic(err)
	}

	buf := gopacket.NewSerializeBuffer()
	serOpts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err = gopacket.SerializeLayers(buf, serOpts, &eth, &ip6, &tcp, gopacket.Payload(payload))
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// CreateICMPv4EchoPacket builds a serialized Ethernet/IPv4/ICMP Echo Request.
func CreateICMPv4EchoPacket(srcIP, dstIP net.IP, id, seq uint16, payload []byte) []byte {
	eth := layers.Ethernet{
		SrcMAC:       MustParseMAC(SrcMAC),
		DstMAC:       MustParseMAC(DstMAC),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
		Id:       id,
		Seq:      seq,
	}
	buf := gopacket.NewSerializeBuffer()
	serOpts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	if err := gopacket.SerializeLayers(buf, serOpts, &eth, &ip4, &icmp, gopacket.Payload(payload)); err != nil {
		panic(fmt.Sprintf("CreateICMPv4EchoPacket: serialize failed: %v", err))
	}
	return buf.Bytes()
}
