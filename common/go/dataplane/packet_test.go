package dataplane

import (
	"bytes"
	"encoding/binary"
	"net"
	"runtime"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
)

func TestPacket(t *testing.T) {
	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		DstMAC:       net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.IP{192, 168, 1, 10},
		DstIP:    net.IP{192, 168, 1, 20},
	}

	tcp := &layers.TCP{
		SrcPort: layers.TCPPort(12345),
		DstPort: layers.TCPPort(80),
		SYN:     true,
		Seq:     1105024978,
		Window:  14600,
	}
	tcp.SetNetworkLayerForChecksum(ip)

	payload := gopacket.Payload("hello")

	gopacket := xpacket.LayersToPacket(t, eth, ip, tcp, payload)

	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	gopacketData := gopacket.Data()
	pinner.Pin(&gopacketData[0])

	data := PacketData{
		Payload: gopacketData,
	}
	packet, err := NewPacketFromData(data)
	require.NoError(t, err, "failed to create new packet from data")

	packetData := packet.Data()
	assert.Equal(t, data, packetData)
	packet.Free()
}

func genPackets(t *testing.T, pinner *runtime.Pinner, count uint64) []*Packet {
	packets := make([]*Packet, 0, count)

	for idx := range count {
		eth := &layers.Ethernet{
			SrcMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			DstMAC:       net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
			EthernetType: layers.EthernetTypeIPv4,
		}

		ip := &layers.IPv4{
			Version:  4,
			IHL:      5,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    net.IP{192, 168, 1, byte(idx % 255)},
			DstIP:    net.IP{192, 168, 1, byte((idx * 17) % 255)},
		}

		tcp := &layers.TCP{
			SrcPort: layers.TCPPort(12345),
			DstPort: layers.TCPPort(80),
			SYN:     true,
			Seq:     uint32(idx),
			Window:  14600,
		}
		tcp.SetNetworkLayerForChecksum(ip)

		var payload gopacket.SerializableLayer
		if idx%2 == 0 {
			payload = gopacket.Payload("hello")
		} else {
			payload = gopacket.Payload("hello5555")
		}

		data := xpacket.LayersToPacket(t, eth, ip, tcp, payload).Data()
		pinner.Pin(&data[0])

		packet, err := NewPacketFromData(PacketData{
			Payload: data,
		})

		require.NoError(t, err)

		pinner.Pin(packet)

		packets = append(packets, packet)
	}

	pinner.Pin(&packets[0])

	return packets
}

func TestPacketList(t *testing.T) {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	packets := genPackets(t, &pinner, 5)

	packetList := NewPacketList(&pinner, packets)
	defer packetList.Free()

	packet := packetList.First()
	for idx := 0; packet != nil; idx += 1 {
		assert.Equal(t, packet, packets[idx])
		packet = packet.Next()
	}
}

func TestPacketFront(t *testing.T) {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	packets := genPackets(t, &pinner, 2)

	packetList := NewPacketList(&pinner, packets)

	pf := NewPacketFront(&pinner, packetList, nil, nil)
	defer pf.Free()

	assert.Equal(t, pf.InputList(), packetList)
}

// ipProtoTCP is the IP protocol number the transport header records for a
// TCP flow.
const ipProtoTCP uint16 = 6

// transportHeaderUnavailable is the high tag of a transport type whose
// header was not parsed: the packet carries a non-initial fragment, so the
// low byte only keeps the declared protocol.
const transportHeaderUnavailable uint16 = 0x100

// ipProtoNone is the IPv6 No Next Header value: the extension chain ends
// and no transport header follows.
const ipProtoNone uint16 = 59

// ipv6FragmentFrame builds an unpadded Ethernet frame carrying an IPv6
// header, a Fragment extension header with the given next header, More
// Fragments bit and 8-byte-unit offset, and the payload verbatim. No
// serializer padding is added, so a short fragment stays short.
func ipv6FragmentFrame(
	nextHeader byte,
	moreFragments bool,
	offsetUnits uint16,
	payload []byte,
) []byte {
	frame := make([]byte, 14+40+8+len(payload))
	copy(frame[0:6], []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66})
	copy(frame[6:12], []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})
	binary.BigEndian.PutUint16(frame[12:14], 0x86dd)
	frame[14] = 0x60
	binary.BigEndian.PutUint16(frame[18:20], uint16(8+len(payload)))
	frame[20] = 44
	frame[21] = 64
	copy(frame[22:38], net.ParseIP("2001:db8::1").To16())
	copy(frame[38:54], net.ParseIP("2001:db8::2").To16())
	frame[54] = nextHeader
	offsetFlag := offsetUnits << 3
	if moreFragments {
		offsetFlag |= 1
	}
	binary.BigEndian.PutUint16(frame[56:58], offsetFlag)
	copy(frame[62:], payload)
	return frame
}

// ipv4FragmentFrame builds an unpadded Ethernet frame carrying an IPv4 TCP
// header set with the given DF and MF flags and the given 8-byte-unit
// fragment offset, followed by the payload verbatim.
func ipv4FragmentFrame(
	dontFragment, moreFragments bool,
	offsetUnits uint16,
	payload []byte,
) []byte {
	frame := make([]byte, 14+20+len(payload))
	copy(frame[0:6], []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66})
	copy(frame[6:12], []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})
	binary.BigEndian.PutUint16(frame[12:14], 0x0800)
	frame[14] = 0x45
	binary.BigEndian.PutUint16(frame[16:18], uint16(20+len(payload)))
	fragmentField := offsetUnits
	if moreFragments {
		fragmentField |= 1 << 13
	}
	if dontFragment {
		fragmentField |= 1 << 14
	}
	binary.BigEndian.PutUint16(frame[20:22], fragmentField)
	frame[22] = 64
	frame[23] = 6
	copy(frame[26:30], net.IP{10, 0, 0, 1}.To4())
	copy(frame[30:34], net.IP{192, 168, 1, 1}.To4())
	copy(frame[34:], payload)
	return frame
}

// newFragmentPacket builds a packet from a raw frame through the CGO
// construction seam, pinning the frame for the call as the cgo pointer rules
// require.
func newFragmentPacket(t *testing.T, pinner *runtime.Pinner, frame []byte) *Packet {
	t.Helper()

	pinner.Pin(&frame[0])

	packet, err := NewPacketFromData(PacketData{Payload: frame})
	require.NoError(t, err, "the raw fragment frame must parse")
	pinner.Pin(packet)
	t.Cleanup(packet.Free)
	return packet
}

// TestPacket_NonInitialFragmentSegmented verifies that a non-initial
// fragment whose transport-header region crosses a segment boundary parses
// without reading payload as ports: the fragment keeps its declared protocol
// tagged as header-unavailable.
func TestPacket_NonInitialFragmentSegmented(t *testing.T) {
	frame := ipv6FragmentFrame(6, false, 1, make([]byte, 8))
	require.Equal(t, 70, len(frame))

	packet, err := NewPacketFromSegments(
		[][]byte{frame[:64], frame[64:]},
	)
	require.NoError(t, err, "the segmented fragment must parse")
	t.Cleanup(packet.Free)

	fragmented, offset, transportType, _ := packet.fragmentMetadata()
	assert.True(t, fragmented, "the fragment must be flagged")
	assert.Equal(t, uint16(8), offset, "the byte offset must be reported")
	assert.Equal(t, ipProtoTCP|transportHeaderUnavailable, transportType,
		"the declared protocol must be kept and tagged unavailable")
}

// TestPacket_InitialAndAtomicFragmentsKeepTransport verifies that fragment
// metadata alone does not remove the transport header: an initial fragment
// (More Fragments set, zero offset) and an atomic fragment (both cleared)
// keep a parsed TCP header.
func TestPacket_InitialAndAtomicFragmentsKeepTransport(t *testing.T) {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	payload := bytes.Repeat([]byte{0x42}, 20)

	initial := newFragmentPacket(
		t, &pinner, ipv6FragmentFrame(6, true, 0, payload),
	)

	fragmented, offset, transportType, _ := initial.fragmentMetadata()
	assert.True(t, fragmented, "the initial fragment must be flagged")
	assert.Equal(t, uint16(0), offset)
	assert.Equal(t, ipProtoTCP, transportType,
		"an initial fragment keeps its transport header")

	atomic := newFragmentPacket(
		t, &pinner, ipv6FragmentFrame(6, false, 0, payload),
	)

	fragmented, offset, transportType, _ = atomic.fragmentMetadata()
	assert.False(t, fragmented, "an atomic fragment is not flagged")
	assert.Equal(t, uint16(0), offset)
	assert.Equal(t, ipProtoTCP, transportType,
		"an atomic fragment keeps its transport header")

	dfOnly := newFragmentPacket(
		t, &pinner, ipv4FragmentFrame(true, false, 0, payload),
	)

	fragmented, _, transportType, _ = dfOnly.fragmentMetadata()
	assert.False(t, fragmented, "a DF-only packet is not flagged")
	assert.Equal(t, ipProtoTCP, transportType)
}

// TestPacket_NonInitialFragmentHashPayloadIndependent verifies that the
// packet hash of a non-initial fragment is the address-only flow hash: it is
// nonzero, ignores payload bytes so non-initial fragments of one datagram
// hash identically, and follows the flow addresses.
func TestPacket_NonInitialFragmentHashPayloadIndependent(t *testing.T) {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	first := newFragmentPacket(
		t, &pinner, ipv4FragmentFrame(false, false, 1, bytes.Repeat([]byte{0x00}, 8)),
	)
	second := newFragmentPacket(
		t, &pinner, ipv4FragmentFrame(false, false, 1, bytes.Repeat([]byte{0xFF}, 8)),
	)

	firstFragmented, _, firstTransport, firstHash := first.fragmentMetadata()
	assert.True(t, firstFragmented, "the fragment must be flagged")
	assert.Equal(t, ipProtoTCP|transportHeaderUnavailable, firstTransport,
		"the declared protocol must be kept and tagged unavailable")
	assert.NotZero(t, firstHash,
		"a non-initial fragment keeps the address-only flow hash")

	_, _, _, secondHash := second.fragmentMetadata()
	assert.Equal(t, firstHash, secondHash,
		"fragment hash must not depend on payload bytes")

	readdressed := ipv4FragmentFrame(false, false, 1, bytes.Repeat([]byte{0x00}, 8))
	readdressed[27] = 1 // moves the source from 10.0.0.1 to 10.1.0.1
	third := newFragmentPacket(t, &pinner, readdressed)
	_, _, _, thirdHash := third.fragmentMetadata()
	assert.NotEqual(t, firstHash, thirdHash,
		"fragment hash must depend on the flow addresses")
}

// ipv6ExtChainFrame builds an unpadded Ethernet frame carrying an IPv6
// header and a Hop-by-Hop extension chain laid out the way the parser
// walks it: stubs[0] sits at the payload start and every next stub sits
// (1 + extension length) * 8 bytes after the previous one. The frame
// holds exactly the declared payload length, and the bytes between stubs
// stay zeroed padding that the walk never reads.
func ipv6ExtChainFrame(t *testing.T, declaredPayloadLen int, stubs [][2]byte) []byte {
	t.Helper()

	frame := make([]byte, 14+40+declaredPayloadLen)
	copy(frame[0:6], []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66})
	copy(frame[6:12], []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})
	binary.BigEndian.PutUint16(frame[12:14], 0x86dd)
	frame[14] = 0x60
	binary.BigEndian.PutUint16(frame[18:20], uint16(declaredPayloadLen))
	frame[20] = 0 // IPPROTO_HOPOPTS starts the chain
	frame[21] = 64
	copy(frame[22:38], net.ParseIP("2001:db8::1").To16())
	copy(frame[38:54], net.ParseIP("2001:db8::2").To16())

	offset := 54
	for _, stub := range stubs {
		require.Less(t, offset+1, len(frame),
			"each stub must fit the frame: the walk reads it before advancing")
		frame[offset] = stub[0]
		frame[offset+1] = stub[1]
		offset += (1 + int(stub[1])) * 8
	}
	return frame
}

// TestPacket_IPv6ExtChainAdvanceOverflowRejected verifies that a
// Hop-by-Hop chain whose advances sum past 65535 bytes is rejected in
// bounded time: the walk cursor cannot wrap back below the end of the
// declared payload, so parsing terminates with an error instead of
// walking the chain again from the wrapped offset.
func TestPacket_IPv6ExtChainAdvanceOverflowRejected(t *testing.T) {
	stubs := make([][2]byte, 32)
	for idx := range stubs {
		stubs[idx] = [2]byte{0, 255} // next Hop-by-Hop, 2048-byte advance
	}
	frame := ipv6ExtChainFrame(t, 65480, stubs)
	require.Equal(t, 65534, len(frame))

	pinner := runtime.Pinner{}
	defer pinner.Unpin()
	pinner.Pin(&frame[0])

	_, err := NewPacketFromData(PacketData{Payload: frame})
	require.Error(t, err,
		"an extension chain advancing past 65535 bytes must be rejected")
}

// TestPacket_IPv6ExtChainCrossingSegmentRejected verifies that a
// Hop-by-Hop chain advancing past the first segment is rejected instead
// of reading header bytes beyond it: the walk dereferences
// segment-relative pointers, so a chained frame cannot drive reads into
// memory the segment does not hold.
func TestPacket_IPv6ExtChainCrossingSegmentRejected(t *testing.T) {
	frame := ipv6ExtChainFrame(t, 300, [][2]byte{{0, 31}}) // 256-byte advance
	require.Equal(t, 354, len(frame))

	_, err := NewPacketFromSegments(
		[][]byte{frame[:200], frame[200:]},
	)
	require.Error(t, err,
		"a chain advancing past the first segment must be rejected")
}

// TestPacket_IPv6ExtAdvancePastDeclaredPayloadRejected verifies that an
// extension header whose advance runs past the declared payload length
// is rejected: the walked region can never exceed the payload the frame
// declares.
func TestPacket_IPv6ExtAdvancePastDeclaredPayloadRejected(t *testing.T) {
	frame := ipv6ExtChainFrame(t, 16, [][2]byte{{0, 255}})

	pinner := runtime.Pinner{}
	defer pinner.Unpin()
	pinner.Pin(&frame[0])

	_, err := NewPacketFromData(PacketData{Payload: frame})
	require.Error(t, err,
		"an advance past the declared payload length must be rejected")
}

// TestPacket_IPv6ShortExtChainAccepted verifies that a Hop-by-Hop chain
// ending exactly at the declared payload length still parses: the final
// next header is carried as the transport type.
func TestPacket_IPv6ShortExtChainAccepted(t *testing.T) {
	frame := ipv6ExtChainFrame(t, 16, [][2]byte{{0, 0}, {59, 0}})

	pinner := runtime.Pinner{}
	defer pinner.Unpin()
	pinner.Pin(&frame[0])

	packet, err := NewPacketFromData(PacketData{Payload: frame})
	require.NoError(t, err, "a short valid extension chain must parse")
	t.Cleanup(packet.Free)

	_, _, transportType, _ := packet.fragmentMetadata()
	assert.Equal(t, ipProtoNone, transportType,
		"the chain's terminal next header is the transport type")
}
