package dscp_test

import (
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	"github.com/yanet-platform/yanet2/modules/dscp/bindings/go/cdscp"
	dscppb "github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb/v1"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	"github.com/yanet-platform/yanet2/operators/generic/operator"
)

const (
	dscpMarkNever   uint8 = 0
	dscpMarkDefault uint8 = 1
	dscpMarkAlways  uint8 = 2
)

// ECN codepoints of RFC 3168 section 5, held in the two low bits of the IPv4
// TOS octet and of the IPv6 traffic class.
const (
	ecnNotECT uint8 = 0b00
	ecnECT1   uint8 = 0b01
	ecnECT0   uint8 = 0b10
	ecnCE     uint8 = 0b11
)

// setupDSCPHarness publishes marking and unchanged egress for both IP families.
func setupDSCPHarness(t *testing.T, flag, mark uint8) *dataplaneut.Harness {
	t.Helper()

	return setupDSCPSelectionHarness(t, flag, mark, dscpSelection{
		destinations: []netip.Prefix{
			netip.MustParsePrefix("1:2:3:4::/64"),
			netip.MustParsePrefix("1.1.0.0/32"),
		},
	})
}

type dscpSelection struct {
	destinations []netip.Prefix
	sources      []netip.Prefix
}

// setupDSCPSelectionHarness publishes both prefix sets with unchanged egress.
func setupDSCPSelectionHarness(
	t *testing.T,
	flag, mark uint8,
	selection dscpSelection,
) *dataplaneut.Harness {
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
	for _, prefix := range selection.destinations {
		require.NoError(t, module.PrefixAdd(prefix))
	}
	for _, prefix := range selection.sources {
		require.NoError(t, module.SourcePrefixAdd(prefix))
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

// markPacket returns a copy carrying the given DSCP and ECN codepoint, with
// recalculated lengths and checksums.
func markPacket(t *testing.T, packet gopacket.Packet, dscp, ecn uint8) gopacket.Packet {
	t.Helper()

	packet = gopacket.NewPacket(packet.Data(), layers.LayerTypeEthernet, gopacket.Default)
	network := packet.NetworkLayer()
	// A DSCP above six bits wraps, and the ECN codepoint takes the two bits
	// below it.
	trafficClass := dscp<<2 | ecn
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

	return dscpFramesWithAddresses(t, "0.0.0.0", destination4, "::", destination6)
}

// dscpFramesWithAddresses returns both IP families, tagged and untagged,
// with independently selected source and destination addresses.
func dscpFramesWithAddresses(
	t *testing.T,
	source4, destination4, source6, destination6 string,
) []dscpFrame {
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
		SrcIP:      net.ParseIP(source6),
		DstIP:      net.ParseIP(destination6),
	}
	ipv4 := layers.IPv4{
		Version:  4,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP(source4),
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
					packet := markPacket(t, frame.packet, tc.from, ecnNotECT)
					result, err := harness.HandlePackets(packet)
					require.NoError(t, err)
					expected := markPacket(t, frame.packet, tc.expected, ecnNotECT)
					requireDSCPOutput(t, harness, result, expected)
				})
			}
		})
	}
}

// Test_DSCP_MarkingKeepsECN verifies that marking rewrites only the DSCP and
// keeps the ECN codepoint of either family as it arrived.
//
// RFC 3260 section 4 gives Diffserv the six most significant bits of the IPv4
// TOS octet and of the IPv6 traffic class, and RFC 3168 section 5 defines the
// two least significant ones as the ECN field, the same in both families.
// Clearing an ECN-capable codepoint disables ECN for the packet (RFC 3168
// section 18.1.3), and clearing congestion experienced also erases the signal
// of a congested router upstream (RFC 3168 section 18.1.1).
func Test_DSCP_MarkingKeepsECN(t *testing.T) {
	frames := dscpFrames(t, "1.1.0.0", "1:2:3:4::abcd")
	codepoints := []struct {
		name string
		ecn  uint8
	}{
		{"ECT(1)", ecnECT1},
		{"ECT(0)", ecnECT0},
		{"CE", ecnCE},
	}
	cases := []struct {
		name     string
		flag     uint8
		mark     uint8
		from     uint8
		expected uint8
	}{
		{"Always mark 40-> 0=40", dscpMarkAlways, 40, 0, 40},
		{"Always mark 40->26=40", dscpMarkAlways, 40, 26, 40},
		{"Default mark 40-> 0=40", dscpMarkDefault, 40, 0, 40},
		{"Default mark 40->26=26", dscpMarkDefault, 40, 26, 26},
		{"Never remark 40->26=26", dscpMarkNever, 40, 26, 26},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			harness := setupDSCPHarness(t, tc.flag, tc.mark)
			for _, codepoint := range codepoints {
				t.Run(codepoint.name, func(t *testing.T) {
					for _, frame := range frames {
						t.Run(frame.name, func(t *testing.T) {
							packet := markPacket(t, frame.packet, tc.from, codepoint.ecn)
							result, err := harness.HandlePackets(packet)
							require.NoError(t, err)
							expected := markPacket(t, frame.packet, tc.expected, codepoint.ecn)
							requireDSCPOutput(t, harness, result, expected)
						})
					}
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
			packet := markPacket(t, tc.packet, 7, ecnNotECT)
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
			first := markPacket(t, tc.packet, 10, ecnNotECT)
			firstResult, err := harness.HandlePackets(first)
			require.NoError(t, err)
			require.Zero(t, harness.OutstandingMbufs())

			second := markPacket(t, tc.packet, 20, ecnNotECT)
			secondResult, err := harness.HandlePackets(second)
			require.NoError(t, err)
			requireDSCPOutput(t, harness, secondResult, second)
			requireDSCPOutput(t, harness, firstResult, first)
		})
	}
}

// setupDSCPYAMLHarness loads the generic operator's request and publishes its
// prefix sets and marking through the native module binding.
func setupDSCPYAMLHarness(t *testing.T, document string) *dataplaneut.Harness {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dscp.yaml")
	require.NoError(t, os.WriteFile(path, []byte(document), 0o600))
	message, err := operator.LoadRequest(
		"modules.dscp.controlplane.dscppb.v1.DscpService/UpdateConfig",
		path,
	)
	require.NoError(t, err)
	request := message.(*dscppb.UpdateConfigRequest)
	require.NoError(t, request.Validate())
	config := request.GetConfig()
	prefixes4, err := commonpb.PrefixesFromNetworks("prefixes4", config.GetPrefixes4())
	require.NoError(t, err)
	prefixes6, err := commonpb.PrefixesFromNetworks("prefixes6", config.GetPrefixes6())
	require.NoError(t, err)
	sources4, err := commonpb.PrefixesFromNetworks("source_prefixes4", config.GetSourcePrefixes4())
	require.NoError(t, err)
	sources6, err := commonpb.PrefixesFromNetworks("source_prefixes6", config.GetSourcePrefixes6())
	require.NoError(t, err)
	return setupDSCPSelectionHarness(t,
		uint8(config.GetDscpConfig().GetFlag()),
		uint8(config.GetDscpConfig().GetMark()),
		dscpSelection{
			destinations: slices.Concat(prefixes4, prefixes6),
			sources:      slices.Concat(sources4, sources6),
		},
	)
}

// Test_DSCP_ConfigWithoutSourcesMatchesOnlyDestinations verifies that legacy
// YAML marks matching destinations independently of the source address and
// preserves packets whose destination misses, including empty selections.
func Test_DSCP_ConfigWithoutSourcesMatchesOnlyDestinations(t *testing.T) {
	cases := []struct {
		name   string
		config string
		marks4 bool
		marks6 bool
	}{
		{
			name: "IPv4 destinations",
			config: `config:
  prefixes4: [1.1.0.0/32]
  dscp_config: {flag: 2, mark: 40}
`,
			marks4: true,
		},
		{
			name: "IPv6 destinations",
			config: `config:
  prefixes6: ["1:2:3:4::/64"]
  dscp_config: {flag: 2, mark: 40}
`,
			marks6: true,
		},
		{
			name: "both destination families",
			config: `config:
  prefixes4: [1.1.0.0/32]
  prefixes6: ["1:2:3:4::/64"]
  dscp_config: {flag: 2, mark: 40}
`,
			marks4: true,
			marks6: true,
		},
		{
			name: "explicit empty destinations",
			config: `config:
  prefixes4: []
  prefixes6: []
  dscp_config: {flag: 2, mark: 40}
`,
		},
		{
			name:   "omitted destinations",
			config: "config: {dscp_config: {flag: 2, mark: 40}}\n",
		},
		{
			name:   "empty config",
			config: "config: {}\n",
		},
		{
			name: "omitted marking",
			config: `config:
  prefixes4: [1.1.0.0/32]
  prefixes6: ["1:2:3:4::/64"]
`,
		},
	}
	addresses := []struct {
		name     string
		address4 string
		address6 string
		matches  bool
	}{
		{"inside destination prefixes", "1.1.0.0", "1:2:3:4::abcd", true},
		{"outside destination prefixes", "203.0.113.7", "2001:db8::7", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			harness := setupDSCPYAMLHarness(t, "name: mark\n"+tc.config)
			for _, source := range addresses {
				for _, destination := range addresses {
					t.Run("source "+source.name+"/destination "+destination.name, func(t *testing.T) {
						for _, frame := range dscpFramesWithAddresses(t,
							source.address4, destination.address4,
							source.address6, destination.address6,
						) {
							t.Run(frame.name, func(t *testing.T) {
								packet := markPacket(t, frame.packet, 7, ecnCE)
								marks := tc.marks4
								if packet.NetworkLayer().LayerType() == layers.LayerTypeIPv6 {
									marks = tc.marks6
								}
								expected := packet
								if destination.matches && marks {
									expected = markPacket(t, frame.packet, 40, ecnCE)
								}
								result, err := harness.HandlePackets(packet)
								require.NoError(t, err)
								requireDSCPOutput(t, harness, result, expected)
							})
						}
					})
				}
			}
		})
	}
}

// Test_DSCP_SourceSelection verifies that source-only filters ignore the
// destination, combined filters require both directions, and a populated
// direction without prefixes for the packet's family never matches.
func Test_DSCP_SourceSelection(t *testing.T) {
	cases := []struct {
		name     string
		prefixes string
		marks4   [4]bool
		marks6   [4]bool
	}{
		{
			name:     "IPv4 source only",
			prefixes: "  source_prefixes4: [192.0.2.0/24]\n",
			marks4:   [4]bool{false, false, true, true},
		},
		{
			name:     "IPv6 source only",
			prefixes: "  source_prefixes6: ['2001:db8:1::/48']\n",
			marks6:   [4]bool{false, false, true, true},
		},
		{
			name: "both source families",
			prefixes: `  source_prefixes4: [192.0.2.0/24]
  source_prefixes6: ["2001:db8:1::/48"]
`,
			marks4: [4]bool{false, false, true, true},
			marks6: [4]bool{false, false, true, true},
		},
		{
			name: "both directions and families",
			prefixes: `  prefixes4: [198.51.100.0/24]
  prefixes6: ["2001:db8:2::/48"]
  source_prefixes4: [192.0.2.0/24]
  source_prefixes6: ["2001:db8:1::/48"]
`,
			marks4: [4]bool{false, false, false, true},
			marks6: [4]bool{false, false, false, true},
		},
		{
			name: "directions with different address families",
			prefixes: `  prefixes6: ["2001:db8:2::/48"]
  source_prefixes4: [192.0.2.0/24]
`,
		},
	}
	packets := []struct {
		name         string
		source4      string
		destination4 string
		source6      string
		destination6 string
	}{
		{"neither matches", "203.0.113.7", "203.0.113.9", "2001:db8:3::7", "2001:db8:4::9"},
		{"destination matches", "203.0.113.7", "198.51.100.7", "2001:db8:3::7", "2001:db8:2::7"},
		{"source matches", "192.0.2.7", "203.0.113.9", "2001:db8:1::7", "2001:db8:4::9"},
		{"both match", "192.0.2.7", "198.51.100.7", "2001:db8:1::7", "2001:db8:2::7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			harness := setupDSCPYAMLHarness(t,
				"name: mark\nconfig:\n"+tc.prefixes+"  dscp_config: {flag: 2, mark: 40}\n",
			)
			for idx, addresses := range packets {
				t.Run(addresses.name, func(t *testing.T) {
					for _, frame := range dscpFramesWithAddresses(t,
						addresses.source4, addresses.destination4,
						addresses.source6, addresses.destination6,
					) {
						t.Run(frame.name, func(t *testing.T) {
							marks := tc.marks4[idx]
							if frame.packet.NetworkLayer().LayerType() == layers.LayerTypeIPv6 {
								marks = tc.marks6[idx]
							}
							packet := markPacket(t, frame.packet, 7, ecnECT0)
							expected := packet
							if marks {
								expected = markPacket(t, frame.packet, 40, ecnECT0)
							}
							result, err := harness.HandlePackets(packet)
							require.NoError(t, err)
							requireDSCPOutput(t, harness, result, expected)
						})
					}
				})
			}
		})
	}
}

// Test_DSCP_SourceMarkingPoliciesKeepECN verifies that source-selected packets
// obey every marking mode while preserving ECN and updating IPv4 checksums.
func Test_DSCP_SourceMarkingPoliciesKeepECN(t *testing.T) {
	cases := []struct {
		name     string
		flag     uint8
		from     uint8
		expected uint8
	}{
		{"never preserves unmarked packets", dscpMarkNever, 0, 0},
		{"never preserves marked packets", dscpMarkNever, 7, 7},
		{"default marks unmarked packets", dscpMarkDefault, 0, 40},
		{"default preserves marked packets", dscpMarkDefault, 7, 7},
		{"always marks unmarked packets", dscpMarkAlways, 0, 40},
		{"always remarks marked packets", dscpMarkAlways, 7, 40},
	}
	codepoints := []struct {
		name string
		ecn  uint8
	}{
		{"not ECT", ecnNotECT},
		{"ECT1", ecnECT1},
		{"ECT0", ecnECT0},
		{"CE", ecnCE},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			harness := setupDSCPSelectionHarness(t, tc.flag, 40, dscpSelection{
				sources: []netip.Prefix{
					netip.MustParsePrefix("192.0.2.0/24"),
					netip.MustParsePrefix("2001:db8:1::/48"),
				},
			})
			for _, codepoint := range codepoints {
				t.Run(codepoint.name, func(t *testing.T) {
					for _, frame := range dscpFramesWithAddresses(t,
						"192.0.2.7", "198.51.100.7", "2001:db8:1::7", "2001:db8:2::7",
					) {
						t.Run(frame.name, func(t *testing.T) {
							packet := markPacket(t, frame.packet, tc.from, codepoint.ecn)
							expected := markPacket(t, frame.packet, tc.expected, codepoint.ecn)
							result, err := harness.HandlePackets(packet)
							require.NoError(t, err)
							requireDSCPOutput(t, harness, result, expected)
						})
					}
				})
			}
		})
	}
}
