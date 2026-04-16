package utils

import (
	"cmp"
	"fmt"
	"math/rand"
	"net"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// MakeTCPPacketLayers creates a TCP packet with the specified parameters.
// Supports both IPv4 and IPv6.
func MakeTCPPacketLayers(
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
	tcp *layers.TCP,
) []gopacket.SerializableLayer {
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
	_ = tcp.SetNetworkLayerForChecksum(ip)

	payload := []byte("BALANCER TEST PAYLOAD 12345678910")
	return []gopacket.SerializableLayer{
		eth,
		ip.(gopacket.SerializableLayer),
		tcp,
		gopacket.Payload(payload),
	}
}

// MakeUDPPacketLayers creates a UDP packet with the specified parameters.
// Supports both IPv4 and IPv6.
func MakeUDPPacketLayers(
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
) []gopacket.SerializableLayer {
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
	_ = udp.SetNetworkLayerForChecksum(ip)

	payload := []byte("BALANCER TEST PAYLOAD 12345678910")
	return []gopacket.SerializableLayer{
		eth,
		ip.(gopacket.SerializableLayer),
		udp,
		gopacket.Payload(payload),
	}
}

// MakePacketLayers creates TCP or UDP packet layers.
// If tcp is nil, creates a UDP packet; otherwise creates a TCP packet.
func MakePacketLayers(
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
	tcp *layers.TCP,
) []gopacket.SerializableLayer {
	if tcp == nil {
		return MakeUDPPacketLayers(srcIP, srcPort, dstIP, dstPort)
	}
	return MakeTCPPacketLayers(srcIP, srcPort, dstIP, dstPort, tcp)
}

type PacketInfo struct {
	VsID       VsID
	ClientAddr netip.Addr
	ClientPort uint16
	RealID     RealID

	// Might be nil.
	Packet *framework.PacketInfo
}

func (pkt *PacketInfo) Compare(other *PacketInfo) int {
	if cmp := pkt.VsID.Compare(&other.VsID); cmp != 0 {
		return cmp
	}
	if cmp := pkt.ClientAddr.Compare(other.ClientAddr); cmp != 0 {
		return cmp
	}
	if cmp := cmp.Compare(pkt.ClientPort, other.ClientPort); cmp != 0 {
		return cmp
	}
	return pkt.RealID.Compare(&other.RealID)
}

func (pkt *PacketInfo) String() string {
	addrStr := pkt.ClientAddr.String()
	if pkt.ClientAddr.Is6() {
		addrStr = fmt.Sprintf("[%s]", addrStr)
	}
	return fmt.Sprintf("[%s -> %s -> %s]", addrStr, pkt.VsID.String(), pkt.RealID.String())
}

func PacketInfoFromSessionPb(s *balancerpb.Session) (PacketInfo, error) {
	addr, ok := netip.AddrFromSlice(s.ClientAddr)
	if !ok {
		return PacketInfo{}, fmt.Errorf("invalid client address: %v", s.ClientAddr)
	}
	if s.ClientPort > 65535 {
		return PacketInfo{}, fmt.Errorf("invalid client port: %d", s.ClientPort)
	}
	port := uint16(s.ClientPort)
	return PacketInfo{
		VsID:       VsIDFromPb(s.VsId),
		ClientAddr: addr,
		ClientPort: port,
		RealID:     RealIDFromPb(s.RealId),
	}, nil
}

func SendAndValidateTCP(
	t *testing.T,
	ts *TestSetup,
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
	tcp *layers.TCP,
) PacketInfo {
	t.Helper()

	pktLayers := MakeTCPPacketLayers(srcIP, srcPort, dstIP, dstPort, tcp)
	pkt := xpacket.LayersToPacket(t, pktLayers...)

	result, err := ts.Mock.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "expected 1 output packet")
	require.Empty(t, result.Drop, "expected no drops")

	return ValidatePacket(t, ts.Config, pkt, result.Output[0])
}

func SendAndValidateUDP(
	t *testing.T,
	ts *TestSetup,
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
) PacketInfo {
	t.Helper()

	pktLayers := MakeUDPPacketLayers(srcIP, srcPort, dstIP, dstPort)
	pkt := xpacket.LayersToPacket(t, pktLayers...)

	result, err := ts.Mock.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "expected 1 output packet")
	require.Empty(t, result.Drop, "expected no drops")

	return ValidatePacket(t, ts.Config, pkt, result.Output[0])
}

func SendAndValidate(
	t *testing.T,
	ts *TestSetup,
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
	tcp *layers.TCP,
) PacketInfo {
	t.Helper()
	if tcp == nil {
		return SendAndValidateUDP(t, ts, srcIP, srcPort, dstIP, dstPort)
	}
	return SendAndValidateTCP(t, ts, srcIP, srcPort, dstIP, dstPort, tcp)
}

func SendAndValidateRandomSrcPorts(
	t *testing.T,
	ts *TestSetup,
	srcIP netip.Addr,
	dstIP netip.Addr,
	dstPort uint16,
	tcp *layers.TCP,
	count int,
) []PacketInfo {
	t.Helper()

	inputPackets := make([]gopacket.Packet, 0, count)

	for range count {
		var layers []gopacket.SerializableLayer
		if tcp == nil {
			layers = MakeUDPPacketLayers(srcIP, uint16(rand.Uint32()%60000+1024), dstIP, dstPort)
		} else {
			layers = MakeTCPPacketLayers(srcIP, uint16(rand.Uint32()%60000+1024), dstIP, dstPort, tcp)
		}
		inputPackets = append(inputPackets, xpacket.LayersToPacket(t, layers...))
	}

	outputPackets := make([]PacketInfo, 0, count)
	result, err := ts.Mock.HandlePackets(inputPackets...)
	require.NoError(t, err)
	require.Len(t, result.Output, count, "expected %d output packets", count)
	require.Empty(t, result.Drop, "expected no drops")

	for i, output := range result.Output {
		outputPackets = append(outputPackets, ValidatePacket(t, ts.Config, inputPackets[i], output))
	}

	return outputPackets
}
