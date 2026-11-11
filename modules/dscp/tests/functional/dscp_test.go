package dscp_test

import (
	"net"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	"github.com/yanet-platform/yanet2/modules/dscp/bindings/go/cdscp"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
)

const (
	dscpMarkNever   uint8 = 0
	dscpMarkDefault uint8 = 1
	dscpMarkAlways  uint8 = 2
)

// setupDSCPHarness publishes marking and unchanged egress for both IP families.
func setupDSCPHarness(t *testing.T, flag, mark uint8) *dataplaneut.Harness {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"dscp", "forward"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach("dscp-test", 0, 8*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, agent.CleanUp()) })

	module, err := cdscp.NewModuleConfig(agent, "mark")
	require.NoError(t, err)
	t.Cleanup(func() { _ = module.Free() })
	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("1:2:3:4::/64"),
		netip.MustParsePrefix("1.1.0.0/32"),
	} {
		require.NoError(t, module.PrefixAdd(prefix))
	}
	require.NoError(t, module.SetDscpMarking(flag, mark))

	sink, err := cforward.NewModuleConfig(agent, "sink")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Free() })
	require.NoError(t, sink.Update([]cforward.ForwardRule{{
		Target:  "port0",
		Mode:    cforward.ModeOut,
		Counter: "egress",
		Devices: filter.Devices{{Name: "port0"}},
	}}))
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{
		module.AsFFIModule(), sink.AsFFIModule(),
	}))
	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: "mark",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: "mark",
				Modules: []ffi.ChainModuleConfig{
					{Type: "dscp", Name: "mark"},
					{Type: "forward", Name: "sink"},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "input",
		Functions: []string{"mark"},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "output"}))
	devices, err := cplain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: "input", Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "output", Weight: 1}},
	}})
	require.NoError(t, err)
	for _, device := range devices {
		t.Cleanup(func() { _ = device.Free() })
	}
	return harness
}

// markPacket returns a marked copy with recalculated lengths and checksums.
func markPacket(t *testing.T, packet gopacket.Packet, dscp uint8) gopacket.Packet {
	t.Helper()

	packet = gopacket.NewPacket(packet.Data(), layers.LayerTypeEthernet, gopacket.Default)
	network := packet.NetworkLayer()
	// DSCP is six bits; larger values wrap and ECN stays clear.
	trafficClass := dscp << 2
	switch network.LayerType() {
	case layers.LayerTypeIPv4:
		network.(*layers.IPv4).TOS = trafficClass
	case layers.LayerTypeIPv6:
		network.(*layers.IPv6).TrafficClass = trafficClass
	default:
		t.Fatalf("unexpected network layer type: %v", network.LayerType())
	}
	buffer := gopacket.NewSerializeBuffer()
	options := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	require.NoError(t, gopacket.SerializePacket(buffer, options, packet))

	packet = gopacket.NewPacket(buffer.Bytes(), layers.LayerTypeEthernet, gopacket.Default)
	require.Empty(t, packet.ErrorLayer())
	return packet
}

type dscpFrame struct {
	name   string
	packet gopacket.Packet
}

// dscpFrames returns IPv4 and IPv6 ICMP fixtures with and without VLAN 100.
func dscpFrames(t *testing.T, destination4, destination6 string) []dscpFrame {
	t.Helper()

	ethernet := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	ethernet4 := ethernet
	ethernet4.EthernetType = layers.EthernetTypeIPv4
	ethernet6 := ethernet
	ethernet6.EthernetType = layers.EthernetTypeIPv6

	vlan4 := layers.Dot1Q{
		VLANIdentifier: 100,
		Type:           layers.EthernetTypeIPv4,
	}
	vlan6 := vlan4
	vlan6.Type = layers.EthernetTypeIPv6

	ipv6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolICMPv4,
		HopLimit:   64,
		SrcIP:      net.IPv6zero,
		DstIP:      net.ParseIP(destination6),
	}
	ipv4 := layers.IPv4{
		Version:  4,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.IPv4zero,
		DstIP:    net.ParseIP(destination4),
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(
			layers.ICMPv4TypeEchoRequest,
			layers.ICMPv4CodeNet,
		),
	}

	return []dscpFrame{
		{"v4", xpacket.LayersToPacket(t, &ethernet4, &ipv4, &icmp)},
		{"v6", xpacket.LayersToPacket(t, &ethernet6, &ipv6, &icmp)},
		{"vlan>v4", xpacket.LayersToPacket(t, &ethernet, &vlan4, &ipv4, &icmp)},
		{"vlan>v6", xpacket.LayersToPacket(t, &ethernet, &vlan6, &ipv6, &icmp)},
	}
}

// requireDSCPOutput checks every decoded layer after native packet reclamation.
func requireDSCPOutput(
	t *testing.T,
	harness *dataplaneut.Harness,
	result *dataplaneut.Result,
	expected gopacket.Packet,
) {
	t.Helper()

	require.Len(t, result.Output, 1)
	require.Empty(t, result.Drop)
	require.Zero(t, harness.OutstandingMbufs())
	actual := xpacket.ParseEtherPacket(result.Output[0].RawData)
	require.Empty(t, actual.ErrorLayer())
	diff := cmp.Diff(expected.Layers(), actual.Layers(),
		cmpopts.IgnoreUnexported(layers.IPv6{}, layers.ICMPv6{}),
	)
	require.Empty(t, diff)
}

// Test_DSCP_MarkingPolicies verifies that marking changes only DSCP and the
// IPv4 checksum, preserving every other layer and VLAN header.
func Test_DSCP_MarkingPolicies(t *testing.T) {
	frames := dscpFrames(t, "1.1.0.0", "1:2:3:4::abcd")
	cases := []struct {
		name     string
		flag     uint8
		mark     uint8
		from     uint8
		expected uint8
	}{
		{"Never remark 0->10=10", dscpMarkNever, 0, 10, 10},
		{"Never remark 0-> 0= 0", dscpMarkNever, 0, 0, 0},
		{"Never remark 7->60=60", dscpMarkNever, 7, 60, 60},
		{"Never remark 7->70=70", dscpMarkNever, 7, 70, 70},

		{"Always mark 40-> 0=40", dscpMarkAlways, 40, 0, 40},
		{"Always mark 7 ->16= 7", dscpMarkAlways, 7, 16, 7},
		{"Always mark 10->26=10", dscpMarkAlways, 10, 26, 10},
		{"Always mark 0 ->36= 0", dscpMarkAlways, 0, 36, 0},

		{"Default mark 7 ->13=13", dscpMarkDefault, 7, 13, 13},
		{"Default mark 7 -> 0= 7", dscpMarkDefault, 7, 0, 7},
		{"Default mark 0 -> 1= 1", dscpMarkDefault, 0, 1, 1},
		{"Default mark 0 -> 0= 0", dscpMarkDefault, 0, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			harness := setupDSCPHarness(t, tc.flag, tc.mark)
			for _, frame := range frames {
				t.Run(frame.name, func(t *testing.T) {
					packet := markPacket(t, frame.packet, tc.from)
					result, err := harness.HandlePackets(packet)
					require.NoError(t, err)
					expected := markPacket(t, frame.packet, tc.expected)
					requireDSCPOutput(t, harness, result, expected)
				})
			}
		})
	}
}

// Test_DSCP_PrefixMissPreservesBytes verifies that unmatched destinations
// bypass unconditional marking and reach egress byte for byte.
func Test_DSCP_PrefixMissPreservesBytes(t *testing.T) {
	harness := setupDSCPHarness(t, dscpMarkAlways, 40)
	for _, tc := range dscpFrames(t, "1.1.0.1", "1:2:3:5::abcd") {
		t.Run(tc.name, func(t *testing.T) {
			packet := markPacket(t, tc.packet, 7)
			result, err := harness.HandlePackets(packet)
			require.NoError(t, err)
			requireDSCPOutput(t, harness, result, packet)
			require.Equal(t, packet.Data(), result.Output[0].RawData)
		})
	}
}

// Test_DSCP_ResultsSurviveNextRound verifies that returned bytes remain valid
// after native packet reclamation and a subsequent round with different DSCP.
func Test_DSCP_ResultsSurviveNextRound(t *testing.T) {
	harness := setupDSCPHarness(t, dscpMarkNever, 0)
	for _, tc := range dscpFrames(t, "1.1.0.0", "1:2:3:4::abcd") {
		t.Run(tc.name, func(t *testing.T) {
			first := markPacket(t, tc.packet, 10)
			firstResult, err := harness.HandlePackets(first)
			require.NoError(t, err)
			require.Zero(t, harness.OutstandingMbufs())

			second := markPacket(t, tc.packet, 20)
			secondResult, err := harness.HandlePackets(second)
			require.NoError(t, err)
			requireDSCPOutput(t, harness, secondResult, second)
			requireDSCPOutput(t, harness, firstResult, first)
		})
	}
}
