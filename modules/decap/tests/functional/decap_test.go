package decap_test

import (
	"net"
	"net/netip"
	"slices"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	decap "github.com/yanet-platform/yanet2/modules/decap/controlplane"
)

const (
	decapCPSize  = 64 * datasize.MB
	decapDPSize  = 4 * datasize.MB
	decapMemSize = 8 * datasize.MB
)

func setupDecapHarness(t *testing.T) (*dataplaneut.Harness, *ffi.Agent, decap.Backend) {
	t.Helper()

	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(decapCPSize),
		DPMemory:      uint64(decapDPSize),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"decap"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	agent, err := h.SharedMemory().AgentAttach("decap-test", 0, decapMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	backend := decap.NewBackend(agent)
	return h, agent, backend
}

func wireDecapPipeline(t *testing.T, agent *ffi.Agent, configName string) {
	t.Helper()

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: configName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: configName + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "decap", Name: configName},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      configName,
		Functions: []string{configName},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	require.NoError(t, agent.UpdatePlainDevices([]ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}))
}

func applyDecapConfig(
	t *testing.T,
	backend decap.Backend,
	agent *ffi.Agent,
	name string,
	prefixes []netip.Prefix,
) {
	t.Helper()

	handle, err := backend.UpdateModule(name, prefixes)
	require.NoError(t, err)
	t.Cleanup(handle.Free)
	wireDecapPipeline(t, agent, name)
}

func requireSingleOutputEquals(t *testing.T, result *dataplaneut.Result, expected gopacket.Packet) {
	t.Helper()
	require.Len(t, result.Output, 1)
	require.Empty(t, result.Drop)
	actual := xpacket.ParseEtherPacket(result.Output[0].RawData)
	diff := cmp.Diff(expected.Layers(), actual.Layers(), cmpopts.IgnoreUnexported(layers.IPv6{}, layers.ICMPv6{}))
	require.Empty(t, diff)
}

// TestDecap_IPIPAndIPIP6_Match verifies that prefix-matched IPv4 outer packets
// are decapsulated for both IPv4 and IPv6 inner payloads.
func TestDecap_IPIPAndIPIP6_Match(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	vlan := layers.Dot1Q{
		VLANIdentifier: 100,
		Type:           layers.EthernetTypeIPv6,
	}
	ip4 := layers.IPv4{
		Version:  4,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.IPv4zero,
		DstIP:    net.ParseIP("1.1.0.1"),
	}
	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      net.ParseIP("2001:db8::1"),
		DstIP:      net.ParseIP("2001:db8::2"),
	}
	icmp4 := layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}
	icmp6 := layers.ICMPv6{TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0)}
	icmp6.SetNetworkLayerForChecksum(&ip6)

	h, agent, backend := setupDecapHarness(t)
	applyDecapConfig(t, backend, agent, "match4", []netip.Prefix{netip.MustParsePrefix("4.5.6.0/24")})

	ipv4Outer := ip4
	ipv4Outer.DstIP = net.ParseIP("4.5.6.7")
	ipv4Outer.Protocol = layers.IPProtocolIPv4
	ipv4Inner := ip4
	ipv4Inner.DstIP = net.ParseIP("1.1.0.9")
	vlan4 := vlan
	vlan4.Type = layers.EthernetTypeIPv4
	pkt4 := xpacket.LayersToPacket(t, &eth, &vlan4, &ipv4Outer, &ipv4Inner, &icmp4)
	expected4 := xpacket.LayersToPacket(t, &eth, &vlan4, &ipv4Inner, &icmp4)

	res4, err := h.HandlePackets(pkt4)
	require.NoError(t, err)
	requireSingleOutputEquals(t, res4, expected4)

	ipv6Inner := ip6
	ipv6Inner.DstIP = net.ParseIP("2001:db8::77")
	ipv6Inner.NextHeader = layers.IPProtocolICMPv6
	icmp6Inner := icmp6
	icmp6Inner.SetNetworkLayerForChecksum(&ipv6Inner)
	ipv4Outer6 := ip4
	ipv4Outer6.DstIP = net.ParseIP("4.5.6.8")
	ipv4Outer6.Protocol = layers.IPProtocolIPv6
	pkt6 := xpacket.LayersToPacket(t, &eth, &vlan4, &ipv4Outer6, &ipv6Inner, &icmp6Inner)
	vlan6 := vlan
	vlan6.Type = layers.EthernetTypeIPv6
	expected6 := xpacket.LayersToPacket(t, &eth, &vlan6, &ipv6Inner, &icmp6Inner)

	res6, err := h.HandlePackets(pkt6)
	require.NoError(t, err)
	requireSingleOutputEquals(t, res6, expected6)
}

// TestDecap_IP6IPAndIP6IP6_Match_WithAndWithoutVLAN verifies that
// prefix-matched IPv6 outer packets are decapsulated for IPv4 and IPv6 inners
// with correct VLAN handling.
func TestDecap_IP6IPAndIP6IP6_Match_WithAndWithoutVLAN(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	vlan := layers.Dot1Q{
		VLANIdentifier: 100,
		Type:           layers.EthernetTypeIPv6,
	}
	ip4 := layers.IPv4{
		Version:  4,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.IPv4zero,
		DstIP:    net.ParseIP("1.1.0.1"),
	}
	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      net.ParseIP("2001:db8::1"),
		DstIP:      net.ParseIP("2001:db8::2"),
	}
	icmp4 := layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}
	icmp6 := layers.ICMPv6{TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0)}
	icmp6.SetNetworkLayerForChecksum(&ip6)

	h, agent, backend := setupDecapHarness(t)
	applyDecapConfig(t, backend, agent, "match6", []netip.Prefix{netip.MustParsePrefix("1:2:3:4::/64")})

	outer6 := ip6
	outer6.NextHeader = layers.IPProtocolIPv4
	outer6.DstIP = net.ParseIP("1:2:3:4::abcd")
	vlan6 := vlan
	vlan6.Type = layers.EthernetTypeIPv6
	pktVLAN := xpacket.LayersToPacket(t, &eth, &vlan6, &outer6, &ip4, &icmp4)
	vlan4 := vlan
	vlan4.Type = layers.EthernetTypeIPv4
	expectedVLAN := xpacket.LayersToPacket(t, &eth, &vlan4, &ip4, &icmp4)

	resVLAN, err := h.HandlePackets(pktVLAN)
	require.NoError(t, err)
	requireSingleOutputEquals(t, resVLAN, expectedVLAN)

	outer66 := ip6
	outer66.NextHeader = layers.IPProtocolIPv4
	outer66.DstIP = net.ParseIP("1:2:3:4::dcba")
	ethNoVLAN := eth
	ethNoVLAN.EthernetType = layers.EthernetTypeIPv6
	pktNoVLAN := xpacket.LayersToPacket(t, &ethNoVLAN, &outer66, &ip4, &icmp4)
	expectedNoVLANEth := eth
	expectedNoVLANEth.EthernetType = layers.EthernetTypeIPv4
	expectedNoVLAN := xpacket.LayersToPacket(t, &expectedNoVLANEth, &ip4, &icmp4)

	resNoVLAN, err := h.HandlePackets(pktNoVLAN)
	require.NoError(t, err)
	requireSingleOutputEquals(t, resNoVLAN, expectedNoVLAN)

	inner66 := ip6
	inner66.DstIP = net.ParseIP("2001:db8::77")
	inner66.NextHeader = layers.IPProtocolICMPv6
	icmp66 := icmp6
	icmp66.SetNetworkLayerForChecksum(&inner66)

	outer666 := ip6
	outer666.NextHeader = layers.IPProtocolIPv6
	outer666.DstIP = net.ParseIP("1:2:3:4::beef")
	pktVLAN66 := xpacket.LayersToPacket(t, &eth, &vlan6, &outer666, &inner66, &icmp66)
	expectedVLAN66 := xpacket.LayersToPacket(t, &eth, &vlan6, &inner66, &icmp66)

	resVLAN66, err := h.HandlePackets(pktVLAN66)
	require.NoError(t, err)
	requireSingleOutputEquals(t, resVLAN66, expectedVLAN66)
}

// TestDecap_PrefixMiss_UnchangedOutput verifies that packets with non-matching
// outer destination prefixes pass through unchanged without drops.
func TestDecap_PrefixMiss_UnchangedOutput(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	vlan := layers.Dot1Q{
		VLANIdentifier: 100,
		Type:           layers.EthernetTypeIPv6,
	}
	ip4 := layers.IPv4{
		Version:  4,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.IPv4zero,
		DstIP:    net.ParseIP("1.1.0.1"),
	}
	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      net.ParseIP("2001:db8::1"),
		DstIP:      net.ParseIP("2001:db8::2"),
	}
	icmp4 := layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}
	icmp6 := layers.ICMPv6{TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0)}
	icmp6.SetNetworkLayerForChecksum(&ip6)

	h, agent, backend := setupDecapHarness(t)
	applyDecapConfig(t, backend, agent, "miss", []netip.Prefix{
		netip.MustParsePrefix("10.10.10.10/32"),
		netip.MustParsePrefix("2001:db8:ffff::/48"),
	})

	vlan4 := vlan
	vlan4.Type = layers.EthernetTypeIPv4
	outer4 := ip4
	outer4.DstIP = net.ParseIP("4.5.6.7")
	outer4.Protocol = layers.IPProtocolIPv4
	pkt4 := xpacket.LayersToPacket(t, &eth, &vlan4, &outer4, &ip4, &icmp4)

	vlan6 := vlan
	vlan6.Type = layers.EthernetTypeIPv6
	outer6 := ip6
	outer6.DstIP = net.ParseIP("1:2:3:4::abcd")
	outer6.NextHeader = layers.IPProtocolIPv4
	pkt6 := xpacket.LayersToPacket(t, &eth, &vlan6, &outer6, &ip4, &icmp4)

	inner6 := ip6
	inner6.DstIP = net.ParseIP("2001:db8::1234")
	inner6.NextHeader = layers.IPProtocolICMPv6
	icmp6Inner := icmp6
	icmp6Inner.SetNetworkLayerForChecksum(&inner6)
	outer46 := ip4
	outer46.DstIP = net.ParseIP("4.5.6.8")
	outer46.Protocol = layers.IPProtocolIPv6
	pkt46 := xpacket.LayersToPacket(t, &eth, &vlan4, &outer46, &inner6, &icmp6Inner)

	res, err := h.HandlePackets(pkt4, pkt6, pkt46)
	require.NoError(t, err)
	require.Len(t, res.Output, 3)
	require.Empty(t, res.Drop)
	require.Equal(t, pkt4.Data(), res.Output[0].RawData[:len(pkt4.Data())])
	require.Equal(t, pkt6.Data(), res.Output[1].RawData[:len(pkt6.Data())])
	require.Equal(t, pkt46.Data(), res.Output[2].RawData[:len(pkt46.Data())])
}

// TestDecap_OuterFragmentHandling verifies that only non-fragmented outer
// packets are decapsulated while fragmented outers are dropped.
func TestDecap_OuterFragmentHandling(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	vlan := layers.Dot1Q{
		VLANIdentifier: 100,
		Type:           layers.EthernetTypeIPv6,
	}
	ip4 := layers.IPv4{
		Version:  4,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.IPv4zero,
		DstIP:    net.ParseIP("1.1.0.1"),
	}
	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      net.ParseIP("2001:db8::1"),
		DstIP:      net.ParseIP("2001:db8::2"),
	}
	icmp4 := layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}

	h, agent, backend := setupDecapHarness(t)
	applyDecapConfig(t, backend, agent, "frag", []netip.Prefix{
		netip.MustParsePrefix("4.5.6.7/32"),
		netip.MustParsePrefix("1:2:3:4::abcd/128"),
	})

	vlan4 := vlan
	vlan4.Type = layers.EthernetTypeIPv4

	outerDF := ip4
	outerDF.DstIP = net.ParseIP("4.5.6.7")
	outerDF.Protocol = layers.IPProtocolIPv4
	outerDF.Flags = layers.IPv4DontFragment
	dfPkt := xpacket.LayersToPacket(t, &eth, &vlan4, &outerDF, &ip4, &icmp4)

	outerMF := ip4
	outerMF.DstIP = net.ParseIP("4.5.6.7")
	outerMF.Protocol = layers.IPProtocolIPv4
	outerMF.Flags = layers.IPv4MoreFragments
	mfPkt := xpacket.LayersToPacket(t, &eth, &vlan4, &outerMF, &ip4, &icmp4)

	outerOff := ip4
	outerOff.DstIP = net.ParseIP("4.5.6.7")
	outerOff.Protocol = layers.IPProtocolIPv4
	outerOff.FragOffset = 1
	offPkt := xpacket.LayersToPacket(t, &eth, &vlan4, &outerOff, &ip4, &icmp4)

	vlan6 := vlan
	vlan6.Type = layers.EthernetTypeIPv6
	outer6Frag := ip6
	outer6Frag.DstIP = net.ParseIP("1:2:3:4::abcd")
	outer6Frag.NextHeader = layers.IPProtocolIPv6Fragment
	outer6FragExt := layers.IPv6Fragment{
		NextHeader:     layers.IPProtocolIPv4,
		FragmentOffset: 0,
		MoreFragments:  true,
		Identification: 42,
	}
	frag6Pkt := xpacket.LayersToPacket(t, &eth, &vlan6, &outer6Frag, &outer6FragExt, &ip4, &icmp4)

	res, err := h.HandlePackets(dfPkt, mfPkt, offPkt, frag6Pkt)
	require.NoError(t, err)
	require.Len(t, res.Output, 1)
	require.Len(t, res.Drop, 3)

	expectedDF := xpacket.LayersToPacket(t, &eth, &vlan4, &ip4, &icmp4)
	requireSingleOutputEquals(t, &dataplaneut.Result{Output: res.Output[:1]}, expectedDF)
	require.Equal(t, mfPkt.Data(), res.Drop[0].RawData[:len(mfPkt.Data())])
	require.Equal(t, offPkt.Data(), res.Drop[1].RawData[:len(offPkt.Data())])
	require.Equal(t, frag6Pkt.Data(), res.Drop[2].RawData[:len(frag6Pkt.Data())])
}

// TestDecap_InnerIPv4FragmentsOverIPv6 verifies that matched IPv6 outer
// decapsulation preserves inner IPv4 fragments and their payload slices.
func TestDecap_InnerIPv4FragmentsOverIPv6(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	vlan := layers.Dot1Q{
		VLANIdentifier: 100,
		Type:           layers.EthernetTypeIPv6,
	}
	ip4 := layers.IPv4{
		Version:  4,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.IPv4zero,
		DstIP:    net.ParseIP("1.1.0.1"),
	}
	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      net.ParseIP("2001:db8::1"),
		DstIP:      net.ParseIP("2001:db8::2"),
	}
	icmp4 := layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}

	h, agent, backend := setupDecapHarness(t)
	applyDecapConfig(t, backend, agent, "inner_frag", []netip.Prefix{
		netip.MustParsePrefix("1:2:3:4::/64"),
	})

	icmpPayload := append(slices.Repeat([]byte("ABCDEFGH123CCCCCCCCC"), 120), []byte("QWERTY123")...)
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	require.NoError(t, gopacket.SerializeLayers(buf, opts, &icmp4, gopacket.Payload(icmpPayload)))
	payload := buf.Bytes()

	const fragmentSize = 1208
	require.Greater(t, len(payload), fragmentSize)

	outer6 := ip6
	outer6.DstIP = net.ParseIP("1:2:3:4::abcd")
	outer6.NextHeader = layers.IPProtocolIPv4

	vlan6 := vlan
	vlan6.Type = layers.EthernetTypeIPv6
	vlan4 := vlan
	vlan4.Type = layers.EthernetTypeIPv4

	input := []gopacket.Packet{}
	expected := []gopacket.Packet{}
	for offset := 0; offset < len(payload); offset += fragmentSize {
		end := min(offset+fragmentSize, len(payload))
		payloadFragment := gopacket.Payload(payload[offset:end])

		inner4 := ip4
		inner4.Flags = 0
		if end < len(payload) {
			inner4.Flags = layers.IPv4MoreFragments
		}
		inner4.FragOffset = uint16(offset / 8)

		input = append(input, xpacket.LayersToPacket(t, &eth, &vlan6, &outer6, &inner4, payloadFragment))
		expected = append(expected, xpacket.LayersToPacket(t, &eth, &vlan4, &inner4, payloadFragment))
	}

	res, err := h.HandlePackets(input...)
	require.NoError(t, err)
	require.Len(t, res.Output, len(expected))
	require.Empty(t, res.Drop)

	for idx, expectedPkt := range expected {
		actual := xpacket.ParseEtherPacket(res.Output[idx].RawData)
		ethLayer := actual.Layer(layers.LayerTypeEthernet)
		require.NotNil(t, ethLayer)
		require.Equal(t, layers.EthernetTypeDot1Q, ethLayer.(*layers.Ethernet).EthernetType)

		vlanLayer := actual.Layer(layers.LayerTypeDot1Q)
		require.NotNil(t, vlanLayer)
		require.Equal(t, layers.EthernetTypeIPv4, vlanLayer.(*layers.Dot1Q).Type)

		diff := cmp.Diff(expectedPkt.Layers(), actual.Layers())
		require.Empty(t, diff)
	}
}

// TestDecap_GRE_ValidAndInvalid verifies GRE decapsulation for supported
// combinations and drop behavior for invalid GRE and tunnel variants.
func TestDecap_GRE_ValidAndInvalid(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	vlan := layers.Dot1Q{
		VLANIdentifier: 100,
		Type:           layers.EthernetTypeIPv6,
	}
	ip4 := layers.IPv4{
		Version:  4,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.IPv4zero,
		DstIP:    net.ParseIP("1.1.0.1"),
	}
	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv6,
		HopLimit:   64,
		SrcIP:      net.ParseIP("2001:db8::1"),
		DstIP:      net.ParseIP("2001:db8::2"),
	}
	icmp4 := layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}
	icmp6 := layers.ICMPv6{TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0)}
	icmp6.SetNetworkLayerForChecksum(&ip6)

	h, agent, backend := setupDecapHarness(t)
	applyDecapConfig(t, backend, agent, "gre", []netip.Prefix{
		netip.MustParsePrefix("1:2:3:4::/64"),
		netip.MustParsePrefix("1.2.3.0/24"),
	})

	vlan6 := vlan
	vlan6.Type = layers.EthernetTypeIPv6
	outer6 := ip6
	outer6.NextHeader = layers.IPProtocolGRE
	outer6.DstIP = net.ParseIP("1:2:3:4::abcd")

	vlan4 := vlan
	vlan4.Type = layers.EthernetTypeIPv4
	outer4 := ip4
	outer4.Protocol = layers.IPProtocolGRE
	outer4.DstIP = net.ParseIP("1.2.3.4")

	inner4 := ip4
	inner4.DstIP = net.ParseIP("198.51.100.9")
	outer4UnknownTun := outer4
	outer4UnknownTun.Protocol = layers.IPProtocolUDP
	udpUnknown := layers.UDP{SrcPort: 1234, DstPort: 4321}
	udpUnknown.SetNetworkLayerForChecksum(&outer4UnknownTun)
	inner6 := ip6
	inner6.NextHeader = layers.IPProtocolICMPv6
	inner6.DstIP = net.ParseIP("2001:db8::77")
	icmp6Inner := icmp6
	icmp6Inner.SetNetworkLayerForChecksum(&inner6)

	mkValidGRE4 := func(gre layers.GRE) gopacket.Packet {
		return xpacket.LayersToPacket(t, &eth, &vlan6, &outer6, &gre, &inner4, &icmp4)
	}
	mkValidGRE6 := func(gre layers.GRE) gopacket.Packet {
		return xpacket.LayersToPacket(t, &eth, &vlan6, &outer6, &gre, &inner6, &icmp6Inner)
	}

	cases := []struct {
		name      string
		packet    gopacket.Packet
		expected  gopacket.Packet
		wantDrop  bool
		dropIsRaw bool
	}{
		{
			name:     "valid_gre_ipv4_inner_over_ipv6_outer",
			packet:   mkValidGRE4(layers.GRE{Protocol: layers.EthernetTypeIPv4}),
			expected: xpacket.LayersToPacket(t, &eth, &vlan4, &inner4, &icmp4),
		},
		{
			name:     "valid_gre_ipv6_inner_over_ipv6_outer",
			packet:   mkValidGRE6(layers.GRE{Protocol: layers.EthernetTypeIPv6}),
			expected: xpacket.LayersToPacket(t, &eth, &vlan6, &inner6, &icmp6Inner),
		},
		{
			name:     "valid_gre_checksum_only",
			packet:   mkValidGRE4(layers.GRE{Protocol: layers.EthernetTypeIPv4, ChecksumPresent: true}),
			expected: xpacket.LayersToPacket(t, &eth, &vlan4, &inner4, &icmp4),
		},
		{
			name:     "valid_gre_key_only",
			packet:   mkValidGRE4(layers.GRE{Protocol: layers.EthernetTypeIPv4, KeyPresent: true}),
			expected: xpacket.LayersToPacket(t, &eth, &vlan4, &inner4, &icmp4),
		},
		{
			name:     "valid_gre_seq_only",
			packet:   mkValidGRE4(layers.GRE{Protocol: layers.EthernetTypeIPv4, SeqPresent: true}),
			expected: xpacket.LayersToPacket(t, &eth, &vlan4, &inner4, &icmp4),
		},
		{
			name:     "valid_gre_all_options",
			packet:   mkValidGRE4(layers.GRE{Protocol: layers.EthernetTypeIPv4, ChecksumPresent: true, KeyPresent: true, SeqPresent: true}),
			expected: xpacket.LayersToPacket(t, &eth, &vlan4, &inner4, &icmp4),
		},
		{
			name:     "valid_gre_over_ipv4_outer",
			packet:   xpacket.LayersToPacket(t, &eth, &vlan4, &outer4, &layers.GRE{Protocol: layers.EthernetTypeIPv4}, &inner4, &icmp4),
			expected: xpacket.LayersToPacket(t, &eth, &vlan4, &inner4, &icmp4),
		},
		{
			name:      "drop_invalid_version",
			packet:    mkValidGRE4(layers.GRE{Protocol: layers.EthernetTypeIPv4, Version: 1}),
			wantDrop:  true,
			dropIsRaw: true,
		},
		{
			name:      "drop_invalid_version_4",
			packet:    mkValidGRE4(layers.GRE{Protocol: layers.EthernetTypeIPv4, Version: 4}),
			wantDrop:  true,
			dropIsRaw: true,
		},
		{
			name:      "drop_reserved_bits_recursion_control",
			packet:    mkValidGRE4(layers.GRE{Protocol: layers.EthernetTypeIPv4, RecursionControl: 1}),
			wantDrop:  true,
			dropIsRaw: true,
		},
		{
			name:      "drop_reserved_bits_recursion_control_4",
			packet:    mkValidGRE4(layers.GRE{Protocol: layers.EthernetTypeIPv4, RecursionControl: 4}),
			wantDrop:  true,
			dropIsRaw: true,
		},
		{
			name:      "drop_routing_present",
			packet:    mkValidGRE4(layers.GRE{Protocol: layers.EthernetTypeIPv4, RoutingPresent: true}),
			wantDrop:  true,
			dropIsRaw: true,
		},
		{
			name:      "drop_strict_source_route",
			packet:    mkValidGRE4(layers.GRE{Protocol: layers.EthernetTypeIPv4, StrictSourceRoute: true}),
			wantDrop:  true,
			dropIsRaw: true,
		},
		{
			name:      "drop_unsupported_gre_proto",
			packet:    mkValidGRE4(layers.GRE{Protocol: layers.EthernetTypeARP}),
			wantDrop:  true,
			dropIsRaw: true,
		},
		{
			name:      "drop_unknown_tunnel_protocol_after_prefix_match",
			packet:    xpacket.LayersToPacket(t, &eth, &vlan4, &outer4UnknownTun, &udpUnknown, gopacket.Payload([]byte{1, 2, 3, 4})),
			wantDrop:  true,
			dropIsRaw: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := h.HandlePackets(tc.packet)
			require.NoError(t, err)
			if tc.wantDrop {
				require.Empty(t, res.Output)
				require.Len(t, res.Drop, 1)
				if tc.dropIsRaw {
					require.Equal(t, tc.packet.Data(), res.Drop[0].RawData[:len(tc.packet.Data())])
				}
				return
			}
			requireSingleOutputEquals(t, res, tc.expected)
		})
	}
}

// TestDecap_NonIPPacket_UnchangedOutput verifies that non-IP packets bypass
// decapsulation and are forwarded unchanged.
func TestDecap_NonIPPacket_UnchangedOutput(t *testing.T) {
	h, agent, backend := setupDecapHarness(t)
	applyDecapConfig(t, backend, agent, "nonip", []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")})

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeARP,
	}
	arp := layers.ARP{
		AddrType:          layers.LinkTypeEthernet,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         layers.ARPRequest,
		SourceHwAddress:   []byte{0, 0, 0, 0, 0, 1},
		SourceProtAddress: []byte{192, 0, 2, 1},
		DstHwAddress:      []byte{0, 0, 0, 0, 0, 0},
		DstProtAddress:    []byte{192, 0, 2, 2},
	}
	pkt := xpacket.LayersToPacket(t, &eth, &arp)

	res, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, res.Output, 1)
	require.Empty(t, res.Drop)
	require.Equal(t, pkt.Data(), res.Output[0].RawData[:len(pkt.Data())])
}
