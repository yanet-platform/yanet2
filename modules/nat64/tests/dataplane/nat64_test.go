package nat64_test

import (
	"encoding/binary"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/testutils"
)

const (
	ipProtoHopopts  = 0
	ipProtoFragment = 44
	ipProtoNone     = 59
	ipProtoICMPv6   = 58
	ipProtoUDP      = 17
	ipProtoTCP      = 6

	icmpv6DstUnreach        = 1
	icmpv6DstUnreachNoRoute = 0
	icmpv6EchoRequest       = 128

	etherTypeIPv6 = 0x86dd
)

// nat64Prefix and nat64Mappings mirror config_data from the C regression
// suite: an RFC 3849 documentation prefix with RFC 5737 TEST-NET-2 addresses
// mapped behind it.
var (
	nat64Prefix = [12]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0}

	nat64Mappings = []nat64Mapping{
		{IP4: ip4(198, 51, 100, 1), IP6: ip6DocPrefix(4)},
		{IP4: ip4(198, 51, 100, 2), IP6: ip6DocPrefix(3)},
		{IP4: ip4(198, 51, 100, 3), IP6: ip6DocPrefix(2)},
		{IP4: ip4(198, 51, 100, 4), IP6: ip6DocPrefix(1)},
		{IP4: ip4(198, 51, 100, 5), IP6: ip6DocPrefix(8)},
		{IP4: ip4(198, 51, 100, 6), IP6: ip6DocPrefix(7)},
		{IP4: ip4(198, 51, 100, 7), IP6: ip6DocPrefix(6)},
		{IP4: ip4(198, 51, 100, 8), IP6: ip6DocPrefix(5)},
	}

	// outerIP4 is the external address used as the NAT64-side destination
	// of the outer ICMPv6 error, per RFC 5737 TEST-NET-1.
	outerIP4 = [4]byte{192, 0, 2, 34}
)

// ip4 packs four octets into the network-byte-order in-memory representation
// the mapping table's IP4 field expects, matching RTE_BE32(RTE_IPV4(...)) on
// the host running the test.
func ip4(a, b, c, d byte) uint32 {
	return binary.NativeEndian.Uint32([]byte{a, b, c, d})
}

// ip6DocPrefix builds a 2001:db8::/96 address with the given last 32-bit
// segment, matching config_data's mapping table.
func ip6DocPrefix(last uint32) [16]byte {
	var addr [16]byte
	addr[0], addr[1], addr[2], addr[3] = 0x20, 0x01, 0x0d, 0xb8
	binary.BigEndian.PutUint32(addr[12:], last)
	return addr
}

// outerDstIP6 builds the well-known NAT64 destination for the outer ICMPv6
// error: nat64Prefix with outerIP4 embedded in the last 4 bytes.
func outerDstIP6() [16]byte {
	var addr [16]byte
	copy(addr[:], nat64Prefix[:])
	copy(addr[12:], outerIP4[:])
	return addr
}

// putEther appends a 14-byte Ethernet header addressed like the C fixtures:
// broadcast destination, a fixed locally-administered source, IPv6 ethertype.
func putEther(buf []byte) []byte {
	buf = append(buf, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	buf = append(buf, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00)
	return binary.BigEndian.AppendUint16(buf, etherTypeIPv6)
}

// putIPv6 appends a 40-byte IPv6 header.
func putIPv6(buf []byte, payloadLen uint16, proto uint8, src, dst [16]byte) []byte {
	buf = binary.BigEndian.AppendUint32(buf, 0x60000000)
	buf = binary.BigEndian.AppendUint16(buf, payloadLen)
	buf = append(buf, proto, 64)
	buf = append(buf, src[:]...)
	buf = append(buf, dst[:]...)
	return buf
}

// putICMPv6Header appends an 8-byte ICMPv6 header with a zero checksum and a
// zero reserved/data field: parse_packet never validates the checksum.
func putICMPv6Header(buf []byte, icmpType, code uint8) []byte {
	buf = append(buf, icmpType, code)
	buf = binary.BigEndian.AppendUint16(buf, 0)  // checksum
	return binary.BigEndian.AppendUint32(buf, 0) // reserved/dataun
}

func putICMPv6Error(buf, embedded []byte) []byte {
	buf = putEther(buf)
	buf = putIPv6(buf, uint16(8+len(embedded)), ipProtoICMPv6, nat64Mappings[0].IP6, outerDstIP6())
	buf = putICMPv6Header(buf, icmpv6DstUnreach, icmpv6DstUnreachNoRoute)
	return append(buf, embedded...)
}

// TestICMPv6EmbeddedFragTruncatedDropped ports
// test_nat64_icmpv6_embedded_frag_oob: an ICMPv6 error whose embedded IPv6
// header claims a Fragment header (payload_len=1) but the packet ends one
// byte in. The module must bound the walk by the mbuf's actual length, not
// the header's self-reported payload_len, and drop it as malformed.
func TestICMPv6EmbeddedFragTruncatedDropped(t *testing.T) {
	memCtx := testutils.NewMemoryContext(t.Name(), 4*datasize.MB)
	defer memCtx.Free()
	mc := nat64ModuleConfig(nat64Prefix, nat64Mappings, memCtx)

	// Embedded original packet: an IPv6 header announcing a Fragment
	// header, followed by exactly one byte of it.
	embedded := putIPv6(nil, 1, ipProtoFragment, nat64Mappings[0].IP6, nat64Mappings[1].IP6)
	embedded = append(embedded, ipProtoNone)
	require.Len(t, embedded, 41)

	pkt := putEther(nil)
	pkt = putIPv6(pkt, uint16(8+len(embedded)), ipProtoICMPv6, nat64Mappings[0].IP6, outerDstIP6())
	pkt = putICMPv6Header(pkt, icmpv6DstUnreach, icmpv6DstUnreachNoRoute)
	pkt = append(pkt, embedded...)

	result := nat64HandlePackets(t, mc, [][]byte{pkt})

	require.Empty(t, result.Output, "truncated embedded Fragment header must not be forwarded")
	require.Len(t, result.Drop, 1, "expected a drop for the truncated embedded Fragment header")
	require.EqualValues(t, 1, nat64MalformedPackets(mc))
}

// TestICMPv6EmbeddedExtTruncatedCountedMalformed ports
// test_nat64_icmpv6_embedded_ext_oob: same shape as the Fragment case, but
// the embedded IPv6 header announces a Hop-by-Hop Options header
// (payload_len=1) truncated to a single byte, short of the two bytes needed
// to read the extension's own length field. Both the pre-fix and fixed code
// drop this packet, so the drop alone does not discriminate. The malformed
// counter does, since it is reachable only through the new bounds guard
// rejecting the packet before the out-of-bounds read.
func TestICMPv6EmbeddedExtTruncatedCountedMalformed(t *testing.T) {
	memCtx := testutils.NewMemoryContext(t.Name(), 4*datasize.MB)
	defer memCtx.Free()
	mc := nat64ModuleConfig(nat64Prefix, nat64Mappings, memCtx)

	embedded := putIPv6(nil, 1, ipProtoHopopts, nat64Mappings[0].IP6, nat64Mappings[1].IP6)
	embedded = append(embedded, 0)
	require.Len(t, embedded, 41)

	pkt := putEther(nil)
	pkt = putIPv6(pkt, uint16(8+len(embedded)), ipProtoICMPv6, nat64Mappings[0].IP6, outerDstIP6())
	pkt = putICMPv6Header(pkt, icmpv6DstUnreach, icmpv6DstUnreachNoRoute)
	pkt = append(pkt, embedded...)

	result := nat64HandlePackets(t, mc, [][]byte{pkt})

	require.Empty(t, result.Output, "truncated embedded extension header must not be forwarded")
	require.Len(t, result.Drop, 1, "expected a drop for the truncated embedded extension header")
	require.EqualValues(t, 1, nat64MalformedPackets(mc))
}

func TestICMPv6EmbeddedICMPv6TruncatedCountedMalformed(t *testing.T) {
	memCtx := testutils.NewMemoryContext(t.Name(), 4*datasize.MB)
	defer memCtx.Free()
	mc := nat64ModuleConfig(nat64Prefix, nat64Mappings, memCtx)

	// A complete Hop-by-Hop header reaches the embedded ICMPv6 guard.
	embedded := putIPv6(nil, 12, ipProtoHopopts, nat64Mappings[0].IP6, nat64Mappings[1].IP6)
	embedded = append(embedded, ipProtoICMPv6, 0, 0, 0, 0, 0, 0, 0)
	embedded = append(embedded, icmpv6EchoRequest, 0, 0, 0)
	require.Len(t, embedded, 52)

	packet := putICMPv6Error(nil, embedded)
	result := nat64HandlePackets(t, mc, [][]byte{packet})

	require.Empty(t, result.Output, "truncated embedded ICMPv6 header must not be forwarded")
	require.Len(t, result.Drop, 1, "expected a drop for the truncated embedded ICMPv6 header")
	require.EqualValues(t, 1, nat64MalformedPackets(mc))
}

func TestICMPv6EmbeddedUDPTruncatedCountedMalformed(t *testing.T) {
	memCtx := testutils.NewMemoryContext(t.Name(), 4*datasize.MB)
	defer memCtx.Free()
	mc := nat64ModuleConfig(nat64Prefix, nat64Mappings, memCtx)

	embedded := putIPv6(nil, 4, ipProtoUDP, nat64Mappings[0].IP6, nat64Mappings[1].IP6)
	embedded = append(embedded, 0, 0, 0, 0)
	require.Len(t, embedded, 44)

	packet := putICMPv6Error(nil, embedded)
	result := nat64HandlePackets(t, mc, [][]byte{packet})

	require.Empty(t, result.Output, "truncated embedded UDP header must not be forwarded")
	require.Len(t, result.Drop, 1, "expected a drop for the truncated embedded UDP header")
	require.EqualValues(t, 1, nat64MalformedPackets(mc))
}

func TestICMPv6EmbeddedTCPTruncatedCountedMalformed(t *testing.T) {
	memCtx := testutils.NewMemoryContext(t.Name(), 4*datasize.MB)
	defer memCtx.Free()
	mc := nat64ModuleConfig(nat64Prefix, nat64Mappings, memCtx)

	embedded := putIPv6(nil, 12, ipProtoTCP, nat64Mappings[0].IP6, nat64Mappings[1].IP6)
	embedded = append(embedded, make([]byte, 12)...)
	require.Len(t, embedded, 52)

	packet := putICMPv6Error(nil, embedded)
	result := nat64HandlePackets(t, mc, [][]byte{packet})

	require.Empty(t, result.Output, "truncated embedded TCP header must not be forwarded")
	require.Len(t, result.Drop, 1, "expected a drop for the truncated embedded TCP header")
	require.EqualValues(t, 1, nat64MalformedPackets(mc))
}

// TestICMPv6EmbeddedExtFragTranslated ports
// test_nat64_icmpv6_embedded_ext_frag_translated: positive control for the
// existing embedded Fragment-header and extension-header truncation cases. A
// well-formed embedded IPv6 packet with a Hop-by-Hop Options header followed
// by a Fragment header must translate
// cleanly, and the Fragment header's identification and offset/flags must
// survive into the translated embedded IPv4 header untouched by the
// in-place IPv6-to-IPv4 rewrite that follows it.
func TestICMPv6EmbeddedExtFragTranslated(t *testing.T) {
	memCtx := testutils.NewMemoryContext(t.Name(), 4*datasize.MB)
	defer memCtx.Free()
	mc := nat64ModuleConfig(nat64Prefix, nat64Mappings, memCtx)

	// Hop-by-Hop Options: 2-byte header (next=Fragment, ext_len=0) + 6
	// bytes of options, an 8-byte extension per (1+ext_len)*8.
	hopopts := []byte{ipProtoFragment, 0, 0, 0, 0, 0, 0, 0}

	// Fragment header: next=ICMPv6, reserved=0, offset_flag=(2<<3)|MF,
	// identification=0x12345678.
	fragment := []byte{ipProtoICMPv6, 0}
	fragment = binary.BigEndian.AppendUint16(fragment, (2<<3)|1)
	fragment = binary.BigEndian.AppendUint32(fragment, 0x12345678)

	embeddedICMPv6 := putICMPv6Header(nil, icmpv6EchoRequest, 0)

	embeddedPayload := append(append([]byte{}, hopopts...), fragment...)
	embeddedPayload = append(embeddedPayload, embeddedICMPv6...)

	embedded := putIPv6(nil, uint16(len(embeddedPayload)), ipProtoHopopts, nat64Mappings[0].IP6, nat64Mappings[1].IP6)
	embedded = append(embedded, embeddedPayload...)

	pkt := putEther(nil)
	pkt = putIPv6(pkt, uint16(8+len(embedded)), ipProtoICMPv6, nat64Mappings[0].IP6, outerDstIP6())
	pkt = putICMPv6Header(pkt, icmpv6DstUnreach, icmpv6DstUnreachNoRoute)
	pkt = append(pkt, embedded...)

	result := nat64HandlePackets(t, mc, [][]byte{pkt})

	require.Len(t, result.Output, 1, "expected the translated ICMPv6 error to be forwarded")
	require.Empty(t, result.Drop, "well-formed embedded extension and Fragment headers must not drop")
	require.EqualValues(t, 0, nat64MalformedPackets(mc))

	out := result.Output[0]
	require.GreaterOrEqual(t, len(out), 14+20)

	// The outer translated IPv4 header follows the 14-byte Ethernet header;
	// its source and destination are its last 8 bytes.
	outerIPv4 := out[14 : 14+20]
	require.Equal(t, []byte{198, 51, 100, 1}, outerIPv4[12:16], "translated outer IPv4 source is wrong")
	require.Equal(t, outerIP4[:], outerIPv4[16:20], "translated outer IPv4 destination is wrong")

	// The translated embedded IPv4 header follows ether(14) + ipv4(20) +
	// icmp(8) in the output packet.
	const embeddedIPv4Offset = 14 + 20 + 8
	require.GreaterOrEqual(t, len(out), embeddedIPv4Offset+20)

	embeddedIPv4 := out[embeddedIPv4Offset : embeddedIPv4Offset+20]
	packetID := binary.BigEndian.Uint16(embeddedIPv4[4:6])
	fragmentOffset := binary.BigEndian.Uint16(embeddedIPv4[6:8])

	// packetID and fragmentOffset pin the translator's current encoding, not
	// RFC 7915: section 5.1.1 wants the identification's low-order 16 bits
	// (0x5678 here), but the narrowing keeps the high-order half, and the
	// offset is written in bytes rather than the 8-octet units the field
	// defines. Only a fix to that narrowing or unit conversion should change
	// these values.
	require.EqualValues(t, 0x1234, packetID, "translated embedded Fragment identification is wrong")
	require.EqualValues(t, (2<<3)|0x2000, fragmentOffset, "translated embedded Fragment offset is wrong")
}
