package balancer

////////////////////////////////////////////////////////////////////////////////

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/lib"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/module"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

////////////////////////////////////////////////////////////////////////////////

func IpAddr(addr string) netip.Addr {
	return xerror.Unwrap(netip.ParseAddr(addr))
}

func IpPrefix(prefix string) netip.Prefix {
	return xerror.Unwrap(netip.ParsePrefix(prefix))
}

////////////////////////////////////////////////////////////////////////////////

func MakePacketLayers(
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
	tcp *layers.TCP,
) []gopacket.SerializableLayer {
	if tcp == nil {
		return MakeUDPPacket(srcIP, srcPort, dstIP, dstPort)
	} else {
		return MakeTCPPacket(srcIP, srcPort, dstIP, dstPort, tcp)
	}
}

func MakeUDPPacket(
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
) []gopacket.SerializableLayer {

	src := net.IP(srcIP.AsSlice())
	dst := net.IP(dstIP.AsSlice())

	var ip gopacket.NetworkLayer
	ethernetType := layers.EthernetTypeIPv6
	if src.To4() != nil {
		ethernetType = layers.EthernetTypeIPv4
		ip = &layers.IPv4{
			Version:  4,
			IHL:      5,
			TTL:      64,
			Protocol: layers.IPProtocolUDP,
			SrcIP:    src,
			DstIP:    dst,
		}
	} else {
		ip = &layers.IPv6{
			Version:    6,
			NextHeader: layers.IPProtocolUDP,
			HopLimit:   64,
			SrcIP:      src,
			DstIP:      dst,
		}
	}

	eth := &layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: ethernetType,
	}

	udp := &layers.UDP{
		SrcPort: layers.UDPPort(srcPort),
		DstPort: layers.UDPPort(dstPort),
	}
	udp.SetNetworkLayerForChecksum(ip)

	payload := []byte("PING TEST PAYLOAD 1234567890")
	layers := []gopacket.SerializableLayer{
		eth,
		ip.(gopacket.SerializableLayer),
		udp,
		gopacket.Payload(payload),
	}

	return layers
}

func MakeTCPPacket(
	srcIP netip.Addr,
	srcPort uint16,
	dstIP netip.Addr,
	dstPort uint16,
	tcp *layers.TCP,
) []gopacket.SerializableLayer {

	src := net.IP(srcIP.AsSlice())
	dst := net.IP(dstIP.AsSlice())

	var ip gopacket.NetworkLayer
	ethernetType := layers.EthernetTypeIPv6
	if src.To4() != nil {
		ethernetType = layers.EthernetTypeIPv4
		ip = &layers.IPv4{
			Version:  4,
			IHL:      5,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    src,
			DstIP:    dst,
		}
	} else {
		ip = &layers.IPv6{
			Version:    6,
			NextHeader: layers.IPProtocolTCP,
			HopLimit:   64,
			SrcIP:      src,
			DstIP:      dst,
		}
	}

	eth := &layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: ethernetType,
	}

	tcp.SrcPort = layers.TCPPort(srcPort)
	tcp.DstPort = layers.TCPPort(dstPort)
	tcp.SetNetworkLayerForChecksum(ip)

	payload := []byte("BALANCER TEST PAYLOAD 12345678910")
	layers := []gopacket.SerializableLayer{
		eth,
		ip.(gopacket.SerializableLayer),
		tcp,
		gopacket.Payload(payload),
	}

	return layers
}

////////////////////////////////////////////////////////////////////////////////

func CheckPacketsEqual(
	t *testing.T,
	result gopacket.Packet,
	expected gopacket.Packet,
) {
	// Find diff
	diff := cmp.Diff(expected.Layers(), result.Layers(),
		cmpopts.IgnoreUnexported(
			layers.Ethernet{},
			layers.IPv4{},
			layers.IPv6{},
			layers.TCP{},
			layers.UDP{},
		),
	)
	require.Empty(t, diff, "packets don't match")

	// Check payload
	require.Equal(t, expected.ApplicationLayer().Payload(),
		result.ApplicationLayer().Payload(), "payload doesn't match")
}

////////////////////////////////////////////////////////////////////////////////

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

func InsertOrUpdateMSS(
	p gopacket.Packet,
	newMSS uint16,
) (*gopacket.Packet, error) {
	// Decode (assumes Ethernet; adjust if you have raw IP)
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

////////////////////////////////////////////////////////////////////////////////

func ValidatePacket(
	t *testing.T,
	config *module.ModuleConfig,
	originalGoPacket gopacket.Packet,
	resultPacket *framework.PacketInfo,
) {
	t.Helper()
	originalPacket, err := framework.NewPacketParser().
		ParsePacket(originalGoPacket.Data())
	if err != nil {
		t.Errorf("failed to parse packet: %v", err)
		return
	}
	if !resultPacket.IsTunneled {
		t.Error("result packet is not tunneled")
		return
	}

	resultInner := resultPacket.InnerPacket
	if resultInner == nil {
		t.Error("no inner packet")
		return
	}

	assert.Equal(
		t,
		originalPacket.DstIP,
		resultInner.DstIP,
		"encapsulated packet dst ip mismatch",
	)
	assert.Equal(
		t,
		originalPacket.SrcIP,
		resultInner.SrcIP,
		"encapsulated packet src ip mismatch",
	)
	assert.Equal(
		t,
		originalGoPacket.ApplicationLayer().Payload(),
		resultPacket.Payload,
	)

	var originPacketProto layers.IPProtocol
	if originalPacket.IsIPv4 {
		assert.Equal(
			t,
			originalPacket.Protocol,
			resultInner.Protocol,
			"encapsulated packet protocol mismatch",
		)
		originPacketProto = originalPacket.Protocol
	} else {
		assert.Equal(
			t,
			originalPacket.NextHeader,
			resultInner.NextHeader,
			"encapsulated packet protocol mismatch",
		)
		originPacketProto = originalPacket.NextHeader
	}

	// get packet proto

	var packetProto lib.Proto
	if originPacketProto.LayerType() == layers.LayerTypeTCP {
		packetProto = lib.ProtoTcp
	} else if originPacketProto.LayerType() == layers.LayerTypeUDP {
		packetProto = lib.ProtoUdp
	} else {
		t.Errorf("invalid packet protocol: %s", originPacketProto.String())
		return
	}

	for idx := range config.VirtualServices {
		service := &config.VirtualServices[idx]
		if reflect.DeepEqual(
			net.IP(service.Identifier.Ip.AsSlice()),
			originalPacket.DstIP,
		) && (service.Identifier.Port == originalPacket.DstPort || service.Flags.PureL3) && service.Identifier.Proto == packetProto {
			// found service
			if service.Flags.GRE {
				expectedTunnelType := "gre-ip4"
				if service.Identifier.Ip.Is6() {
					expectedTunnelType = "gre-ip6"
				}
				assert.Equal(
					t,
					expectedTunnelType,
					resultPacket.TunnelType,
					"packet tunnel type must be gre",
				)
			}

			if service.Flags.FixMSS {
				originalMSS, err := xpacket.PacketMSS(originalGoPacket)
				hadMSS := err == nil

				packet := gopacket.NewPacket(
					resultPacket.RawData,
					layers.LayerTypeEthernet,
					gopacket.Default,
				)
				resultMSS, err := xpacket.PacketMSS(packet)
				hasMSS := err == nil
				if !hasMSS {
					t.Error("no mss in packet, but fix mss flag is present")
					return
				}
				expectedMSS := uint16(0)
				if hadMSS {
					expectedMSS = min(originalMSS, 1220)
				} else {
					expectedMSS = 536
				}
				assert.Equal(
					t,
					expectedMSS,
					resultMSS,
					"incorrect mss after fix",
				)
			}

			for realIdx := range service.Reals {
				real := &service.Reals[realIdx]
				if reflect.DeepEqual(
					net.IP(real.Identifier.Ip.AsSlice()),
					resultPacket.DstIP,
				) { // found real
					assert.True(t, real.Enabled, "send packet to disabled real")
					// TODO: check src address
					// is correct
					return
				}
			}
			t.Error("not found real which can accept packet sent by balancer")
			t.Log("user packet", originalPacket)
			t.Log("balancer packet", resultPacket)
			break
		}
	}

	t.Error("not found service which could serve packet")
	t.Log("user packet", originalPacket)
	t.Log("balancer packet", resultPacket)
}

////////////////////////////////////////////////////////////////////////////////

func ValidateStateInfo(
	t *testing.T,
	info *lib.BalancerInfo,
	virtualServices []lib.VirtualService,
) {
	t.Helper()
	for vsIdx := range virtualServices {
		vs := &virtualServices[vsIdx]
		summaryActiveSession := uint(0)
		summaryPackets := uint64(0)
		for realIdx := range vs.Reals {
			real := &vs.Reals[realIdx]
			summaryActiveSession += info.RealInfo[real.RegistryIdx].ActiveSessions.Value
			summaryPackets += info.RealInfo[realIdx].Stats.Packets
		}

		vsInfo := info.VsInfo[vs.RegistryIdx]
		assert.Equalf(
			t,
			vsInfo.ActiveSessions.Value,
			summaryActiveSession,
			"summary active sessions mismatch for vs %d",
			vsIdx,
		)
		assert.Equal(
			t,
			vsInfo.Stats.OutgoingPackets,
			summaryPackets,
			"summary outgoing packets mismatch for vs %d",
			vsIdx,
		)
	}
}
