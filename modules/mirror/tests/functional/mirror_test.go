package mirror_test

import (
	"fmt"
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	"github.com/yanet-platform/yanet2/modules/mirror/bindings/go/cmirror"
	mirror "github.com/yanet-platform/yanet2/modules/mirror/controlplane"
)

// Memory sizes for the mirror functional harness.
const (
	mirCPSize  = 64 * datasize.MB
	mirDPSize  = 4 * datasize.MB
	mirMemSize = 16 * datasize.MB
)

// setupMirrorHarness builds a dataplane harness with the mirror module
// loaded and attaches a control-plane agent.
//
// devices is the set of logical port names to register in the harness
// topology. The first device in the list serves as the primary ingress port.
// Cleanup is wired via t.Cleanup in LIFO order.
func setupMirrorHarness(
	t *testing.T,
	devices []string,
) (*dataplaneut.Harness, *ffi.Agent, mirror.Backend) {
	t.Helper()

	cfg := dataplaneut.Config{
		CPMemory:      uint64(mirCPSize),
		DPMemory:      uint64(mirDPSize),
		WorkerCount:   1,
		Devices:       devices,
		Modules:       []string{"mirror", "forward"},
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("mir-test", 0, mirMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	backend := mirror.NewBackend(agent)
	return h, agent, backend
}

// applyRules pushes the given rules via backend.UpdateModule.
//
// The module handle is freed via t.Cleanup. The caller must wire the pipeline
// after calling applyRules, because the mirror module config must exist in
// shared memory before UpdatePlainDevices resolves chain module references.
func applyRules(
	t *testing.T,
	backend mirror.Backend,
	name string,
	rules []cmirror.MirrorRule,
) mirror.ModuleHandle {
	t.Helper()

	handle, err := backend.UpdateModule(name, rules)
	require.NoError(t, err)
	t.Cleanup(handle.Free)
	return handle
}

// catchAllForwardRules returns forward rules with ModeOut that match every
// packet type (IPv4, IPv6, and non-IP L2) and route them to the given device's
// output stage.
//
// The input entry point is not allowed to transmit directly, so pass-through
// packets need a catch-all ModeOut rule to reach egress via pending_output.
func catchAllForwardRules(device string) []cforward.ForwardRule {
	return []cforward.ForwardRule{
		{
			Target:  device,
			Mode:    cforward.ModeOut,
			Counter: "sink4",
			Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
			Dst4s:   filter.IPNets{filter.UnspecifiedIPv4},
		},
		{
			Target:  device,
			Mode:    cforward.ModeOut,
			Counter: "sink6",
			Src6s:   filter.IPNets{filter.UnspecifiedIPv6},
			Dst6s:   filter.IPNets{filter.UnspecifiedIPv6},
		},
		{
			Target:  device,
			Mode:    cforward.ModeOut,
			Counter: "sink_l2",
			Devices: filter.Devices{{Name: device}},
		},
	}
}

// wireMirrorPipeline wires a chain[mirror:configName -> forward:sink] ->
// function -> pipeline -> plain-device topology.
//
// Each name in extraDevices gets its own input pipeline with a forward sink so
// mirrored packets re-routed via ModeIn reach the output stage. Must be called
// after applyRules.
func wireMirrorPipeline(
	t *testing.T,
	agent *ffi.Agent,
	primaryDevice, configName string,
	extraDevices []string,
) {
	t.Helper()

	fwdBackend := forward.NewBackend(agent)

	// Primary device sink: appended after the test module in the chain so
	// packets that pass through (originals and ModeNone mirrors) reach the
	// output stage.
	primarySink := configName + "-sink"
	primarySinkHandle, err := fwdBackend.UpdateModule(primarySink, catchAllForwardRules(primaryDevice))
	require.NoError(t, err)
	t.Cleanup(primarySinkHandle.Free)

	// Extra device sinks: each gets its own input pipeline with a forward
	// sink so mirrored packets re-routed via ModeIn reach the output stage.
	for _, dev := range extraDevices {
		sinkName := configName + "-sink-" + dev
		sinkHandle, err := fwdBackend.UpdateModule(sinkName, catchAllForwardRules(dev))
		require.NoError(t, err)
		t.Cleanup(sinkHandle.Free)

		require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
			Name: sinkName,
			Chains: []ffi.FunctionChainConfig{{
				Weight: 1,
				Chain: ffi.ChainConfig{
					Name: sinkName + "_chain",
					Modules: []ffi.ChainModuleConfig{
						{Type: "forward", Name: sinkName},
					},
				},
			}},
		}))
		require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
			Name:      "sink_in_" + dev,
			Functions: []string{sinkName},
		}))
	}

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: configName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: configName + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "mirror", Name: configName},
					{Type: "forward", Name: primarySink},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      configName,
		Functions: []string{configName},
	}))

	// A dummy pipeline with no functions passes packets straight through.
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name: "dummy",
	}))

	// Additional dummy output pipelines for extra devices — each output must
	// use a distinct pipeline name to avoid counter-key collisions.
	for _, dev := range extraDevices {
		require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
			Name: "dummy_extra_out_" + dev,
		}))
	}

	// Wire the primary ingress device with the mirror-module pipeline.
	primaryCfg := ffi.DeviceConfig{
		Name:   primaryDevice,
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}

	allDevices := []ffi.DeviceConfig{primaryCfg}
	for _, dev := range extraDevices {
		allDevices = append(allDevices, ffi.DeviceConfig{
			Name:   dev,
			Input:  []ffi.DevicePipelineConfig{{Name: "sink_in_" + dev, Weight: 1}},
			Output: []ffi.DevicePipelineConfig{{Name: "dummy_extra_out_" + dev, Weight: 1}},
		})
	}

	require.NoError(t, agent.UpdatePlainDevices(allDevices))
}

// mirEtherLayers returns shared Ethernet, IPv4, IPv6, and ICMPv4 layer
// templates for mirror functional tests.
func mirEtherLayers() (layers.Ethernet, layers.IPv4, layers.IPv6, layers.ICMPv4) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP("1.2.3.4"),
		DstIP:    net.ParseIP("10.0.0.5"),
	}
	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolICMPv6,
		SrcIP:      net.ParseIP("::1"),
		DstIP:      net.ParseIP("2001:db8::1"),
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}
	return eth, ip4, ip6, icmp
}

// TestMirror_NoMatch verifies that a packet not matched by any rule passes
// through to the next module unchanged.
//
// Covers branch: action == FILTER_RULE_INVALID -> packet_front_output (no target).
func TestMirror_NoMatch(t *testing.T) {
	// A rule with only Dst4s and no Src4s is excluded from the ip4 filter
	// by check_has_ip4 (which requires both src and dst). Such a rule also
	// fails check_mirror_rule_l2 (which rejects rules with any ip condition).
	// The rule therefore appears in no filter, so every packet misses it.
	noMatchRule := cmirror.MirrorRule{
		Target:  "port0",
		Mode:    cmirror.ModeNone,
		Counter: "unmatchable",
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/24")},
		// Src4s deliberately absent — makes the rule invisible to all filters.
	}

	eth, ip4, _, icmp := mirEtherLayers()
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)

	h, agent, backend := setupMirrorHarness(t, []string{"port0"})
	applyRules(t, backend, "test", []cmirror.MirrorRule{noMatchRule})
	wireMirrorPipeline(t, agent, "port0", "test", nil)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "unmatched packet passes through with no mirror copy")
	require.Empty(t, result.Drop, "unmatched packet must not be dropped")
}

// TestMirror_ModeNone_IPv4 verifies that an IPv4 packet matched by an ip4
// rule with ModeNone passes through to the next module without device redirect,
// and that the per-rule counter is incremented.
//
// Covers branch: target found, mode == MIRROR_MODE_NONE -> packet_front_output.
func TestMirror_ModeNone_IPv4(t *testing.T) {
	eth, ip4, _, icmp := mirEtherLayers()
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	pktSize := uint64(len(pkt.Data()))

	rule := cmirror.MirrorRule{
		Target:  "port0",
		Mode:    cmirror.ModeNone,
		Counter: "rule0",
		Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/24")},
	}

	h, agent, backend := setupMirrorHarness(t, []string{"port0"})
	applyRules(t, backend, "test", []cmirror.MirrorRule{rule})
	wireMirrorPipeline(t, agent, "port0", "test", nil)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "ModeNone packet passes through with no mirror copy")
	require.Empty(t, result.Drop, "ModeNone packet must not be dropped")

	path := dataplaneut.CounterPath{
		Device:     "port0",
		Pipeline:   "test",
		Function:   "test",
		Chain:      "test_chain",
		ModuleType: "mirror",
		ModuleName: "test",
	}
	dataplaneut.RequireModuleCounter(t, h, path, "rule0", 1, pktSize)
}

// TestMirror_ModeOut_IPv4 verifies that an IPv4 packet matched by a ModeOut
// rule is re-queued for egress via the target device, and that the per-rule
// counter is incremented.
//
// Covers branch: target found, mode == MIRROR_MODE_OUT -> set tx_device_id,
// queue to pending_output.
func TestMirror_ModeOut_IPv4(t *testing.T) {
	eth, ip4, _, icmp := mirEtherLayers()
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	pktSize := uint64(len(pkt.Data()))

	rule := cmirror.MirrorRule{
		Target:  "port1",
		Mode:    cmirror.ModeOut,
		Counter: "rule0",
		Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/24")},
	}

	h, agent, backend := setupMirrorHarness(t, []string{"port0", "port1"})
	applyRules(t, backend, "test", []cmirror.MirrorRule{rule})
	wireMirrorPipeline(t, agent, "port0", "test", []string{"port1"})

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	// The packet is queued via pending_output -> device_ectx_process_output on
	// port1 -> dummy output pipeline -> packet_front.output.
	require.Len(t, result.Output, 2, "re-routed ModeOut packet plus its mirror copy must reach output")
	require.Empty(t, result.Drop, "ModeOut packet with valid device must not be dropped")

	path := dataplaneut.CounterPath{
		Device:     "port0",
		Pipeline:   "test",
		Function:   "test",
		Chain:      "test_chain",
		ModuleType: "mirror",
		ModuleName: "test",
	}
	dataplaneut.RequireModuleCounter(t, h, path, "rule0", 1, pktSize)
}

// TestMirror_ModeIn_IPv4 verifies that an IPv4 packet matched by a ModeIn
// rule is re-queued for ingress via the target device, and that the per-rule
// counter is incremented.
//
// Covers branch: target found, mode == MIRROR_MODE_IN -> set tx_device_id,
// queue to pending_input.
func TestMirror_ModeIn_IPv4(t *testing.T) {
	eth, ip4, _, icmp := mirEtherLayers()
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	pktSize := uint64(len(pkt.Data()))

	rule := cmirror.MirrorRule{
		Target:  "port1",
		Mode:    cmirror.ModeIn,
		Counter: "rule0",
		Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/24")},
	}

	h, agent, backend := setupMirrorHarness(t, []string{"port0", "port1"})
	applyRules(t, backend, "test", []cmirror.MirrorRule{rule})
	// Port 1 gets a dummy input pipeline — the re-routed packet passes through
	// it unchanged and ends up in packet_front.output.
	wireMirrorPipeline(t, agent, "port0", "test", []string{"port1"})

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	// The packet traverses: pending_input -> device_ectx_process_input on
	// port1 -> dummy_extra_in_port1 pipeline -> packet_front.output.
	require.Len(t, result.Output, 2, "re-routed ModeIn packet plus its mirror copy must reach output")
	require.Empty(t, result.Drop, "ModeIn packet with valid device must not be dropped")

	path := dataplaneut.CounterPath{
		Device:     "port0",
		Pipeline:   "test",
		Function:   "test",
		Chain:      "test_chain",
		ModuleType: "mirror",
		ModuleName: "test",
	}
	dataplaneut.RequireModuleCounter(t, h, path, "rule0", 1, pktSize)
}

// TestMirror_UnmappedDevice verifies that a rule whose target device is not
// registered in UpdatePlainDevices leaves the matched packet untouched: the
// original still passes through and no mirror copy is produced.
//
// The per-rule counter is incremented BEFORE the device translation check,
// so the counter must show 1 even though no mirror copy is emitted.
//
// Covers branch: target found, module_ectx_encode_device returns -1 -> the
// mirror copy is skipped while the original is forwarded unconditionally.
func TestMirror_UnmappedDevice(t *testing.T) {
	eth, ip4, _, icmp := mirEtherLayers()
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	pktSize := uint64(len(pkt.Data()))

	// The "phantom" target is never registered via UpdatePlainDevices. Its
	// mc_index entry stays at the initial sentinel value of -1.
	rule := cmirror.MirrorRule{
		Target:  "phantom",
		Mode:    cmirror.ModeOut,
		Counter: "rule0",
		Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/24")},
	}

	h, agent, backend := setupMirrorHarness(t, []string{"port0"})
	applyRules(t, backend, "test", []cmirror.MirrorRule{rule})
	// Only port0 is wired. "phantom" has no cp_device entry.
	wireMirrorPipeline(t, agent, "port0", "test", nil)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "original passes through; unmapped mirror device yields no mirror copy")
	require.Empty(t, result.Drop, "original is never dropped, even when the mirror device is unmapped")

	// Counter is bumped before the device translation check.
	path := dataplaneut.CounterPath{
		Device:     "port0",
		Pipeline:   "test",
		Function:   "test",
		Chain:      "test_chain",
		ModuleType: "mirror",
		ModuleName: "test",
	}
	dataplaneut.RequireModuleCounter(t, h, path, "rule0", 1, pktSize)
}

// TestMirror_IPv6_ModeOut verifies that an IPv6 packet matched by an ip6
// rule with ModeOut is re-queued for egress via the target device, and that
// the per-rule counter is incremented.
//
// Covers the filter_ip6 path through mirror_handle_packets.
func TestMirror_IPv6_ModeOut(t *testing.T) {
	eth, _, ip6, _ := mirEtherLayers()
	eth.EthernetType = layers.EthernetTypeIPv6
	icmp6 := layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	icmp6.SetNetworkLayerForChecksum(&ip6)

	pkt := xpacket.LayersToPacket(t, &eth, &ip6, &icmp6)
	pktSize := uint64(len(pkt.Data()))

	rule := cmirror.MirrorRule{
		Target:  "port1",
		Mode:    cmirror.ModeOut,
		Counter: "rule0",
		Src6s:   filter.IPNets{filter.UnspecifiedIPv6},
		Dst6s:   filter.IPNets{filter.MustParseIPNet("2001:db8::/32")},
	}

	h, agent, backend := setupMirrorHarness(t, []string{"port0", "port1"})
	applyRules(t, backend, "test", []cmirror.MirrorRule{rule})
	wireMirrorPipeline(t, agent, "port0", "test", []string{"port1"})

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 2, "IPv6 ModeOut packet plus its mirror copy must reach output")
	require.Empty(t, result.Drop, "IPv6 ModeOut packet with valid device must not be dropped")

	path := dataplaneut.CounterPath{
		Device:     "port0",
		Pipeline:   "test",
		Function:   "test",
		Chain:      "test_chain",
		ModuleType: "mirror",
		ModuleName: "test",
	}
	dataplaneut.RequireModuleCounter(t, h, path, "rule0", 1, pktSize)
}

// TestMirror_NonIP verifies that a non-IP packet (ARP) is matched by a
// device-only rule that appears in the L2 (vlan) filter.
//
// Non-IP packets skip the ip4 and ip6 filter branches entirely. The vlan
// filter result is used as the final action. A device-only rule (no ip
// conditions) qualifies for the L2 filter via check_mirror_rule_l2.
//
// Covers the path: vlan_result valid, neither IPv4 nor IPv6 branch taken,
// action = vlan_result -> target found -> mode == MIRROR_MODE_NONE -> passthrough.
func TestMirror_NonIP(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("ff:ff:ff:ff:ff:ff")),
		EthernetType: layers.EthernetTypeARP,
	}
	arp := layers.ARP{
		AddrType:          layers.LinkTypeEthernet,
		Protocol:          layers.EthernetTypeIPv4,
		HwAddressSize:     6,
		ProtAddressSize:   4,
		Operation:         layers.ARPRequest,
		SourceHwAddress:   eth.SrcMAC,
		SourceProtAddress: net.ParseIP("10.0.0.1").To4(),
		DstHwAddress:      net.HardwareAddr{0, 0, 0, 0, 0, 0},
		DstProtAddress:    net.ParseIP("10.0.0.2").To4(),
	}
	pkt := xpacket.LayersToPacket(t, &eth, &arp)
	pktSize := uint64(len(pkt.Data()))

	// A rule with no ip conditions qualifies for the L2 filter only. An ARP
	// packet will match it because neither ip4 nor ip6 filter branches are
	// entered — the final action stays at vlan_result.
	rule := cmirror.MirrorRule{
		Target:  "port0",
		Mode:    cmirror.ModeNone,
		Counter: "l2rule",
		Devices: filter.Devices{{Name: "port0"}},
		// No Src4s/Dst4s/Src6s/Dst6s — L2-only rule.
	}

	h, agent, backend := setupMirrorHarness(t, []string{"port0"})
	applyRules(t, backend, "test", []cmirror.MirrorRule{rule})
	wireMirrorPipeline(t, agent, "port0", "test", nil)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "ARP passthrough packet has no mirror copy")
	require.Empty(t, result.Drop, "ARP packet must not be dropped")

	path := dataplaneut.CounterPath{
		Device:     "port0",
		Pipeline:   "test",
		Function:   "test",
		Chain:      "test_chain",
		ModuleType: "mirror",
		ModuleName: "test",
	}
	dataplaneut.RequireModuleCounter(t, h, path, "l2rule", 1, pktSize)
}

// TestMirror_MinAction verifies that when a packet matches both the L2 filter
// (rule 0) and the ip4 filter (rule 1), the handler picks the lower action
// value via min(vlan_result, ip4_result), so only rule 0's counter is bumped.
//
// Rule 0: device-only (L2 filter, action 0).
// Rule 1: ip4 prefix rule (ip4 filter, action 1).
// An IPv4 packet satisfies both filters. action = min(0, 1) = 0 -> rule 0 wins.
//
// Covers branch: both vlan_result and ip4_result valid -> action = min ->
// lower-index rule's target is used.
func TestMirror_MinAction(t *testing.T) {
	eth, ip4, _, icmp := mirEtherLayers()
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	pktSize := uint64(len(pkt.Data()))

	rules := []cmirror.MirrorRule{
		{
			// Rule 0: L2-only. Qualifies for filter_vlan (no ip conditions).
			// An IPv4 packet with device "port0" matches here -> vlan_result = 0.
			Target:  "port0",
			Mode:    cmirror.ModeNone,
			Counter: "l2win",
			Devices: filter.Devices{{Name: "port0"}},
		},
		{
			// Rule 1: ip4-only. Qualifies for filter_ip4 (has both src and dst).
			// The same IPv4 packet matches here -> ip4_result = 1.
			// min(0, 1) = 0, so rule 1 never fires.
			Target:  "port0",
			Mode:    cmirror.ModeNone,
			Counter: "ip4lose",
			Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
			Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/24")},
		},
	}

	h, agent, backend := setupMirrorHarness(t, []string{"port0"})
	applyRules(t, backend, "test", rules)
	wireMirrorPipeline(t, agent, "port0", "test", nil)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "passthrough packet has no mirror copy")
	require.Empty(t, result.Drop, "packet must not be dropped")

	// Rule 0 counter must be 1 — rule 1 counter must be 0 (never selected).
	path := dataplaneut.CounterPath{
		Device:     "port0",
		Pipeline:   "test",
		Function:   "test",
		Chain:      "test_chain",
		ModuleType: "mirror",
		ModuleName: "test",
	}
	dataplaneut.RequireModuleCounter(t, h, path, "l2win", 1, pktSize)
	dataplaneut.RequireModuleCounter(t, h, path, "ip4lose", 0, 0)
}

// TestMirror_EmptyRound verifies that a force-poll round with no packets at
// all passes through mirror_handle_packets harmlessly.
//
// The empty round models a force-poll tick with no traffic: the worker
// invokes mirror_handle_packets even though the input front is empty. This
// cannot observe any sanitizer diagnostic on its own, so it only checks that
// the round completes cleanly and leaves the module's state untouched.
func TestMirror_EmptyRound(t *testing.T) {
	rule := cmirror.MirrorRule{
		Target:  "port0",
		Mode:    cmirror.ModeNone,
		Counter: "rule0",
		Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/24")},
	}

	h, agent, backend := setupMirrorHarness(t, []string{"port0"})
	applyRules(t, backend, "test", []cmirror.MirrorRule{rule})
	wireMirrorPipeline(t, agent, "port0", "test", nil)

	path := dataplaneut.CounterPath{
		Device:     "port0",
		Pipeline:   "test",
		Function:   "test",
		Chain:      "test_chain",
		ModuleType: "mirror",
		ModuleName: "test",
	}

	result, err := h.HandlePackets()
	require.NoError(t, err)
	require.Empty(t, result.Output, "empty round produces no output")
	require.Empty(t, result.Drop, "empty round produces no drops")
	dataplaneut.RequireModuleCounter(t, h, path, "rule0", 0, 0)
}

// TestMirrorConfigMemoryLeak verifies the block allocator returns memory once a
// superseded mirror config generation is reclaimed.
//
// THE MAGIC: 8 rules make sizeof(mirror_target)*8 = 192 and
// sizeof(mirror_target*)*8 = 64 land in different block-allocator pools even
// under ASAN red zones; fewer rules round both into the same pool and hide a
// leak in either array from BlockAllocatorFreeSize.
//
// Releasing a handle only parks it: reclaim happens on the type's next
// construction, not on release. Each round below publishes and releases one
// "mirror0" generation, and that construction drains the round before it, so
// from round 1 the arena holds two generations' worth of rules and every
// round's free byte count must match the last.
func TestMirrorConfigMemoryLeak(t *testing.T) {
	_, agent, _ := setupMirrorHarness(t, []string{"port0"})

	rules := make([]cmirror.MirrorRule, 8)
	for idx := range rules {
		rules[idx] = cmirror.MirrorRule{
			Target:  "port0",
			Mode:    cmirror.ModeIn,
			Counter: fmt.Sprintf("rule%d", idx),
		}
	}

	var baseline uint64
	for round := range 4 {
		module, err := cmirror.NewModuleConfig(agent, "mirror0")
		require.NoErrorf(t, err, "round %d: construct failed", round)
		require.NoErrorf(t, module.Update(rules), "round %d: update failed", round)
		require.NoErrorf(
			t,
			agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}),
			"round %d: publish failed", round,
		)
		module.Free()

		if round == 0 {
			// Nothing is parked yet for this round's construction to
			// drain, so this round's shape isn't comparable to later ones.
			continue
		}

		free := agent.BlockAllocatorFreeSize()
		if round == 1 {
			baseline = free
		} else {
			require.Equalf(
				t, baseline, free,
				"round %d: free bytes drifted from the round-1 baseline", round,
			)
		}
	}
}
