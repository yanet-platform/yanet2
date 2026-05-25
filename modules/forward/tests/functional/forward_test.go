package forward_test

import (
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
)

// Memory sizes for the forward functional harness.
const (
	fwdCPSize  = 64 * datasize.MB
	fwdDPSize  = 4 * datasize.MB
	fwdMemSize = 8 * datasize.MB
)

// setupForwardHarness builds a dataplane harness with the forward module
// loaded and attaches a control-plane agent.
//
// devices is the set of logical port names to register in the harness
// topology. The first device in the list serves as the primary ingress port.
// Cleanup is wired via t.Cleanup in LIFO order.
func setupForwardHarness(
	t *testing.T,
	devices []string,
) (*dataplaneut.Harness, *ffi.Agent, forward.Backend) {
	t.Helper()

	cfg := dataplaneut.Config{
		CPMemory:      uint64(fwdCPSize),
		DPMemory:      uint64(fwdDPSize),
		WorkerCount:   1,
		Devices:       devices,
		Modules:       []string{"forward"},
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("fwd-test", 0, fwdMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	backend := forward.NewBackend(agent)
	return h, agent, backend
}

// applyRules pushes the given rules via backend.UpdateModule.
//
// The module handle is freed via t.Cleanup. The caller must wire the pipeline
// after calling applyRules, because the forward module config must exist in
// shared memory before UpdatePlainDevices resolves chain module references.
func applyRules(
	t *testing.T,
	backend forward.Backend,
	name string,
	rules []cforward.ForwardRule,
) forward.ModuleHandle {
	t.Helper()

	handle, err := backend.UpdateModule(name, rules)
	require.NoError(t, err)
	t.Cleanup(handle.Free)
	return handle
}

// wireForwardPipeline wires a chain[forward:configName] -> function -> pipeline
// -> plain-device topology.
//
// Each name in extraDevices gets its own dummy input/output pipelines so that
// ModeIn and ModeOut packet re-routing resolves without looping. Must be
// called after applyRules.
func wireForwardPipeline(
	t *testing.T,
	agent *ffi.Agent,
	primaryDevice, configName string,
	extraDevices []string,
) {
	t.Helper()

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: configName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: configName + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "forward", Name: configName},
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

	// Additional dummy pipelines for extra devices — each output must use a
	// distinct pipeline name to avoid counter-key collisions.
	for _, dev := range extraDevices {
		pipeName := "dummy_extra_out_" + dev
		require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
			Name: pipeName,
		}))
		pipeName2 := "dummy_extra_in_" + dev
		require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
			Name: pipeName2,
		}))
	}

	// Wire the primary ingress device with the forward-module pipeline.
	primaryCfg := ffi.DeviceConfig{
		Name:   primaryDevice,
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}

	allDevices := []ffi.DeviceConfig{primaryCfg}
	for _, dev := range extraDevices {
		allDevices = append(allDevices, ffi.DeviceConfig{
			Name:   dev,
			Input:  []ffi.DevicePipelineConfig{{Name: "dummy_extra_in_" + dev, Weight: 1}},
			Output: []ffi.DevicePipelineConfig{{Name: "dummy_extra_out_" + dev, Weight: 1}},
		})
	}

	require.NoError(t, agent.UpdatePlainDevices(allDevices))
}

// fwdEtherLayers returns shared Ethernet, IPv4, IPv6, and ICMPv4 layer
// templates for forward functional tests.
func fwdEtherLayers() (layers.Ethernet, layers.IPv4, layers.IPv6, layers.ICMPv4) {
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

// TestForward_NoMatch verifies that a packet not matched by any rule passes
// through to the next module unchanged.
//
// Covers branch: action == FILTER_RULE_INVALID -> packet_front_output (no target).
func TestForward_NoMatch(t *testing.T) {
	// A rule with only Dst4s and no Src4s is excluded from the ip4 filter
	// by check_has_ip4 (which requires both src and dst). Such a rule also
	// fails check_forward_rule_l2 (which rejects rules with any ip condition).
	// The rule therefore appears in no filter, so every packet misses it.
	noMatchRule := cforward.ForwardRule{
		Target:  "port0",
		Mode:    cforward.ModeNone,
		Counter: "unmatchable",
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/24")},
		// Src4s deliberately absent — makes the rule invisible to all filters.
	}

	eth, ip4, _, icmp := fwdEtherLayers()
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)

	h, agent, backend := setupForwardHarness(t, []string{"port0"})
	applyRules(t, backend, "test", []cforward.ForwardRule{noMatchRule})
	wireForwardPipeline(t, agent, "port0", "test", nil)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "unmatched packet must pass through")
	require.Empty(t, result.Drop, "unmatched packet must not be dropped")
}

// TestForward_ModeNone_IPv4 verifies that an IPv4 packet matched by an ip4
// rule with ModeNone passes through to the next module without device redirect,
// and that the per-rule counter is incremented.
//
// Covers branch: target found, mode == FORWARD_MODE_NONE -> packet_front_output.
func TestForward_ModeNone_IPv4(t *testing.T) {
	eth, ip4, _, icmp := fwdEtherLayers()
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	pktSize := uint64(len(pkt.Data()))

	rule := cforward.ForwardRule{
		Target:  "port0",
		Mode:    cforward.ModeNone,
		Counter: "rule0",
		Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/24")},
	}

	h, agent, backend := setupForwardHarness(t, []string{"port0"})
	applyRules(t, backend, "test", []cforward.ForwardRule{rule})
	wireForwardPipeline(t, agent, "port0", "test", nil)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "ModeNone packet must pass through")
	require.Empty(t, result.Drop, "ModeNone packet must not be dropped")

	path := dataplaneut.CounterPath{
		Device:     "port0",
		Pipeline:   "test",
		Function:   "test",
		Chain:      "test_chain",
		ModuleType: "forward",
		ModuleName: "test",
	}
	dataplaneut.RequireModuleCounter(t, h, path, "rule0", 1, pktSize)
}

// TestForward_ModeOut_IPv4 verifies that an IPv4 packet matched by a ModeOut
// rule is re-queued for egress via the target device, and that the per-rule
// counter is incremented.
//
// Covers branch: target found, mode == FORWARD_MODE_OUT -> set tx_device_id,
// queue to pending_output.
func TestForward_ModeOut_IPv4(t *testing.T) {
	eth, ip4, _, icmp := fwdEtherLayers()
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	pktSize := uint64(len(pkt.Data()))

	rule := cforward.ForwardRule{
		Target:  "port1",
		Mode:    cforward.ModeOut,
		Counter: "rule0",
		Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/24")},
	}

	h, agent, backend := setupForwardHarness(t, []string{"port0", "port1"})
	applyRules(t, backend, "test", []cforward.ForwardRule{rule})
	wireForwardPipeline(t, agent, "port0", "test", []string{"port1"})

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	// The packet is queued via pending_output -> device_ectx_process_output on
	// port1 -> dummy output pipeline -> packet_front.output.
	require.Len(t, result.Output, 1, "ModeOut packet must reach output after egress pipeline")
	require.Empty(t, result.Drop, "ModeOut packet with valid device must not be dropped")

	path := dataplaneut.CounterPath{
		Device:     "port0",
		Pipeline:   "test",
		Function:   "test",
		Chain:      "test_chain",
		ModuleType: "forward",
		ModuleName: "test",
	}
	dataplaneut.RequireModuleCounter(t, h, path, "rule0", 1, pktSize)
}

// TestForward_ModeIn_IPv4 verifies that an IPv4 packet matched by a ModeIn
// rule is re-queued for ingress via the target device, and that the per-rule
// counter is incremented.
//
// Covers branch: target found, mode == FORWARD_MODE_IN -> set tx_device_id,
// queue to pending_input.
func TestForward_ModeIn_IPv4(t *testing.T) {
	eth, ip4, _, icmp := fwdEtherLayers()
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	pktSize := uint64(len(pkt.Data()))

	rule := cforward.ForwardRule{
		Target:  "port1",
		Mode:    cforward.ModeIn,
		Counter: "rule0",
		Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/24")},
	}

	h, agent, backend := setupForwardHarness(t, []string{"port0", "port1"})
	applyRules(t, backend, "test", []cforward.ForwardRule{rule})
	// port1 gets a dummy input pipeline — the re-routed packet passes through
	// it unchanged and ends up in packet_front.output.
	wireForwardPipeline(t, agent, "port0", "test", []string{"port1"})

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	// The packet traverses: pending_input -> device_ectx_process_input on
	// port1 -> dummy_extra_in_port1 pipeline -> packet_front.output.
	require.Len(t, result.Output, 1, "ModeIn packet must reach output after ingress re-route")
	require.Empty(t, result.Drop, "ModeIn packet with valid device must not be dropped")

	path := dataplaneut.CounterPath{
		Device:     "port0",
		Pipeline:   "test",
		Function:   "test",
		Chain:      "test_chain",
		ModuleType: "forward",
		ModuleName: "test",
	}
	dataplaneut.RequireModuleCounter(t, h, path, "rule0", 1, pktSize)
}

// TestForward_UnmappedDevice verifies that a rule whose target device is not
// registered in UpdatePlainDevices causes the matched packet to be dropped.
//
// The per-rule counter is incremented BEFORE the device translation check,
// so the counter must show 1 even though the packet is dropped.
//
// Covers branch: target found, module_ectx_encode_device returns -1 -> drop.
func TestForward_UnmappedDevice(t *testing.T) {
	eth, ip4, _, icmp := fwdEtherLayers()
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	pktSize := uint64(len(pkt.Data()))

	// "phantom" is never registered via UpdatePlainDevices; its mc_index
	// entry stays at the initial sentinel value of -1.
	rule := cforward.ForwardRule{
		Target:  "phantom",
		Mode:    cforward.ModeOut,
		Counter: "rule0",
		Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/24")},
	}

	h, agent, backend := setupForwardHarness(t, []string{"port0"})
	applyRules(t, backend, "test", []cforward.ForwardRule{rule})
	// Only port0 is wired; "phantom" has no cp_device entry.
	wireForwardPipeline(t, agent, "port0", "test", nil)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, result.Output, "packet with unmapped device must be dropped")
	require.Len(t, result.Drop, 1, "expected exactly one dropped packet")

	// Counter is bumped before the device translation check.
	path := dataplaneut.CounterPath{
		Device:     "port0",
		Pipeline:   "test",
		Function:   "test",
		Chain:      "test_chain",
		ModuleType: "forward",
		ModuleName: "test",
	}
	dataplaneut.RequireModuleCounter(t, h, path, "rule0", 1, pktSize)
}

// TestForward_IPv6_ModeOut verifies that an IPv6 packet matched by an ip6
// rule with ModeOut is re-queued for egress via the target device, and that
// the per-rule counter is incremented.
//
// Covers the filter_ip6 path through forward_handle_packets.
func TestForward_IPv6_ModeOut(t *testing.T) {
	eth, _, ip6, _ := fwdEtherLayers()
	eth.EthernetType = layers.EthernetTypeIPv6
	icmp6 := layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	icmp6.SetNetworkLayerForChecksum(&ip6)

	pkt := xpacket.LayersToPacket(t, &eth, &ip6, &icmp6)
	pktSize := uint64(len(pkt.Data()))

	rule := cforward.ForwardRule{
		Target:  "port1",
		Mode:    cforward.ModeOut,
		Counter: "rule0",
		Src6s:   filter.IPNets{filter.UnspecifiedIPv6},
		Dst6s:   filter.IPNets{filter.MustParseIPNet("2001:db8::/32")},
	}

	h, agent, backend := setupForwardHarness(t, []string{"port0", "port1"})
	applyRules(t, backend, "test", []cforward.ForwardRule{rule})
	wireForwardPipeline(t, agent, "port0", "test", []string{"port1"})

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "IPv6 ModeOut packet must reach output after egress pipeline")
	require.Empty(t, result.Drop, "IPv6 ModeOut packet with valid device must not be dropped")

	path := dataplaneut.CounterPath{
		Device:     "port0",
		Pipeline:   "test",
		Function:   "test",
		Chain:      "test_chain",
		ModuleType: "forward",
		ModuleName: "test",
	}
	dataplaneut.RequireModuleCounter(t, h, path, "rule0", 1, pktSize)
}

// TestForward_NonIP verifies that a non-IP packet (ARP) is matched by a
// device-only rule that appears in the L2 (vlan) filter.
//
// Non-IP packets skip the ip4 and ip6 filter branches entirely. The vlan
// filter result is used as the final action. A device-only rule (no ip
// conditions) qualifies for the L2 filter via check_forward_rule_l2.
//
// Covers the path: vlan_result valid, neither IPv4 nor IPv6 branch taken,
// action = vlan_result -> target found -> mode == FORWARD_MODE_NONE -> passthrough.
func TestForward_NonIP(t *testing.T) {
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
	rule := cforward.ForwardRule{
		Target:  "port0",
		Mode:    cforward.ModeNone,
		Counter: "l2rule",
		Devices: filter.Devices{{Name: "port0"}},
		// No Src4s/Dst4s/Src6s/Dst6s — L2-only rule.
	}

	h, agent, backend := setupForwardHarness(t, []string{"port0"})
	applyRules(t, backend, "test", []cforward.ForwardRule{rule})
	wireForwardPipeline(t, agent, "port0", "test", nil)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "ARP packet matching L2 rule with ModeNone must pass through")
	require.Empty(t, result.Drop, "ARP packet must not be dropped")

	path := dataplaneut.CounterPath{
		Device:     "port0",
		Pipeline:   "test",
		Function:   "test",
		Chain:      "test_chain",
		ModuleType: "forward",
		ModuleName: "test",
	}
	dataplaneut.RequireModuleCounter(t, h, path, "l2rule", 1, pktSize)
}

// TestForward_MinAction verifies that when a packet matches both the L2 filter
// (rule 0) and the ip4 filter (rule 1), the handler picks the lower action
// value via min(vlan_result, ip4_result), so only rule 0's counter is bumped.
//
// Rule 0: device-only (L2 filter, action 0).
// Rule 1: ip4 prefix rule (ip4 filter, action 1).
// An IPv4 packet satisfies both filters. action = min(0, 1) = 0 -> rule 0 wins.
//
// Covers branch: both vlan_result and ip4_result valid -> action = min ->
// lower-index rule's target is used.
func TestForward_MinAction(t *testing.T) {
	eth, ip4, _, icmp := fwdEtherLayers()
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	pktSize := uint64(len(pkt.Data()))

	rules := []cforward.ForwardRule{
		{
			// Rule 0: L2-only. Qualifies for filter_vlan (no ip conditions).
			// An IPv4 packet with device "port0" matches here -> vlan_result = 0.
			Target:  "port0",
			Mode:    cforward.ModeNone,
			Counter: "l2win",
			Devices: filter.Devices{{Name: "port0"}},
		},
		{
			// Rule 1: ip4-only. Qualifies for filter_ip4 (has both src and dst).
			// The same IPv4 packet matches here -> ip4_result = 1.
			// min(0, 1) = 0, so rule 1 never fires.
			Target:  "port0",
			Mode:    cforward.ModeNone,
			Counter: "ip4lose",
			Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
			Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/24")},
		},
	}

	h, agent, backend := setupForwardHarness(t, []string{"port0"})
	applyRules(t, backend, "test", rules)
	wireForwardPipeline(t, agent, "port0", "test", nil)

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "packet must pass through via rule 0 (ModeNone)")
	require.Empty(t, result.Drop, "packet must not be dropped")

	// Rule 0 counter must be 1; rule 1 counter must be 0 (never selected).
	path := dataplaneut.CounterPath{
		Device:     "port0",
		Pipeline:   "test",
		Function:   "test",
		Chain:      "test_chain",
		ModuleType: "forward",
		ModuleName: "test",
	}
	dataplaneut.RequireModuleCounter(t, h, path, "l2win", 1, pktSize)
	dataplaneut.RequireModuleCounter(t, h, path, "ip4lose", 0, 0)
}
