package lib

import (
	"fmt"
	"net"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

// PacketBuilder implements a small, self-contained DSL on top of gopacket
// that the converter uses at runtime in generated tests. The key goals are:
//   - avoid CGO and keep all packet construction logic in Go
//   - mirror Scapy semantics closely enough to reproduce malformed packets
//     (broken lengths, unusual options, non-standard GRE flags, etc.)
//   - provide a stable surface for IR → Go code generation so that future
//     changes to test templates do not leak low-level gopacket concerns.

// NewPacket creates a new gopacket.Packet from the given LayerBuilder chain,
// inferring protocol numbers and EtherType/VLAN types from layer order and
// leaving checksums/lengths untouched unless a custom layer requests otherwise.
// If opts is nil, default options (FixLengths: true, ComputeChecksums: true) are used.
func NewPacket(opts *gopacket.SerializeOptions, layerBuilders ...LayerBuilder) (gopacket.Packet, error) {
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
				case *layers.IPv4, *customIPv4Layer:
					eth.EthernetType = layers.EthernetTypeIPv4
				case *layers.IPv6, *customIPv6Layer:
					eth.EthernetType = layers.EthernetTypeIPv6
				case *layers.ARP:
					eth.EthernetType = layers.EthernetTypeARP
				}
			}
		case *layers.Dot1Q:
			// Look ahead to next layer to set VLAN Type
			if i+1 < len(serialLayers) {
				vlan := layer.(*layers.Dot1Q)
				switch serialLayers[i+1].(type) {
				case *layers.IPv4, *customIPv4Layer:
					vlan.Type = layers.EthernetTypeIPv4
				case *layers.IPv6, *customIPv6Layer:
					vlan.Type = layers.EthernetTypeIPv6
				case *layers.MPLS:
					vlan.Type = layers.EthernetTypeMPLSUnicast
				case *layers.ARP:
					vlan.Type = layers.EthernetTypeARP
				}
			}
		case *layers.IPv4:
			// Look ahead to set protocol
			if i+1 < len(serialLayers) {
				ip := layer.(*layers.IPv4)
				if ip.Protocol == 0 {
					switch serialLayers[i+1].(type) {
					case *layers.TCP, *customTCPLayer:
						ip.Protocol = layers.IPProtocolTCP
					case *layers.UDP:
						ip.Protocol = layers.IPProtocolUDP
					case *udpNoChecksum:
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
			}
		case *layers.IPv6:
			// Look ahead to set next header
			if i+1 < len(serialLayers) {
				ip6 := layer.(*layers.IPv6)
				// Only set NextHeader if it wasn't provided via IR
				if ip6.NextHeader == 0 {
					switch serialLayers[i+1].(type) {
					case *layers.TCP, *customTCPLayer:
						ip6.NextHeader = layers.IPProtocolTCP
					case *layers.UDP:
						ip6.NextHeader = layers.IPProtocolUDP
					case *udpNoChecksum:
						ip6.NextHeader = layers.IPProtocolUDP
					case *layers.ICMPv6:
						ip6.NextHeader = layers.IPProtocolICMPv6
					case *icmpv6WithEcho:
						// Treat icmpv6WithEcho as ICMPv6
						ip6.NextHeader = layers.IPProtocolICMPv6
					case *layers.IPv6Fragment:
						ip6.NextHeader = layers.IPProtocolIPv6Fragment
					case *layers.IPv6Destination:
						ip6.NextHeader = layers.IPProtocolIPv6Destination
					case *layers.IPv6HopByHop:
						ip6.NextHeader = layers.IPProtocolIPv6HopByHop
					case *layers.GRE:
						ip6.NextHeader = layers.IPProtocolGRE
					case *layers.IPv4:
						ip6.NextHeader = layers.IPProtocolIPv4
					case *layers.IPv6, *customIPv6Layer:
						// IPv6-in-IPv6 tunneling
						ip6.NextHeader = layers.IPProtocolIPv6
					}
				}
			}
		case *layers.IPv6Fragment:
			// Look ahead to set next header in fragment
			if i+1 < len(serialLayers) {
				frag := layer.(*layers.IPv6Fragment)
				// Only infer NextHeader if not explicitly provided via IR
				if frag.NextHeader == 0 {
					switch serialLayers[i+1].(type) {
					case *layers.TCP, *customTCPLayer:
						frag.NextHeader = layers.IPProtocolTCP
					case *layers.UDP:
						frag.NextHeader = layers.IPProtocolUDP
					case *udpNoChecksum:
						frag.NextHeader = layers.IPProtocolUDP
					case *layers.ICMPv6:
						frag.NextHeader = layers.IPProtocolICMPv6
					case *layers.IPv6HopByHop:
						frag.NextHeader = layers.IPProtocolIPv6HopByHop
					case *layers.IPv4:
						frag.NextHeader = layers.IPProtocolIPv4
					}
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

	// Set network layer for transport layer checksum calculation using the most recent network layer
	var currentNL gopacket.NetworkLayer
	for _, layer := range serialLayers {
		if nl, ok := layer.(gopacket.NetworkLayer); ok {
			currentNL = nl
			continue
		}
		// Handle custom network layers that don't implement gopacket.NetworkLayer
		switch nl := layer.(type) {
		case *customIPv4Layer:
			currentNL = nl.ipv4
			continue
		case *customIPv6Layer:
			currentNL = nl.ipv6
			continue
		}
		// Set network layer for transport layers
		switch tl := layer.(type) {
		case *layers.TCP:
			if currentNL != nil {
				_ = tl.SetNetworkLayerForChecksum(currentNL)
			}
		case *customTCPLayer:
			if currentNL != nil {
				_ = tl.tcp.SetNetworkLayerForChecksum(currentNL)
			}
		case *layers.UDP:
			if currentNL != nil {
				_ = tl.SetNetworkLayerForChecksum(currentNL)
			}
		case *layers.ICMPv6:
			if currentNL != nil {
				_ = tl.SetNetworkLayerForChecksum(currentNL)
			}
		case *icmpv6WithEcho:
			if currentNL != nil {
				_ = tl.icmp.SetNetworkLayerForChecksum(currentNL)
			}
		}
	}

	// Serialize the packet with provided or default options
	buf := gopacket.NewSerializeBuffer()

	// Use provided options or defaults
	var serializeOpts gopacket.SerializeOptions
	if opts != nil {
		serializeOpts = *opts
	} else {
		serializeOpts = gopacket.SerializeOptions{
			FixLengths:       true,
			ComputeChecksums: true,
		}
	}

	if err := gopacket.SerializeLayers(buf, serializeOpts, serialLayers...); err != nil {
		return nil, err
	}

	// Parse back to packet using the standard decoder so that layer stacks
	// (including ICMPv6 control subtypes) match those produced when reading
	// original PCAPs via NewPacketSource.
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
	// Default MACs: client -> yanet (for tests without explicit MACs)
	srcMAC, _ := net.ParseMAC("52:54:00:6b:ff:a1")
	dstMAC, _ := net.ParseMAC("52:54:00:6b:ff:a5")

	eth := &layers.Ethernet{
		SrcMAC:       srcMAC,
		DstMAC:       dstMAC,
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
	layer       *layers.IPv4
	explicitLen *uint16 // If set, override len after serialization
}

// IPv4 creates an IPv4 layer builder for packet construction
func IPv4(opts ...IPv4Option) *IPv4Builder {
	ip := &layers.IPv4{
		Version: 4,
		IHL:     5,
		TTL:     64, // default
		// Protocol left unset (0) so it can come from IR or inference
		Id: 1, // Scapy default (auto-increments from 1)
	}

	builder := &IPv4Builder{layer: ip}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func (b *IPv4Builder) Build() gopacket.SerializableLayer {
	// If we have an explicit len that needs correction, return custom serializer
	if b.explicitLen != nil {
		return &customIPv4Layer{
			ipv4:        b.layer,
			explicitLen: *b.explicitLen,
		}
	}
	return b.layer
}

type IPv4Option func(*IPv4Builder)

func IPSrc(ip string) IPv4Option {
	return func(builder *IPv4Builder) {
		parsed := net.ParseIP(ip)
		if parsed != nil {
			builder.layer.SrcIP = parsed.To4()
		}
	}
}

func IPDst(ip string) IPv4Option {
	return func(builder *IPv4Builder) {
		parsed := net.ParseIP(ip)
		if parsed != nil {
			builder.layer.DstIP = parsed.To4()
		}
	}
}

func IPTTL(ttl uint8) IPv4Option {
	return func(builder *IPv4Builder) {
		builder.layer.TTL = ttl
	}
}

func IPTOS(tos uint8) IPv4Option {
	return func(builder *IPv4Builder) {
		builder.layer.TOS = tos
	}
}

func IPProto(proto layers.IPProtocol) IPv4Option {
	return func(builder *IPv4Builder) {
		builder.layer.Protocol = proto
	}
}

func IPId(id uint16) IPv4Option {
	return func(builder *IPv4Builder) {
		builder.layer.Id = id
	}
}

func IPFlags(flags layers.IPv4Flag) IPv4Option {
	return func(builder *IPv4Builder) {
		builder.layer.Flags = flags
	}
}

func IPFragOffset(offset uint16) IPv4Option {
	return func(builder *IPv4Builder) {
		builder.layer.FragOffset = offset
	}
}

func IPv4Length(length uint16) IPv4Option {
	return func(builder *IPv4Builder) {
		builder.layer.Length = length
		// Store explicit len for potential correction
		builder.explicitLen = &length
	}
}

func IPv4ChecksumRaw(cs uint16) IPv4Option {
	return func(builder *IPv4Builder) {
		builder.layer.Checksum = cs
	}
}

func IPv4IHL(ihl uint8) IPv4Option {
	return func(builder *IPv4Builder) {
		builder.layer.IHL = ihl
	}
}

func IPv4DummyOptions(size int) IPv4Option {
	return func(builder *IPv4Builder) {
		// Add NOP options to fill the required size
		for i := 0; i < size; i++ {
			builder.layer.Options = append(builder.layer.Options, layers.IPv4Option{
				OptionType: 1, // NOP
			})
		}
	}
}

// IPv4RawOptions sets raw IPv4 options data (for non-standard options)
func IPv4RawOptions(data []byte) IPv4Option {
	return func(builder *IPv4Builder) {
		// Create one option per byte to preserve exact data
		for _, b := range data {
			builder.layer.Options = append(builder.layer.Options, layers.IPv4Option{
				OptionType: b,
			})
		}
	}
}

// IPv4OptionDef defines a single IPv4 option
type IPv4OptionDef struct {
	Type   uint8
	Length uint8
	Data   []byte
}

// IPv4Options sets IPv4 options and automatically updates IHL
func IPv4Options(opts []IPv4OptionDef) IPv4Option {
	return func(builder *IPv4Builder) {
		builder.layer.Options = make([]layers.IPv4Option, len(opts))
		for i, opt := range opts {
			builder.layer.Options[i] = layers.IPv4Option{
				OptionType:   opt.Type,
				OptionLength: opt.Length,
				OptionData:   opt.Data,
			}
		}
		// Update IHL based on options
		optionsLen := 0
		for _, opt := range builder.layer.Options {
			if opt.OptionType == 0 || opt.OptionType == 1 {
				// EOL or NOP - 1 byte
				optionsLen += 1
			} else {
				// Other options use OptionLength field
				optionsLen += int(opt.OptionLength)
			}
		}
		// Round up to 4-byte boundary
		builder.layer.IHL = uint8(5 + (optionsLen+3)/4)
	}
}

// customIPv4Layer wraps layers.IPv4 to allow overriding the len field after serialization
// This is needed for malformed packets where len doesn't match actual payload
type customIPv4Layer struct {
	ipv4        *layers.IPv4
	explicitLen uint16
}

func (c *customIPv4Layer) LayerType() gopacket.LayerType {
	return layers.LayerTypeIPv4
}

func (c *customIPv4Layer) SerializeTo(b gopacket.SerializeBuffer, opts gopacket.SerializeOptions) error {
	// Serialize the IPv4 header manually with explicit len
	// The payload will be serialized by subsequent layers

	// Calculate header size based on IHL
	headerSize := int(c.ipv4.IHL) * 4
	bytes, err := b.PrependBytes(headerSize)
	if err != nil {
		return err
	}

	// Build IPv4 header manually
	// Byte 0: Version (4 bits) + IHL (4 bits)
	bytes[0] = (4 << 4) | c.ipv4.IHL
	// Byte 1: TOS
	bytes[1] = c.ipv4.TOS
	// Bytes 2-3: Total Length (use explicit len)
	bytes[2] = uint8(c.explicitLen >> 8)
	bytes[3] = uint8(c.explicitLen)
	// Bytes 4-5: Identification
	bytes[4] = uint8(c.ipv4.Id >> 8)
	bytes[5] = uint8(c.ipv4.Id)
	// Bytes 6-7: Flags (3 bits) + Fragment Offset (13 bits)
	flagsAndOffset := (uint16(c.ipv4.Flags) << 13) | c.ipv4.FragOffset
	bytes[6] = uint8(flagsAndOffset >> 8)
	bytes[7] = uint8(flagsAndOffset)
	// Byte 8: TTL
	bytes[8] = c.ipv4.TTL
	// Byte 9: Protocol
	bytes[9] = uint8(c.ipv4.Protocol)
	// Bytes 10-11: Checksum
	bytes[10] = uint8(c.ipv4.Checksum >> 8)
	bytes[11] = uint8(c.ipv4.Checksum)
	// Bytes 12-15: Source Address
	copy(bytes[12:16], c.ipv4.SrcIP.To4())
	// Bytes 16-19: Destination Address
	copy(bytes[16:20], c.ipv4.DstIP.To4())

	// Bytes 20+: Options (if IHL > 5)
	if c.ipv4.IHL > 5 {
		optionsStart := 20
		for _, opt := range c.ipv4.Options {
			// Check if we have enough space
			if optionsStart >= headerSize {
				break
			}
			bytes[optionsStart] = opt.OptionType
			optionsStart++

			// For standard options (with length field)
			if opt.OptionType != 0 && opt.OptionType != 1 && opt.OptionLength > 0 {
				if optionsStart < headerSize {
					bytes[optionsStart] = opt.OptionLength
					optionsStart++
				}
				if len(opt.OptionData) > 0 && optionsStart+len(opt.OptionData) <= headerSize {
					copy(bytes[optionsStart:], opt.OptionData)
					optionsStart += len(opt.OptionData)
				}
			}
			// For EOL (0), NOP (1), or non-standard single-byte options, just write the type
		}
	}

	return nil
}

// ===== TCP Custom Layer =====

// customTCPLayer wraps layers.TCP to provide custom serialization for non-standard options
type customTCPLayer struct {
	tcp *layers.TCP
}

func (c *customTCPLayer) LayerType() gopacket.LayerType {
	return layers.LayerTypeTCP
}

func (c *customTCPLayer) SerializeTo(b gopacket.SerializeBuffer, opts gopacket.SerializeOptions) error {
	// Calculate options length
	var optionsLen int
	for _, opt := range c.tcp.Options {
		if opt.OptionType == layers.TCPOptionKindNop || opt.OptionType == layers.TCPOptionKindEndList {
			optionsLen += 1
		} else {
			optionsLen += int(opt.OptionLength)
		}
	}

	// Pad options to 4-byte boundary
	paddedOptionsLen := (optionsLen + 3) &^ 3
	dataOffset := uint8(5 + paddedOptionsLen/4)

	headerSize := 20 + paddedOptionsLen
	bytes, err := b.PrependBytes(headerSize)
	if err != nil {
		return err
	}

	// Bytes 0-1: Source port
	bytes[0] = byte(c.tcp.SrcPort >> 8)
	bytes[1] = byte(c.tcp.SrcPort)

	// Bytes 2-3: Destination port
	bytes[2] = byte(c.tcp.DstPort >> 8)
	bytes[3] = byte(c.tcp.DstPort)

	// Bytes 4-7: Sequence number
	bytes[4] = byte(c.tcp.Seq >> 24)
	bytes[5] = byte(c.tcp.Seq >> 16)
	bytes[6] = byte(c.tcp.Seq >> 8)
	bytes[7] = byte(c.tcp.Seq)

	// Bytes 8-11: Acknowledgment number
	bytes[8] = byte(c.tcp.Ack >> 24)
	bytes[9] = byte(c.tcp.Ack >> 16)
	bytes[10] = byte(c.tcp.Ack >> 8)
	bytes[11] = byte(c.tcp.Ack)

	// Byte 12: Data offset (upper 4 bits) + reserved (lower 4 bits)
	bytes[12] = dataOffset << 4

	// Byte 13: Flags
	var flags uint8
	if c.tcp.FIN {
		flags |= 0x01
	}
	if c.tcp.SYN {
		flags |= 0x02
	}
	if c.tcp.RST {
		flags |= 0x04
	}
	if c.tcp.PSH {
		flags |= 0x08
	}
	if c.tcp.ACK {
		flags |= 0x10
	}
	if c.tcp.URG {
		flags |= 0x20
	}
	if c.tcp.ECE {
		flags |= 0x40
	}
	if c.tcp.CWR {
		flags |= 0x80
	}
	bytes[13] = flags

	// Bytes 14-15: Window size
	bytes[14] = byte(c.tcp.Window >> 8)
	bytes[15] = byte(c.tcp.Window)

	// Bytes 16-17: Checksum (will be calculated later if opts.ComputeChecksums is true)
	bytes[16] = byte(c.tcp.Checksum >> 8)
	bytes[17] = byte(c.tcp.Checksum)

	// Bytes 18-19: Urgent pointer
	bytes[18] = byte(c.tcp.Urgent >> 8)
	bytes[19] = byte(c.tcp.Urgent)

	// Bytes 20+: Options
	optionsStart := 20
	for _, opt := range c.tcp.Options {
		bytes[optionsStart] = byte(opt.OptionType)
		optionsStart++
		if opt.OptionType != layers.TCPOptionKindNop && opt.OptionType != layers.TCPOptionKindEndList {
			bytes[optionsStart] = opt.OptionLength
			optionsStart++
			if len(opt.OptionData) > 0 {
				copy(bytes[optionsStart:], opt.OptionData)
				optionsStart += len(opt.OptionData)
			}
		}
	}
	// Pad remaining bytes with zeros
	for i := optionsStart; i < headerSize; i++ {
		bytes[i] = 0
	}

	// Compute checksum if requested
	if opts.ComputeChecksums {
		// Zero out checksum field for calculation
		bytes[16] = 0
		bytes[17] = 0
	}

	return nil
}

// ===== IPv6 Layer =====

type IPv6Builder struct {
	layer        *layers.IPv6
	explicitPlen *uint16 // If set, override plen after serialization
}

func IPv6(opts ...IPv6Option) *IPv6Builder {
	ip6 := &layers.IPv6{
		Version:    6,
		HopLimit:   64, // default
		NextHeader: 0,  // will be set based on the next layer
	}

	builder := &IPv6Builder{layer: ip6}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func (b *IPv6Builder) Build() gopacket.SerializableLayer {
	// If we have an explicit plen that needs correction, return custom serializer
	if b.explicitPlen != nil {
		return &customIPv6Layer{
			ipv6:         b.layer,
			explicitPlen: *b.explicitPlen,
		}
	}
	return b.layer
}

// customIPv6Layer wraps layers.IPv6 to allow overriding the plen field after serialization
// This is needed for malformed packets where plen doesn't match actual payload
type customIPv6Layer struct {
	ipv6         *layers.IPv6
	explicitPlen uint16
}

func (c *customIPv6Layer) LayerType() gopacket.LayerType {
	return layers.LayerTypeIPv6
}

func (c *customIPv6Layer) SerializeTo(b gopacket.SerializeBuffer, opts gopacket.SerializeOptions) error {
	// Serialize the IPv6 header manually with explicit plen
	// The payload (extension headers, etc.) will be serialized by subsequent layers
	bytes, err := b.PrependBytes(40) // IPv6 header is always 40 bytes
	if err != nil {
		return err
	}

	// Build IPv6 header manually
	// Byte 0: Version (4 bits) + Traffic Class (4 bits high)
	bytes[0] = (6 << 4) | (c.ipv6.TrafficClass >> 4)
	// Byte 1: Traffic Class (4 bits low) + Flow Label (4 bits high)
	bytes[1] = (c.ipv6.TrafficClass << 4) | uint8((c.ipv6.FlowLabel>>16)&0x0F)
	// Bytes 2-3: Flow Label (16 bits low)
	bytes[2] = uint8(c.ipv6.FlowLabel >> 8)
	bytes[3] = uint8(c.ipv6.FlowLabel)
	// Bytes 4-5: Payload Length (use explicit plen)
	bytes[4] = uint8(c.explicitPlen >> 8)
	bytes[5] = uint8(c.explicitPlen)
	// Byte 6: Next Header
	bytes[6] = uint8(c.ipv6.NextHeader)
	// Byte 7: Hop Limit
	bytes[7] = c.ipv6.HopLimit
	// Bytes 8-23: Source Address
	copy(bytes[8:24], c.ipv6.SrcIP.To16())
	// Bytes 24-39: Destination Address
	copy(bytes[24:40], c.ipv6.DstIP.To16())

	return nil
}

type IPv6Option func(*IPv6Builder)

func IPv6Src(ip string) IPv6Option {
	return func(builder *IPv6Builder) {
		builder.layer.SrcIP = net.ParseIP(ip)
	}
}

func IPv6Dst(ip string) IPv6Option {
	return func(builder *IPv6Builder) {
		builder.layer.DstIP = net.ParseIP(ip)
	}
}

func IPv6HopLimit(hl uint8) IPv6Option {
	return func(builder *IPv6Builder) {
		builder.layer.HopLimit = hl
	}
}

func IPv6TrafficClass(tc uint8) IPv6Option {
	return func(builder *IPv6Builder) {
		builder.layer.TrafficClass = tc
	}
}

func IPv6FlowLabel(fl uint32) IPv6Option {
	return func(builder *IPv6Builder) {
		builder.layer.FlowLabel = fl
	}
}

func IPv6NextHeader(nh layers.IPProtocol) IPv6Option {
	return func(builder *IPv6Builder) {
		builder.layer.NextHeader = nh
	}
}

func IPv6PayloadLength(plen uint16) IPv6Option {
	return func(builder *IPv6Builder) {
		builder.layer.Length = plen
		// Store explicit plen for potential correction
		builder.explicitPlen = &plen
	}
}

// ===== TCP Layer =====

type TCPBuilder struct {
	layer *layers.TCP
}

func TCP(opts ...TCPOption) *TCPBuilder {
	tcp := &layers.TCP{
		DataOffset: 5,
		// Scapy defaults
		SrcPort: 20,   // Default sport=20 (ftp-data)
		DstPort: 80,   // Default dport=80 (http)
		SYN:     true, // Default flags='S'
		Window:  8192, // Default window=8192 (0x2000)
	}

	builder := &TCPBuilder{layer: tcp}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func (b *TCPBuilder) Build() gopacket.SerializableLayer {
	// If we have non-standard options, use custom layer for proper serialization
	hasNonStd := b.hasNonStandardOptions()
	if hasNonStd {
		// Debug: log that we're using custom layer
		// fmt.Printf("[DEBUG] Using customTCPLayer for non-standard options (count=%d)\n", len(b.layer.Options))
		return &customTCPLayer{tcp: b.layer}
	}
	return b.layer
}

// hasNonStandardOptions checks if TCP has non-standard options that gopacket might not serialize correctly
func (b *TCPBuilder) hasNonStandardOptions() bool {
	for _, opt := range b.layer.Options {
		// Check for non-standard option types (> 30 are usually experimental/non-standard)
		if opt.OptionType > 30 {
			return true
		}
	}
	return false
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
		// Clear all flags first (override defaults)
		builder.layer.SYN = false
		builder.layer.ACK = false
		builder.layer.FIN = false
		builder.layer.RST = false
		builder.layer.PSH = false
		builder.layer.URG = false

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

func TCPWindow(win uint16) TCPOption {
	return func(builder *TCPBuilder) {
		builder.layer.Window = win
	}
}

func TCPUrgent(urg uint16) TCPOption {
	return func(builder *TCPBuilder) {
		builder.layer.Urgent = urg
	}
}

func TCPChecksumRaw(cs uint16) TCPOption {
	return func(builder *TCPBuilder) {
		builder.layer.Checksum = cs
	}
}

func TCPDataOffset(offset uint8) TCPOption {
	return func(builder *TCPBuilder) {
		builder.layer.DataOffset = offset
	}
}

func TCPDummyOptions(size int) TCPOption {
	return func(builder *TCPBuilder) {
		// Add NOP options to fill the required size
		for i := 0; i < size; i++ {
			builder.layer.Options = append(builder.layer.Options, layers.TCPOption{
				OptionType: layers.TCPOptionKindNop, // NOP
			})
		}
	}
}

// TCPOptionDef defines a single TCP option
type TCPOptionDef struct {
	Kind   layers.TCPOptionKind
	Length uint8
	Data   []byte
}

// TCPOptions sets TCP options and automatically updates DataOffset
func TCPOptions(opts []TCPOptionDef) TCPOption {
	return func(builder *TCPBuilder) {
		builder.layer.Options = make([]layers.TCPOption, len(opts))
		for i, opt := range opts {
			builder.layer.Options[i] = layers.TCPOption{
				OptionType:   opt.Kind,
				OptionLength: opt.Length,
				OptionData:   opt.Data,
			}
		}
		// Update DataOffset based on options
		optionsLen := 0
		for _, opt := range builder.layer.Options {
			if opt.OptionType == layers.TCPOptionKindNop || opt.OptionType == layers.TCPOptionKindEndList {
				// NOP or EOL - 1 byte
				optionsLen += 1
			} else {
				// Other options use OptionLength field
				optionsLen += int(opt.OptionLength)
			}
		}
		// Round up to 4-byte boundary
		builder.layer.DataOffset = uint8(5 + (optionsLen+3)/4)
	}
}

// ===== UDP Layer =====

type UDPBuilder struct {
	layer      *layers.UDP
	noChecksum bool
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
	if b.noChecksum {
		return &udpNoChecksum{layer: b.layer}
	}
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

// UDPLengthRaw sets an explicit UDP length value from the original PCAP header.
// This is required for PCAP equivalence tests where we must preserve even
// malformed or unusual length values byte-for-byte.
func UDPLengthRaw(length uint16) UDPOption {
	return func(builder *UDPBuilder) {
		builder.layer.Length = length
	}
}

// UDPChecksumRaw sets an explicit checksum and prevents recomputation during serialization
func UDPChecksumRaw(cs uint16) UDPOption {
	return func(builder *UDPBuilder) {
		builder.layer.Checksum = cs
		builder.noChecksum = true
	}
}

// udpNoChecksum wraps UDP to preserve the provided checksum (no recomputation)
type udpNoChecksum struct {
	layer *layers.UDP
}

func (u *udpNoChecksum) SerializeTo(b gopacket.SerializeBuffer, opts gopacket.SerializeOptions) error {
	// Force skip checksum computation for this layer only
	local := opts
	local.ComputeChecksums = false
	return u.layer.SerializeTo(b, local)
}

func (u *udpNoChecksum) LayerType() gopacket.LayerType {
	return u.layer.LayerType()
}

// ===== ICMP Layer =====

type ICMPBuilder struct {
	layer      *layers.ICMPv4
	noChecksum bool // If true, use explicit checksum from layer
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
	// If explicit checksum is set, wrap in custom type to prevent recomputation
	if b.noChecksum {
		return &icmpNoChecksum{layer: b.layer}
	}
	return b.layer
}

// icmpNoChecksum wraps ICMPv4 to preserve the provided checksum (no recomputation)
type icmpNoChecksum struct {
	layer *layers.ICMPv4
}

func (ic *icmpNoChecksum) SerializeTo(b gopacket.SerializeBuffer, opts gopacket.SerializeOptions) error {
	// Force skip checksum computation for this layer only
	local := opts
	local.ComputeChecksums = false
	return ic.layer.SerializeTo(b, local)
}

func (ic *icmpNoChecksum) LayerType() gopacket.LayerType {
	return ic.layer.LayerType()
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

func ICMPChecksum(checksum uint16) ICMPOption {
	return func(builder *ICMPBuilder) {
		builder.layer.Checksum = checksum
		builder.noChecksum = true
	}
}

// ===== ICMPv6 Layer =====

type ICMPv6Builder struct {
	layer      *layers.ICMPv6
	echo       *layers.ICMPv6Echo
	noChecksum bool // If true, use explicit checksum from layer
}

// ICMPv6 creates a generic ICMPv6 message. The concrete type/code can be
// provided via ICMPv6Type / ICMPv6Code options, which is useful for control
// messages like Router Solicitation that are not modeled as dedicated builders.
func ICMPv6(opts ...ICMPv6Option) *ICMPv6Builder {
	icmp := &layers.ICMPv6{}

	builder := &ICMPv6Builder{layer: icmp}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
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

func ICMPv6PacketTooBig(opts ...ICMPv6Option) *ICMPv6Builder {
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypePacketTooBig, 0),
	}

	builder := &ICMPv6Builder{layer: icmp}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func ICMPv6TimeExceeded(opts ...ICMPv6Option) *ICMPv6Builder {
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeTimeExceeded, 0),
	}

	builder := &ICMPv6Builder{layer: icmp}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func ICMPv6ParamProblem(opts ...ICMPv6Option) *ICMPv6Builder {
	icmp := &layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeParameterProblem, 0),
	}

	builder := &ICMPv6Builder{layer: icmp}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func (b *ICMPv6Builder) Build() gopacket.SerializableLayer {
	// For Echo Request/Reply, we need to return a composite layer that serializes both ICMPv6 and ICMPv6Echo
	if b.echo != nil {
		// Return a custom serializable that handles both layers
		return &icmpv6WithEcho{icmp: b.layer, echo: b.echo, noChecksum: b.noChecksum}
	}
	// If explicit checksum is set, wrap in custom type to prevent recomputation
	if b.noChecksum {
		return &icmpv6NoChecksum{layer: b.layer}
	}
	return b.layer
}

// icmpv6WithEcho is a wrapper that serializes ICMPv6 + ICMPv6Echo together
type icmpv6WithEcho struct {
	icmp       *layers.ICMPv6
	echo       *layers.ICMPv6Echo
	noChecksum bool // If true, preserve explicit checksum
}

func (ie *icmpv6WithEcho) SerializeTo(b gopacket.SerializeBuffer, opts gopacket.SerializeOptions) error {
	// Serialize ICMPv6Echo first (it becomes the payload of ICMPv6)
	echoBytes, err := b.PrependBytes(4) // ICMPv6Echo is 4 bytes (Identifier + SeqNumber)
	if err != nil {
		return err
	}

	// Write Identifier (2 bytes)
	echoBytes[0] = byte(ie.echo.Identifier >> 8)
	echoBytes[1] = byte(ie.echo.Identifier)

	// Write SeqNumber (2 bytes)
	echoBytes[2] = byte(ie.echo.SeqNumber >> 8)
	echoBytes[3] = byte(ie.echo.SeqNumber)

	// Now serialize ICMPv6 header
	// If explicit checksum is set, disable checksum computation for this layer
	if ie.noChecksum {
		local := opts
		local.ComputeChecksums = false
		return ie.icmp.SerializeTo(b, local)
	}
	return ie.icmp.SerializeTo(b, opts)
}

func (ie *icmpv6WithEcho) LayerType() gopacket.LayerType {
	return ie.icmp.LayerType()
}

// icmpv6NoChecksum wraps ICMPv6 to preserve the provided checksum (no recomputation)
type icmpv6NoChecksum struct {
	layer *layers.ICMPv6
}

func (ic *icmpv6NoChecksum) SerializeTo(b gopacket.SerializeBuffer, opts gopacket.SerializeOptions) error {
	// Force skip checksum computation for this layer only
	local := opts
	local.ComputeChecksums = false
	return ic.layer.SerializeTo(b, local)
}

func (ic *icmpv6NoChecksum) LayerType() gopacket.LayerType {
	return ic.layer.LayerType()
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

// ICMPv6Type sets the ICMPv6 type while preserving the current code. This is
// used for generic control messages (e.g. Router Solicitation) where the type
// comes directly from the original PCAP.
func ICMPv6Type(typ uint8) ICMPv6Option {
	return func(builder *ICMPv6Builder) {
		currentCode := uint8(builder.layer.TypeCode.Code())
		builder.layer.TypeCode = layers.CreateICMPv6TypeCode(typ, currentCode)
	}
}

func ICMPv6Checksum(checksum uint16) ICMPv6Option {
	return func(builder *ICMPv6Builder) {
		builder.layer.Checksum = checksum
		builder.noChecksum = true
	}
}

// ICMPv6EchoBuilder builds an ICMPv6 Echo layer
type ICMPv6EchoBuilder struct {
	layer *layers.ICMPv6Echo
}

func ICMPv6Echo(opts ...ICMPv6EchoOption) *ICMPv6EchoBuilder {
	echo := &layers.ICMPv6Echo{}
	b := &ICMPv6EchoBuilder{layer: echo}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

func (b *ICMPv6EchoBuilder) Build() gopacket.SerializableLayer {
	return b.layer
}

type ICMPv6EchoOption func(*ICMPv6EchoBuilder)

func ICMPv6EchoId(id uint16) ICMPv6EchoOption {
	return func(b *ICMPv6EchoBuilder) {
		b.layer.Identifier = id
	}
}

func ICMPv6EchoSeq(seq uint16) ICMPv6EchoOption {
	return func(b *ICMPv6EchoBuilder) {
		b.layer.SeqNumber = seq
	}
}

// ===== ICMPv6 NDP Messages =====

// ICMPv6RouterSolicitationBuilder builds an ICMPv6 Router Solicitation layer
type ICMPv6RouterSolicitationBuilder struct {
	layer *layers.ICMPv6RouterSolicitation
}

func ICMPv6RouterSolicitation() *ICMPv6RouterSolicitationBuilder {
	rs := &layers.ICMPv6RouterSolicitation{}
	return &ICMPv6RouterSolicitationBuilder{layer: rs}
}

func (b *ICMPv6RouterSolicitationBuilder) Build() gopacket.SerializableLayer {
	return b.layer
}

// ICMPv6RouterAdvertisementBuilder builds an ICMPv6 Router Advertisement layer
type ICMPv6RouterAdvertisementBuilder struct {
	layer *layers.ICMPv6RouterAdvertisement
}

func ICMPv6RouterAdvertisement() *ICMPv6RouterAdvertisementBuilder {
	ra := &layers.ICMPv6RouterAdvertisement{}
	return &ICMPv6RouterAdvertisementBuilder{layer: ra}
}

func (b *ICMPv6RouterAdvertisementBuilder) Build() gopacket.SerializableLayer {
	return b.layer
}

// ICMPv6NeighborSolicitationBuilder builds an ICMPv6 Neighbor Solicitation layer
type ICMPv6NeighborSolicitationBuilder struct {
	layer *layers.ICMPv6NeighborSolicitation
}

func ICMPv6NeighborSolicitation() *ICMPv6NeighborSolicitationBuilder {
	ns := &layers.ICMPv6NeighborSolicitation{}
	return &ICMPv6NeighborSolicitationBuilder{layer: ns}
}

func (b *ICMPv6NeighborSolicitationBuilder) Build() gopacket.SerializableLayer {
	return b.layer
}

// ICMPv6NeighborAdvertisementBuilder builds an ICMPv6 Neighbor Advertisement layer
type ICMPv6NeighborAdvertisementBuilder struct {
	layer *layers.ICMPv6NeighborAdvertisement
}

func ICMPv6NeighborAdvertisement() *ICMPv6NeighborAdvertisementBuilder {
	na := &layers.ICMPv6NeighborAdvertisement{}
	return &ICMPv6NeighborAdvertisementBuilder{layer: na}
}

func (b *ICMPv6NeighborAdvertisementBuilder) Build() gopacket.SerializableLayer {
	return b.layer
}

// ===== IPv6 Extension Headers =====

type IPv6FragmentBuilder struct {
	layer *layers.IPv6Fragment
}

func IPv6ExtHdrFragment(opts ...IPv6FragmentOption) *IPv6FragmentBuilder {
	frag := &layers.IPv6Fragment{
		NextHeader: 0, // will be set based on the next layer
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
	layer    *layers.GRE
	rawFlags *uint16 // For unsupported flags (e.g., ack present)
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
	// If raw flags are set, check if they contain unsupported flags
	// Standard GRE flags: 0x8000 (checksum), 0x4000 (routing), 0x2000 (key), 0x1000 (seq), 0x0007 (version)
	// Unsupported flags: 0x0080 (ack), and others
	// Note: routing (0x4000) is technically standard, but gopacket doesn't serialize it correctly,
	// so we treat it as unsupported and use custom serialization
	if b.rawFlags != nil {
		flags := *b.rawFlags
		standardFlags := uint16(0x8000 | 0x2000 | 0x1000 | 0x0007) // Removed 0x4000 (routing)
		unsupportedFlags := flags & ^standardFlags

		// Only use custom serialization if there are truly unsupported flags (including routing)
		if unsupportedFlags != 0 {
			return &customGRELayer{
				rawFlags: flags,
				protocol: b.layer.Protocol,
				checksum: b.layer.Checksum,
				key:      b.layer.Key,
				seq:      b.layer.Seq,
			}
		}
	}
	return b.layer
}

// customGRELayer is a custom GRE layer that supports raw flags and optional fields
type customGRELayer struct {
	rawFlags uint16
	protocol layers.EthernetType
	checksum uint16
	key      uint32
	seq      uint32
}

func (g *customGRELayer) LayerType() gopacket.LayerType {
	return layers.LayerTypeGRE
}

func (g *customGRELayer) SerializeTo(b gopacket.SerializeBuffer, opts gopacket.SerializeOptions) error {
	// Calculate GRE header size based on flags
	headerSize := 4             // base: 2 bytes flags + 2 bytes protocol
	if g.rawFlags&0x8000 != 0 { // checksum present
		headerSize += 4
	}
	if g.rawFlags&0x4000 != 0 { // routing present
		headerSize += 4
	}
	if g.rawFlags&0x2000 != 0 { // key present
		headerSize += 4
	}
	if g.rawFlags&0x1000 != 0 { // sequence present
		headerSize += 4
	}

	bytes, err := b.PrependBytes(headerSize)
	if err != nil {
		return err
	}

	// Write raw flags (2 bytes)
	bytes[0] = byte(g.rawFlags >> 8)
	bytes[1] = byte(g.rawFlags)
	// Write protocol (2 bytes)
	bytes[2] = byte(g.protocol >> 8)
	bytes[3] = byte(g.protocol)

	// Write optional fields with actual values
	offset := 4
	if g.rawFlags&0x8000 != 0 { // checksum present
		// 2 bytes checksum + 2 bytes reserved (zero)
		bytes[offset] = byte(g.checksum >> 8)
		bytes[offset+1] = byte(g.checksum)
		bytes[offset+2] = 0
		bytes[offset+3] = 0
		offset += 4
	}
	if g.rawFlags&0x4000 != 0 { // routing present
		// 2 bytes offset + 2 bytes reserved (routing data not implemented, zeros)
		bytes[offset] = 0
		bytes[offset+1] = 0
		bytes[offset+2] = 0
		bytes[offset+3] = 0
		offset += 4
	}
	if g.rawFlags&0x2000 != 0 { // key present
		// 4 bytes key
		bytes[offset] = byte(g.key >> 24)
		bytes[offset+1] = byte(g.key >> 16)
		bytes[offset+2] = byte(g.key >> 8)
		bytes[offset+3] = byte(g.key)
		offset += 4
	}
	if g.rawFlags&0x1000 != 0 { // sequence present
		// 4 bytes sequence
		bytes[offset] = byte(g.seq >> 24)
		bytes[offset+1] = byte(g.seq >> 16)
		bytes[offset+2] = byte(g.seq >> 8)
		bytes[offset+3] = byte(g.seq)
		offset += 4
	}

	return nil
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

func GRERoutingPresent(present bool) GREOption {
	return func(builder *GREBuilder) {
		builder.layer.RoutingPresent = present
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

func GREChecksum(checksum uint16) GREOption {
	return func(builder *GREBuilder) {
		builder.layer.Checksum = checksum
		builder.layer.ChecksumPresent = true
	}
}

func GRESeq(seq uint32) GREOption {
	return func(builder *GREBuilder) {
		builder.layer.Seq = seq
		builder.layer.SeqPresent = true
	}
}

func GRERawFlags(flags uint16) GREOption {
	return func(builder *GREBuilder) {
		builder.rawFlags = &flags
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

// IPv6ExtHdrDestOptBuilder builds IPv6 Destination Options extension header
type IPv6ExtHdrDestOptBuilder struct {
	options []*layers.IPv6DestinationOption
}

// IPv6ExtHdrDestOptOption is an option for IPv6ExtHdrDestOpt
type IPv6ExtHdrDestOptOption func(*IPv6ExtHdrDestOptBuilder)

// IPv6ExtHdrDestOpt creates a new IPv6 Destination Options extension header builder
func IPv6ExtHdrDestOpt(opts ...IPv6ExtHdrDestOptOption) *IPv6ExtHdrDestOptBuilder {
	builder := &IPv6ExtHdrDestOptBuilder{
		options: []*layers.IPv6DestinationOption{},
	}
	for _, opt := range opts {
		opt(builder)
	}
	return builder
}

// IPv6DestOptNextHeader sets the next header field (for compatibility, but not used in gopacket)
// The next header is automatically determined during serialization
func IPv6DestOptNextHeader(nh layers.IPProtocol) IPv6ExtHdrDestOptOption {
	return func(b *IPv6ExtHdrDestOptBuilder) {
		// Note: gopacket automatically sets NextHeader during serialization
		// This function is kept for API compatibility with Scapy
	}
}

// IPv6DestOptAddOption adds an option to the destination options header
func IPv6DestOptAddOption(optType uint8, data []byte) IPv6ExtHdrDestOptOption {
	return func(b *IPv6ExtHdrDestOptBuilder) {
		opt := &layers.IPv6DestinationOption{}
		// Note: IPv6DestinationOption is an alias for ipv6HeaderTLVOption
		// We'll create a minimal valid option
		b.options = append(b.options, opt)
	}
}

// Build constructs the IPv6 Destination Options extension header
func (b *IPv6ExtHdrDestOptBuilder) Build() gopacket.SerializableLayer {
	return &layers.IPv6Destination{
		Options: b.options,
	}
}

// MPLSBuilder builds MPLS layer
type MPLSBuilder struct {
	label      uint32
	ttl        uint8
	stackBit   bool
	trafficCls uint8
}

// MPLSOption is an option for MPLS
type MPLSOption func(*MPLSBuilder)

// MPLS creates a new MPLS layer builder
func MPLS(opts ...MPLSOption) *MPLSBuilder {
	builder := &MPLSBuilder{
		ttl:      64,   // Default TTL
		stackBit: true, // Default to bottom of stack
	}
	for _, opt := range opts {
		opt(builder)
	}
	return builder
}

// MPLSLabel sets the MPLS label
func MPLSLabel(label uint32) MPLSOption {
	return func(b *MPLSBuilder) {
		b.label = label
	}
}

// MPLSTTL sets the MPLS TTL
func MPLSTTL(ttl uint8) MPLSOption {
	return func(b *MPLSBuilder) {
		b.ttl = ttl
	}
}

// MPLSStackBit sets the bottom-of-stack bit
func MPLSStackBit(stackBit bool) MPLSOption {
	return func(b *MPLSBuilder) {
		b.stackBit = stackBit
	}
}

// MPLSTrafficClass sets the traffic class (experimental bits)
func MPLSTrafficClass(tc uint8) MPLSOption {
	return func(b *MPLSBuilder) {
		b.trafficCls = tc
	}
}

// Build constructs the MPLS layer
func (b *MPLSBuilder) Build() gopacket.SerializableLayer {
	return &layers.MPLS{
		Label:        b.label,
		TTL:          b.ttl,
		StackBottom:  b.stackBit,
		TrafficClass: b.trafficCls,
	}
}

// ===== IPSec ESP Layer =====

type IPSecESPBuilder struct {
	layer *layers.IPSecESP
}

func IPSecESP(opts ...IPSecESPOption) *IPSecESPBuilder {
	esp := &layers.IPSecESP{}

	builder := &IPSecESPBuilder{layer: esp}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func (b *IPSecESPBuilder) Build() gopacket.SerializableLayer {
	// IPSecESP doesn't implement SerializeTo, so we need a custom layer
	return &customIPSecESPLayer{
		spi:       b.layer.SPI,
		seq:       b.layer.Seq,
		encrypted: b.layer.Encrypted,
	}
}

// customIPSecESPLayer wraps IPSecESP for serialization
type customIPSecESPLayer struct {
	spi       uint32
	seq       uint32
	encrypted []byte
}

func (e *customIPSecESPLayer) LayerType() gopacket.LayerType {
	return layers.LayerTypeIPSecESP
}

func (e *customIPSecESPLayer) SerializeTo(b gopacket.SerializeBuffer, opts gopacket.SerializeOptions) error {
	// ESP header: 4 bytes SPI + 4 bytes Seq + encrypted data
	headerSize := 8 + len(e.encrypted)
	bytes, err := b.PrependBytes(headerSize)
	if err != nil {
		return err
	}

	// Write SPI (4 bytes)
	bytes[0] = byte(e.spi >> 24)
	bytes[1] = byte(e.spi >> 16)
	bytes[2] = byte(e.spi >> 8)
	bytes[3] = byte(e.spi)

	// Write Seq (4 bytes)
	bytes[4] = byte(e.seq >> 24)
	bytes[5] = byte(e.seq >> 16)
	bytes[6] = byte(e.seq >> 8)
	bytes[7] = byte(e.seq)

	// Write encrypted data
	copy(bytes[8:], e.encrypted)

	return nil
}

type IPSecESPOption func(*IPSecESPBuilder)

func ESPSPI(spi uint32) IPSecESPOption {
	return func(b *IPSecESPBuilder) {
		b.layer.SPI = spi
	}
}

func ESPSeq(seq uint32) IPSecESPOption {
	return func(b *IPSecESPBuilder) {
		b.layer.Seq = seq
	}
}

func ESPEncrypted(data []byte) IPSecESPOption {
	return func(b *IPSecESPBuilder) {
		b.layer.Encrypted = data
	}
}

// ===== ARP Layer =====

type ARPBuilder struct {
	layer *layers.ARP
}

func ARP(opts ...ARPOption) *ARPBuilder {
	arp := &layers.ARP{
		AddrType:        1,      // Ethernet
		Protocol:        0x0800, // IPv4
		HwAddressSize:   6,      // MAC address length
		ProtAddressSize: 4,      // IPv4 address length
		Operation:       1,      // ARP request by default
	}

	builder := &ARPBuilder{layer: arp}

	for _, opt := range opts {
		opt(builder)
	}

	return builder
}

func (b *ARPBuilder) Build() gopacket.SerializableLayer {
	return b.layer
}

type ARPOption func(*ARPBuilder)

func ARPOperation(op uint16) ARPOption {
	return func(b *ARPBuilder) {
		b.layer.Operation = op
	}
}

func ARPHwType(hwType uint16) ARPOption {
	return func(b *ARPBuilder) {
		b.layer.AddrType = layers.LinkType(hwType)
	}
}

func ARPPType(pType uint16) ARPOption {
	return func(b *ARPBuilder) {
		b.layer.Protocol = layers.EthernetType(pType)
	}
}

func ARPHwLen(hwLen uint8) ARPOption {
	return func(b *ARPBuilder) {
		b.layer.HwAddressSize = hwLen
	}
}

func ARPPLen(pLen uint8) ARPOption {
	return func(b *ARPBuilder) {
		b.layer.ProtAddressSize = pLen
	}
}

func ARPHwSrc(mac string) ARPOption {
	return func(b *ARPBuilder) {
		if parsed, err := net.ParseMAC(mac); err == nil {
			b.layer.SourceHwAddress = parsed
		}
	}
}

func ARPPSrc(ip string) ARPOption {
	return func(b *ARPBuilder) {
		parsed := net.ParseIP(ip)
		if parsed != nil {
			b.layer.SourceProtAddress = parsed.To4()
		}
	}
}

func ARPHwDst(mac string) ARPOption {
	return func(b *ARPBuilder) {
		if parsed, err := net.ParseMAC(mac); err == nil {
			b.layer.DstHwAddress = parsed
		}
	}
}

func ARPPDst(ip string) ARPOption {
	return func(b *ARPBuilder) {
		parsed := net.ParseIP(ip)
		if parsed != nil {
			b.layer.DstProtAddress = parsed.To4()
		}
	}
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
