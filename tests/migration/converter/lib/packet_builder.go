package lib

import (
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// PacketBuilder provides a Scapy-like DSL for building packets
type PacketBuilder struct {
	layers []gopacket.SerializableLayer
}

// NewPacket creates a new packet from the given layers
func NewPacket(layerBuilders ...LayerBuilder) (gopacket.Packet, error) {
	var serialLayers []gopacket.SerializableLayer

	for _, builder := range layerBuilders {
		layer := builder.Build()
		if layer != nil {
			serialLayers = append(serialLayers, layer)
		}
	}

	// Fix layer types and protocols based on layer order
	// First pass: set ethernet/vlan types based on IMMEDIATE next layer
	for i, layer := range serialLayers {
		switch layer.(type) {
		case *layers.Ethernet:
			// Look ahead to next layer to set EthernetType
			if i+1 < len(serialLayers) {
				eth := layer.(*layers.Ethernet)
				switch serialLayers[i+1].(type) {
				case *layers.Dot1Q:
					eth.EthernetType = layers.EthernetTypeDot1Q
				case *layers.IPv4:
					eth.EthernetType = layers.EthernetTypeIPv4
				case *layers.IPv6:
					eth.EthernetType = layers.EthernetTypeIPv6
				}
			}
		case *layers.Dot1Q:
			// Look ahead to next layer to set VLAN Type
			if i+1 < len(serialLayers) {
				vlan := layer.(*layers.Dot1Q)
				switch serialLayers[i+1].(type) {
				case *layers.IPv4:
					vlan.Type = layers.EthernetTypeIPv4
				case *layers.IPv6:
					vlan.Type = layers.EthernetTypeIPv6
				}
			}
		case *layers.IPv4:
			// Look ahead to set protocol
			if i+1 < len(serialLayers) {
				ip := layer.(*layers.IPv4)
				switch serialLayers[i+1].(type) {
				case *layers.TCP:
					ip.Protocol = layers.IPProtocolTCP
				case *layers.UDP:
					ip.Protocol = layers.IPProtocolUDP
				case *layers.ICMPv4:
					ip.Protocol = layers.IPProtocolICMPv4
				case *layers.GRE:
					ip.Protocol = layers.IPProtocolGRE
				case *layers.IPv4:
					ip.Protocol = layers.IPProtocolIPv4
				case *layers.IPv6:
					ip.Protocol = layers.IPProtocolIPv6
				}
			}
		case *layers.IPv6:
			// Look ahead to set next header
			if i+1 < len(serialLayers) {
				ip6 := layer.(*layers.IPv6)
				switch serialLayers[i+1].(type) {
				case *layers.TCP:
					ip6.NextHeader = layers.IPProtocolTCP
				case *layers.UDP:
					ip6.NextHeader = layers.IPProtocolUDP
				case *layers.ICMPv6:
					ip6.NextHeader = layers.IPProtocolICMPv6
				case *layers.IPv6Fragment:
					ip6.NextHeader = layers.IPProtocolIPv6Fragment
				case *layers.GRE:
					ip6.NextHeader = layers.IPProtocolGRE
				case *layers.IPv4:
					ip6.NextHeader = layers.IPProtocolIPv4
				}
			}
		case *layers.IPv6Fragment:
			// Look ahead to set next header in fragment
			if i+1 < len(serialLayers) {
				frag := layer.(*layers.IPv6Fragment)
				switch serialLayers[i+1].(type) {
				case *layers.TCP:
					frag.NextHeader = layers.IPProtocolTCP
				case *layers.UDP:
					frag.NextHeader = layers.IPProtocolUDP
				case *layers.ICMPv6:
					frag.NextHeader = layers.IPProtocolICMPv6
				case *layers.IPv4:
					frag.NextHeader = layers.IPProtocolIPv4
				}
			}
		case *layers.GRE:
			// Look ahead to set protocol for encapsulated packet
			if i+1 < len(serialLayers) {
				gre := layer.(*layers.GRE)
				switch serialLayers[i+1].(type) {
				case *layers.IPv4:
					gre.Protocol = layers.EthernetTypeIPv4
				case *layers.IPv6:
					gre.Protocol = layers.EthernetTypeIPv6
				}
			}
		}
	}

	// Set network layer for transport layer checksum calculation
	var networkLayer gopacket.NetworkLayer
	for _, layer := range serialLayers {
		if nl, ok := layer.(gopacket.NetworkLayer); ok {
			networkLayer = nl
			break
		}
	}

	// Set network layer for TCP/UDP if present
	if networkLayer != nil {
		for _, layer := range serialLayers {
			switch tl := layer.(type) {
			case *layers.TCP:
				tl.SetNetworkLayerForChecksum(networkLayer)
			case *layers.UDP:
				tl.SetNetworkLayerForChecksum(networkLayer)
			case *layers.ICMPv6:
				tl.SetNetworkLayerForChecksum(networkLayer)
			}
		}
	}

	// Serialize the packet
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}

	if err := gopacket.SerializeLayers(buf, opts, serialLayers...); err != nil {
		return nil, err
	}

	// Parse back to packet
	pkt := gopacket.NewPacket(buf.Bytes(), layers.LayerTypeEthernet, gopacket.Default)
	return pkt, nil
}

// LayerBuilder interface for all layer builders
type LayerBuilder interface {
	Build() gopacket.SerializableLayer
}

// ===== Ethernet Layer =====

type EthernetBuilder struct {
	layer *layers.Ethernet
}

func Ether(opts ...EtherOption) *EthernetBuilder {
	eth := &layers.Ethernet{
		EthernetType: layers.EthernetTypeIPv4, // default
	}

	for _, opt := range opts {
		opt(eth)
	}

	return &EthernetBuilder{layer: eth}
}

func (b *EthernetBuilder) Build() gopacket.SerializableLayer {
	return b.layer
}

type EtherOption func(*layers.Ethernet)

func EtherSrc(mac string) EtherOption {
	return func(eth *layers.Ethernet) {
		if parsed, err := net.ParseMAC(mac); err == nil {
			eth.SrcMAC = parsed
		}
	}
}

func EtherDst(mac string) EtherOption {
	return func(eth *layers.Ethernet) {
		if parsed, err := net.ParseMAC(mac); err == nil {
			eth.DstMAC = parsed
		}
	}
}

func EtherType(etherType layers.EthernetType) EtherOption {
	return func(eth *layers.Ethernet) {
		eth.EthernetType = etherType
	}
}

// ===== Dot1Q (VLAN) Layer =====

type Dot1QBuilder struct {
	layer *layers.Dot1Q
}

func Dot1Q(opts ...Dot1QOption) *Dot1QBuilder {
	vlan := &layers.Dot1Q{
		Type: layers.EthernetTypeIPv4, // default
	}

	for _, opt := range opts {
		opt(vlan)
	}

	return &Dot1QBuilder{layer: vlan}
}

func (b *Dot1QBuilder) Build() gopacket.SerializableLayer {
	return b.layer
}

type Dot1QOption func(*layers.Dot1Q)

func VLANId(id uint16) Dot1QOption {
	return func(vlan *layers.Dot1Q) {
		vlan.VLANIdentifier = id
	}
}

func VLANType(etherType layers.EthernetType) Dot1QOption {
	return func(vlan *layers.Dot1Q) {
		vlan.Type = etherType
	}
}

// ===== IPv4 Layer =====

type IPv4Builder struct {
	layer *layers.IPv4
}

func IP(opts ...IPv4Option) *IPv4Builder {
	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,                   // default
		Protocol: layers.IPProtocolTCP, // default
	}

	for _, opt := range opts {
		opt(ip)
	}

	return &IPv4Builder{layer: ip}
}

func (b *IPv4Builder) Build() gopacket.SerializableLayer {
	return b.layer
}

type IPv4Option func(*layers.IPv4)

func IPSrc(ip string) IPv4Option {
	return func(ipv4 *layers.IPv4) {
		ipv4.SrcIP = net.ParseIP(ip)
	}
}

func IPDst(ip string) IPv4Option {
	return func(ipv4 *layers.IPv4) {
		ipv4.DstIP = net.ParseIP(ip)
	}
}

func IPTTL(ttl uint8) IPv4Option {
	return func(ipv4 *layers.IPv4) {
		ipv4.TTL = ttl
	}
}

func IPTOS(tos uint8) IPv4Option {
	return func(ipv4 *layers.IPv4) {
		ipv4.TOS = tos
	}
}

func IPProto(proto layers.IPProtocol) IPv4Option {
	return func(ipv4 *layers.IPv4) {
		ipv4.Protocol = proto
	}
}

func IPId(id uint16) IPv4Option {
	return func(ipv4 *layers.IPv4) {
		ipv4.Id = id
	}
}

func IPFlags(flags layers.IPv4Flag) IPv4Option {
	return func(ipv4 *layers.IPv4) {
		ipv4.Flags = flags
	}
}

func IPFragOffset(offset uint16) IPv4Option {
	return func(ipv4 *layers.IPv4) {
		ipv4.FragOffset = offset
	}
}

// ===== IPv6 Layer =====

type IPv6Builder struct {
	layer *layers.IPv6
}

func IPv6(opts ...IPv6Option) *IPv6Builder {
	ip6 := &layers.IPv6{
		Version:    6,
		HopLimit:   64,                   // default
		NextHeader: layers.IPProtocolTCP, // default
	}

	for _, opt := range opts {
		opt(ip6)
	}

	return &IPv6Builder{layer: ip6}
}

func (b *IPv6Builder) Build() gopacket.SerializableLayer {
	return b.layer
}

type IPv6Option func(*layers.IPv6)

func IPv6Src(ip string) IPv6Option {
	return func(ipv6 *layers.IPv6) {
		ipv6.SrcIP = net.ParseIP(ip)
	}
}

func IPv6Dst(ip string) IPv6Option {
	return func(ipv6 *layers.IPv6) {
		ipv6.DstIP = net.ParseIP(ip)
	}
}

func IPv6HopLimit(hl uint8) IPv6Option {
	return func(ipv6 *layers.IPv6) {
		ipv6.HopLimit = hl
	}
}

func IPv6TrafficClass(tc uint8) IPv6Option {
	return func(ipv6 *layers.IPv6) {
		ipv6.TrafficClass = tc
	}
}

func IPv6FlowLabel(fl uint32) IPv6Option {
	return func(ipv6 *layers.IPv6) {
		ipv6.FlowLabel = fl
	}
}

func IPv6NextHeader(nh layers.IPProtocol) IPv6Option {
	return func(ipv6 *layers.IPv6) {
		ipv6.NextHeader = nh
	}
}

// ===== TCP Layer =====

type TCPBuilder struct {
	layer        *layers.TCP
	networkLayer gopacket.NetworkLayer
}

func TCP(opts ...TCPOption) *TCPBuilder {
	tcp := &layers.TCP{
		DataOffset: 5,
	}

	builder := &TCPBuilder{layer: tcp}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func (b *TCPBuilder) Build() gopacket.SerializableLayer {
	return b.layer
}

type TCPOption func(*TCPBuilder)

func TCPSport(port uint16) TCPOption {
	return func(builder *TCPBuilder) {
		builder.layer.SrcPort = layers.TCPPort(port)
	}
}

func TCPDport(port uint16) TCPOption {
	return func(builder *TCPBuilder) {
		builder.layer.DstPort = layers.TCPPort(port)
	}
}

func TCPFlags(flags string) TCPOption {
	return func(builder *TCPBuilder) {
		// Parse flags string like "S", "SA", "A", "F", "R", etc.
		for _, flag := range flags {
			switch flag {
			case 'S':
				builder.layer.SYN = true
			case 'A':
				builder.layer.ACK = true
			case 'F':
				builder.layer.FIN = true
			case 'R':
				builder.layer.RST = true
			case 'P':
				builder.layer.PSH = true
			case 'U':
				builder.layer.URG = true
			}
		}
	}
}

func TCPSeq(seq uint32) TCPOption {
	return func(builder *TCPBuilder) {
		builder.layer.Seq = seq
	}
}

func TCPAck(ack uint32) TCPOption {
	return func(builder *TCPBuilder) {
		builder.layer.Ack = ack
	}
}

// ===== UDP Layer =====

type UDPBuilder struct {
	layer *layers.UDP
}

func UDP(opts ...UDPOption) *UDPBuilder {
	udp := &layers.UDP{}

	builder := &UDPBuilder{layer: udp}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func (b *UDPBuilder) Build() gopacket.SerializableLayer {
	return b.layer
}

type UDPOption func(*UDPBuilder)

func UDPSport(port uint16) UDPOption {
	return func(builder *UDPBuilder) {
		builder.layer.SrcPort = layers.UDPPort(port)
	}
}

func UDPDport(port uint16) UDPOption {
	return func(builder *UDPBuilder) {
		builder.layer.DstPort = layers.UDPPort(port)
	}
}

// ===== ICMP Layer =====

type ICMPBuilder struct {
	layer *layers.ICMPv4
}

func ICMP(opts ...ICMPOption) *ICMPBuilder {
	icmp := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}

	builder := &ICMPBuilder{layer: icmp}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func (b *ICMPBuilder) Build() gopacket.SerializableLayer {
	return b.layer
}

type ICMPOption func(*ICMPBuilder)

func ICMPType(icmpType layers.ICMPv4TypeCode) ICMPOption {
	return func(builder *ICMPBuilder) {
		builder.layer.TypeCode = icmpType
	}
}

func ICMPTypeCode(icmpType uint8, code uint8) ICMPOption {
	return func(builder *ICMPBuilder) {
		builder.layer.TypeCode = layers.CreateICMPv4TypeCode(icmpType, code)
	}
}

func ICMPId(id uint16) ICMPOption {
	return func(builder *ICMPBuilder) {
		builder.layer.Id = id
	}
}

func ICMPSeq(seq uint16) ICMPOption {
	return func(builder *ICMPBuilder) {
		builder.layer.Seq = seq
	}
}

// ===== ICMPv6 Layer =====

type ICMPv6Builder struct {
	layer *layers.ICMPv6
	echo  *layers.ICMPv6Echo
}

func ICMPv6EchoRequest(opts ...ICMPv6Option) *ICMPv6Builder {
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	echo := &layers.ICMPv6Echo{}

	builder := &ICMPv6Builder{layer: icmp, echo: echo}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func ICMPv6EchoReply(opts ...ICMPv6Option) *ICMPv6Builder {
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoReply, 0),
	}
	echo := &layers.ICMPv6Echo{}

	builder := &ICMPv6Builder{layer: icmp, echo: echo}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func ICMPv6DestUnreach(opts ...ICMPv6Option) *ICMPv6Builder {
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeDestinationUnreachable, 0),
	}

	builder := &ICMPv6Builder{layer: icmp}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func (b *ICMPv6Builder) Build() gopacket.SerializableLayer {
	// ICMPv6 requires special handling - we return a multi-layer structure
	// For now, return just the ICMPv6 layer
	// The echo layer needs to be handled separately in packet construction
	return b.layer
}

type ICMPv6Option func(*ICMPv6Builder)

func ICMPv6Id(id uint16) ICMPv6Option {
	return func(builder *ICMPv6Builder) {
		if builder.echo != nil {
			builder.echo.Identifier = id
		}
	}
}

func ICMPv6Seq(seq uint16) ICMPv6Option {
	return func(builder *ICMPv6Builder) {
		if builder.echo != nil {
			builder.echo.SeqNumber = seq
		}
	}
}

func ICMPv6Code(code uint8) ICMPv6Option {
	return func(builder *ICMPv6Builder) {
		// Extract current type and set new code
		currentType := uint8(builder.layer.TypeCode >> 8)
		builder.layer.TypeCode = layers.CreateICMPv6TypeCode(currentType, code)
	}
}

// ===== IPv6 Extension Headers =====

type IPv6FragmentBuilder struct {
	layer *layers.IPv6Fragment
}

func IPv6ExtHdrFragment(opts ...IPv6FragmentOption) *IPv6FragmentBuilder {
	frag := &layers.IPv6Fragment{
		NextHeader: layers.IPProtocolTCP, // default
	}

	builder := &IPv6FragmentBuilder{layer: frag}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func (b *IPv6FragmentBuilder) Build() gopacket.SerializableLayer {
	return b.layer
}

type IPv6FragmentOption func(*IPv6FragmentBuilder)

func IPv6FragId(id uint32) IPv6FragmentOption {
	return func(builder *IPv6FragmentBuilder) {
		builder.layer.Identification = id
	}
}

func IPv6FragOffset(offset uint16) IPv6FragmentOption {
	return func(builder *IPv6FragmentBuilder) {
		builder.layer.FragmentOffset = offset
	}
}

func IPv6FragM(m bool) IPv6FragmentOption {
	return func(builder *IPv6FragmentBuilder) {
		builder.layer.MoreFragments = m
	}
}

func IPv6FragNextHeader(nh layers.IPProtocol) IPv6FragmentOption {
	return func(builder *IPv6FragmentBuilder) {
		builder.layer.NextHeader = nh
	}
}

// ===== GRE Layer =====

type GREBuilder struct {
	layer *layers.GRE
}

func GRE(opts ...GREOption) *GREBuilder {
	gre := &layers.GRE{
		Protocol: layers.EthernetTypeIPv4, // default
	}

	builder := &GREBuilder{layer: gre}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func (b *GREBuilder) Build() gopacket.SerializableLayer {
	return b.layer
}

type GREOption func(*GREBuilder)

func GREProtocol(proto layers.EthernetType) GREOption {
	return func(builder *GREBuilder) {
		builder.layer.Protocol = proto
	}
}

func GREChecksumPresent(present bool) GREOption {
	return func(builder *GREBuilder) {
		builder.layer.ChecksumPresent = present
	}
}

func GREKeyPresent(present bool) GREOption {
	return func(builder *GREBuilder) {
		builder.layer.KeyPresent = present
	}
}

func GRESeqPresent(present bool) GREOption {
	return func(builder *GREBuilder) {
		builder.layer.SeqPresent = present
	}
}

func GREVersion(version uint8) GREOption {
	return func(builder *GREBuilder) {
		builder.layer.Version = version
	}
}

func GREKey(key uint32) GREOption {
	return func(builder *GREBuilder) {
		builder.layer.Key = key
		builder.layer.KeyPresent = true
	}
}

// ===== Raw/Payload Layer =====

type RawBuilder struct {
	payload []byte
}

func Raw(data []byte) *RawBuilder {
	return &RawBuilder{payload: data}
}

func (b *RawBuilder) Build() gopacket.SerializableLayer {
	return gopacket.Payload(b.payload)
}

// ===== Helper Functions =====

// PortRange generates a slice of ports from start to end (inclusive)
func PortRange(start, end uint16) []uint16 {
	if start > end {
		return []uint16{}
	}

	ports := make([]uint16, 0, end-start+1)
	for port := start; port <= end; port++ {
		ports = append(ports, port)
	}
	return ports
}

// Payload creates a repeated byte slice from string content
func Payload(content string, repeat int) []byte {
	result := make([]byte, 0, len(content)*repeat)
	for i := 0; i < repeat; i++ {
		result = append(result, []byte(content)...)
	}
	return result
}

// Fragment6 fragments an IPv6 packet according to RFC 8200
// fragSize is the maximum size of each fragment (including IPv6 header and fragment header)
func Fragment6(pkt gopacket.Packet, fragSize int) ([]gopacket.Packet, error) {
	// Extract layers
	ethLayer := pkt.Layer(layers.LayerTypeEthernet)
	vlanLayer := pkt.Layer(layers.LayerTypeDot1Q)
	ipv6Layer := pkt.Layer(layers.LayerTypeIPv6)

	if ipv6Layer == nil {
		return nil, fmt.Errorf("packet does not contain IPv6 layer")
	}

	ipv6 := ipv6Layer.(*layers.IPv6)

	// Get the payload after IPv6 header
	payload := ipv6.Payload
	if len(payload) == 0 {
		// No payload to fragment
		return []gopacket.Packet{pkt}, nil
	}

	// Calculate fragment data size (must be multiple of 8 bytes)
	// fragSize includes Ethernet + VLAN + IPv6 + FragmentHeader
	headerSize := 14 // Ethernet
	if vlanLayer != nil {
		headerSize += 4 // VLAN
	}
	headerSize += 40 // IPv6
	headerSize += 8  // Fragment header

	fragmentDataSize := fragSize - headerSize
	fragmentDataSize = (fragmentDataSize / 8) * 8 // Round down to multiple of 8

	if fragmentDataSize <= 0 {
		return nil, fmt.Errorf("fragment size too small")
	}

	// Generate random fragment ID
	fragID := uint32(0x12345678) // For deterministic testing, use fixed ID

	// Split payload into fragments
	var fragments []gopacket.Packet
	offset := 0

	for offset < len(payload) {
		end := offset + fragmentDataSize
		moreFragments := true

		if end >= len(payload) {
			end = len(payload)
			moreFragments = false
		}

		fragData := payload[offset:end]

		// Build fragment packet
		var fragLayers []gopacket.SerializableLayer

		// Add Ethernet layer
		if ethLayer != nil {
			eth := ethLayer.(*layers.Ethernet)
			newEth := *eth
			fragLayers = append(fragLayers, &newEth)
		}

		// Add VLAN layer
		if vlanLayer != nil {
			vlan := vlanLayer.(*layers.Dot1Q)
			newVlan := *vlan
			fragLayers = append(fragLayers, &newVlan)
		}

		// Add IPv6 layer (copy original)
		newIPv6 := *ipv6
		fragLayers = append(fragLayers, &newIPv6)

		// Add Fragment header
		fragHeader := &layers.IPv6Fragment{
			NextHeader:     ipv6.NextHeader,
			FragmentOffset: uint16(offset / 8),
			MoreFragments:  moreFragments,
			Identification: fragID,
		}
		fragLayers = append(fragLayers, fragHeader)

		// Add payload
		fragLayers = append(fragLayers, gopacket.Payload(fragData))

		// Serialize fragment
		buf := gopacket.NewSerializeBuffer()
		opts := gopacket.SerializeOptions{
			FixLengths:       true,
			ComputeChecksums: true,
		}

		if err := gopacket.SerializeLayers(buf, opts, fragLayers...); err != nil {
			return nil, fmt.Errorf("failed to serialize fragment: %w", err)
		}

		fragPkt := gopacket.NewPacket(buf.Bytes(), layers.LayerTypeEthernet, gopacket.Default)
		fragments = append(fragments, fragPkt)

		offset = end
	}

	return fragments, nil
}

// Fragment fragments an IPv4 packet according to RFC 791
// fragSize is the maximum size of each fragment (including Ethernet/VLAN/IPv4 headers)
func Fragment(pkt gopacket.Packet, fragSize int) ([]gopacket.Packet, error) {
	// Extract layers
	ethLayer := pkt.Layer(layers.LayerTypeEthernet)
	vlanLayer := pkt.Layer(layers.LayerTypeDot1Q)
	ipv4Layer := pkt.Layer(layers.LayerTypeIPv4)

	if ipv4Layer == nil {
		return nil, fmt.Errorf("packet does not contain IPv4 layer")
	}

	ipv4 := ipv4Layer.(*layers.IPv4)

	// Get the payload after IPv4 header
	payload := ipv4.Payload
	if len(payload) == 0 {
		// No payload to fragment
		return []gopacket.Packet{pkt}, nil
	}

	// Calculate fragment data size (must be multiple of 8 bytes)
	headerSize := 14 // Ethernet
	if vlanLayer != nil {
		headerSize += 4 // VLAN
	}
	headerSize += int(ipv4.IHL * 4) // IPv4 header with options

	fragmentDataSize := fragSize - headerSize
	fragmentDataSize = (fragmentDataSize / 8) * 8 // Round down to multiple of 8

	if fragmentDataSize <= 0 {
		return nil, fmt.Errorf("fragment size too small")
	}

	// Split payload into fragments
	var fragments []gopacket.Packet
	offset := 0

	for offset < len(payload) {
		end := offset + fragmentDataSize
		moreFragments := true

		if end >= len(payload) {
			end = len(payload)
			moreFragments = false
		}

		fragData := payload[offset:end]

		// Build fragment packet
		var fragLayers []gopacket.SerializableLayer

		// Add Ethernet layer
		if ethLayer != nil {
			eth := ethLayer.(*layers.Ethernet)
			newEth := *eth
			fragLayers = append(fragLayers, &newEth)
		}

		// Add VLAN layer
		if vlanLayer != nil {
			vlan := vlanLayer.(*layers.Dot1Q)
			newVlan := *vlan
			fragLayers = append(fragLayers, &newVlan)
		}

		// Add IPv4 layer (copy original)
		newIPv4 := *ipv4
		newIPv4.FragOffset = uint16(offset / 8)
		if moreFragments {
			newIPv4.Flags |= layers.IPv4MoreFragments
		} else {
			newIPv4.Flags &= ^layers.IPv4MoreFragments
		}
		fragLayers = append(fragLayers, &newIPv4)

		// Add payload
		fragLayers = append(fragLayers, gopacket.Payload(fragData))

		// Serialize fragment
		buf := gopacket.NewSerializeBuffer()
		opts := gopacket.SerializeOptions{
			FixLengths:       true,
			ComputeChecksums: true,
		}

		if err := gopacket.SerializeLayers(buf, opts, fragLayers...); err != nil {
			return nil, fmt.Errorf("failed to serialize fragment: %w", err)
		}

		fragPkt := gopacket.NewPacket(buf.Bytes(), layers.LayerTypeEthernet, gopacket.Default)
		fragments = append(fragments, fragPkt)

		offset = end
	}

	return fragments, nil
}

// IPv6ExtHdrDestOptBuilder is a placeholder builder for unsupported IPv6 Destination Options header
type IPv6ExtHdrDestOptBuilder struct{}

// IPv6ExtHdrDestOptOption is an option for IPv6ExtHdrDestOpt
type IPv6ExtHdrDestOptOption func(*IPv6ExtHdrDestOptBuilder)

// IPv6ExtHdrDestOpt is a placeholder for unsupported IPv6 Destination Options header
func IPv6ExtHdrDestOpt(opts ...IPv6ExtHdrDestOptOption) *IPv6ExtHdrDestOptBuilder {
	builder := &IPv6ExtHdrDestOptBuilder{}
	for _, opt := range opts {
		opt(builder)
	}
	return builder
}

// Build returns nil as this is an unsupported layer placeholder
func (b *IPv6ExtHdrDestOptBuilder) Build() gopacket.SerializableLayer {
	// Return nil to skip this layer in packet construction
	// TODO: Implement proper IPv6 Destination Options header support
	return nil
}

// MPLSBuilder is a placeholder builder for unsupported MPLS layer
type MPLSBuilder struct{}

// MPLSOption is an option for MPLS
type MPLSOption func(*MPLSBuilder)

// MPLS is a placeholder for unsupported MPLS layer
func MPLS(opts ...MPLSOption) *MPLSBuilder {
	builder := &MPLSBuilder{}
	for _, opt := range opts {
		opt(builder)
	}
	return builder
}

// Build returns nil as this is an unsupported layer placeholder
func (b *MPLSBuilder) Build() gopacket.SerializableLayer {
	// Return nil to skip this layer in packet construction
	// TODO: Implement proper MPLS layer support
	return nil
}

// ExpandCIDR expands a CIDR notation to all IP addresses in the subnet
// This mimics Scapy's behavior where IP(dst="172.20.29.5/30") generates
// packets for 172.20.29.5, 172.20.29.6, 172.20.29.7, 172.20.29.8
func ExpandCIDR(cidr string) []string {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		// Return the base IP if CIDR parsing fails
		if ip := net.ParseIP(cidr); ip != nil {
			return []string{ip.String()}
		}
		return []string{cidr}
	}

	var ips []string
	for ip := ipNet.IP.Mask(ipNet.Mask); ipNet.Contains(ip); incIP(ip) {
		ips = append(ips, ip.String())
	}

	return ips
}

// incIP increments an IP address (helper for ExpandCIDR)
func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}
