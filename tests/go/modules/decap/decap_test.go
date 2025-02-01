package decap_test

import (
	"net"
	"net/netip"
	"testing"

	"tests/common"

	"github.com/google/go-cmp/cmp"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
)

func Unwrap[T any](t T, e error) T {
	if e != nil {
		panic(e)
	}
	return t
}

func TestDecap_Default(t *testing.T) {

	eth := layers.Ethernet{
		SrcMAC:       Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	vlan := layers.Dot1Q{
		VLANIdentifier: 100,
		Type:           layers.EthernetTypeIPv6,
	}
	ip6tun := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolIPv4,
		HopLimit:   64,
		SrcIP:      net.IPv6zero,
		DstIP:      net.ParseIP("1:2:3:4::abcd"),
	}
	ip4 := layers.IPv4{
		Version:  4,
		Id:       1,
		TTL:      64,
		Protocol: 1,
		SrcIP:    net.IPv4zero,
		DstIP:    net.ParseIP("1.1.0.0"),
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(
			layers.ICMPv4TypeEchoRequest, layers.ICMPv4CodeNet,
		),
	}

	pkt := common.LayersToPacket(t, &eth, &vlan, &ip6tun, &ip4, &icmp)
	t.Log(pkt.Dump())

	prefixes := []netip.Prefix{
		Unwrap(netip.ParsePrefix("1:2:3:4::abcd/128")),
	}
	m := decapModuleConfig(prefixes)

	result := decapHandlePackets(&m, [][]byte{pkt.Data()})
	require.NotEmpty(t, result.Output)
	resultPkt := common.ParseEtherPacket(result.Output[0])

	vlan.Type = layers.EthernetTypeIPv4
	expectedPkt := common.LayersToPacket(t, &eth, &vlan, &ip4, &icmp)

	diff := cmp.Diff(expectedPkt.Layers(), resultPkt.Layers())
	require.Empty(t, diff)
}
