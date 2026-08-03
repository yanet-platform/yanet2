package unrdup_test

import (
	"net"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/xnetip"
	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	cforward "github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	"github.com/yanet-platform/yanet2/modules/unrdup/bindings/go/cunrdup"
)

const (
	unrdupCPSize  = 64 * datasize.MB
	unrdupDPSize  = 4 * datasize.MB
	unrdupMemSize = 16 * datasize.MB
)

const (
	servicePort    = 443
	unservedPort   = 8080
	ipprotoTCP     = 6
	deviceName     = "port0"
	configName     = "test"
	outerHeaderV4  = 20
	etherHeaderLen = 14
)

var (
	vip      = netip.MustParseAddr("192.0.2.1")
	client   = net.ParseIP("198.51.100.7")
	router   = net.ParseIP("203.0.113.1")
	peerV4   = netip.MustParseAddr("10.0.0.10")
	peerV6   = netip.MustParseAddr("2001:db8:b::11")
	sourceV4 = netip.MustParsePrefix("10.0.0.0/30")
	sourceV6 = netip.MustParsePrefix("2001:db8:a::/96")
)

func setupHarness(
	t *testing.T,
	sources []xnetip.Network,
	peers []netip.Addr,
) *dataplaneut.Harness {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(unrdupCPSize),
		DPMemory:      uint64(unrdupDPSize),
		WorkerCount:   1,
		Devices:       []string{deviceName},
		Modules:       []string{"unrdup", "forward"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach("unrdup-test", 0, unrdupMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	module, err := cunrdup.NewModuleConfig(agent, configName)
	require.NoError(t, err)
	t.Cleanup(module.Free)

	for _, source := range sources {
		require.NoError(t, module.SetSource(source))
	}
	require.NoError(t, module.UpdateServices([]cunrdup.Service{{
		VIP:       vip,
		Peers:     peers,
		Endpoints: []cunrdup.Endpoint{{Port: servicePort, Proto: ipprotoTCP}},
	}}))

	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}))
	wirePipeline(t, agent)

	return harness
}

func bothSources() []xnetip.Network {
	return []xnetip.Network{
		mustNetwork(sourceV4),
		mustNetwork(sourceV6),
	}
}

func mustNetwork(prefix netip.Prefix) xnetip.Network {
	network, ok := xnetip.NetworkFromPrefix(prefix)
	if !ok {
		panic("prefix must convert to a network: " + prefix.String())
	}

	return network
}

func wirePipeline(t *testing.T, agent *ffi.Agent) {
	t.Helper()

	sinkName := configName + "-sink"
	sink, err := forward.NewBackend(agent).UpdateModule(sinkName, catchAllRules())
	require.NoError(t, err)
	t.Cleanup(sink.Free)

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: configName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: configName + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "unrdup", Name: configName},
					{Type: "forward", Name: sinkName},
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
		Name:   deviceName,
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}))
}

func catchAllRules() []cforward.ForwardRule {
	return []cforward.ForwardRule{
		{
			Target:  deviceName,
			Mode:    cforward.ModeOut,
			Counter: "sink4",
			Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
			Dst4s:   filter.IPNets{filter.UnspecifiedIPv4},
		},
		{
			Target:  deviceName,
			Mode:    cforward.ModeOut,
			Counter: "sink6",
			Src6s:   filter.IPNets{filter.UnspecifiedIPv6},
			Dst6s:   filter.IPNets{filter.UnspecifiedIPv6},
		},
		{
			Target:  deviceName,
			Mode:    cforward.ModeOut,
			Counter: "sink_l2",
			Devices: filter.Devices{{Name: deviceName}},
		},
	}
}

func icmpError(t *testing.T, srcIP net.IP, port layers.TCPPort) gopacket.Packet {
	t.Helper()

	offendingIP := &layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    srcIP,
		DstIP:    client,
	}
	offendingTCP := &layers.TCP{SrcPort: port, DstPort: 12345}
	require.NoError(t, offendingTCP.SetNetworkLayerForChecksum(offendingIP))

	buf := gopacket.NewSerializeBuffer()
	require.NoError(t, gopacket.SerializeLayers(
		buf,
		gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true},
		offendingIP,
		offendingTCP,
	))
	offending := buf.Bytes()

	eth := &layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip := &layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    router,
		DstIP:    net.IP(vip.AsSlice()),
	}
	icmp := &layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(
			layers.ICMPv4TypeDestinationUnreachable,
			layers.ICMPv4CodeFragmentationNeeded,
		),
	}

	return xpacket.LayersToPacket(t, eth, ip, icmp, gopacket.Payload(offending))
}

func TestUnrdup_TunnelsToEveryPeer(t *testing.T) {
	harness := setupHarness(t, bothSources(), []netip.Addr{peerV4, peerV6})

	packet := icmpError(t, net.IP(vip.AsSlice()), servicePort)

	result, err := harness.HandlePackets(packet)
	require.NoError(t, err)

	require.Len(t, result.Output, 2, "one clone per peer")
	require.Len(t, result.Drop, 1, "the error itself is consumed")

	v4Clone, v6Clone := result.Output[0], result.Output[1]

	require.True(t, v4Clone.IsIPv4)
	require.Equal(t, net.IP(peerV4.AsSlice()).String(), v4Clone.DstIP.String())
	require.Equal(t, layers.IPProtocolIPv4, v4Clone.Protocol, "ipv4 in ipv4 tunnel")
	require.True(
		t,
		sourceV4.Contains(netip.MustParseAddr(v4Clone.SrcIP.String())),
		"the source stays inside the configured prefix",
	)

	require.True(t, v6Clone.IsIPv6)
	require.Equal(t, net.IP(peerV6.AsSlice()).String(), v6Clone.DstIP.String())
	require.Equal(t, layers.IPProtocolIPv4, v6Clone.NextHeader, "ipv4 in ipv6 tunnel")
	require.True(
		t,
		sourceV6.Contains(netip.MustParseAddr(v6Clone.SrcIP.String())),
		"the source stays inside the configured prefix",
	)
}

func TestUnrdup_CarriesTheErrorIntact(t *testing.T) {
	harness := setupHarness(t, bothSources(), []netip.Addr{peerV4})

	packet := icmpError(t, net.IP(vip.AsSlice()), servicePort)

	result, err := harness.HandlePackets(packet)
	require.NoError(t, err)
	require.Len(t, result.Output, 1)

	clone := result.Output[0].RawData
	original := packet.Data()

	require.Len(t, clone, len(original)+outerHeaderV4)

	require.Equal(
		t,
		original[etherHeaderLen:],
		clone[etherHeaderLen+outerHeaderV4:],
		"the error travels unchanged inside the tunnel",
	)
}

func TestUnrdup_LeavesOtherTrafficAlone(t *testing.T) {
	tests := []struct {
		name   string
		packet func(t *testing.T) gopacket.Packet
	}{
		{
			name: "endpoint is not served",
			packet: func(t *testing.T) gopacket.Packet {
				return icmpError(t, net.IP(vip.AsSlice()), unservedPort)
			},
		},
		{
			name: "vip is not served",
			packet: func(t *testing.T) gopacket.Packet {
				return icmpError(t, net.ParseIP("192.0.2.9"), servicePort)
			},
		},
		{
			name: "not an icmp error at all",
			packet: func(t *testing.T) gopacket.Packet {
				eth := &layers.Ethernet{
					SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
					DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
					EthernetType: layers.EthernetTypeIPv4,
				}
				ip := &layers.IPv4{
					Version:  4,
					TTL:      64,
					Protocol: layers.IPProtocolTCP,
					SrcIP:    client,
					DstIP:    net.IP(vip.AsSlice()),
				}
				tcp := &layers.TCP{SrcPort: 12345, DstPort: servicePort}
				require.NoError(t, tcp.SetNetworkLayerForChecksum(ip))

				return xpacket.LayersToPacket(t, eth, ip, tcp)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := setupHarness(t, bothSources(), []netip.Addr{peerV4})

			packet := test.packet(t)

			result, err := harness.HandlePackets(packet)
			require.NoError(t, err)

			require.Len(t, result.Output, 1, "the packet travels on untouched")
			require.Empty(t, result.Drop)
			require.Equal(t, packet.Data(), result.Output[0].RawData)
		})
	}
}

func TestUnrdup_SkipsAPeerWithoutASource(t *testing.T) {
	harness := setupHarness(
		t,
		[]xnetip.Network{mustNetwork(sourceV4)},
		[]netip.Addr{peerV4, peerV6},
	)

	result, err := harness.HandlePackets(icmpError(t, net.IP(vip.AsSlice()), servicePort))
	require.NoError(t, err)

	require.Len(t, result.Output, 1, "only the peer whose family has a source")
	require.Len(t, result.Drop, 1, "the error is still consumed")
	require.True(t, result.Output[0].IsIPv4)
}
