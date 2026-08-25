// Package pipeline_test holds regression tests for the dataplane pipeline
// dispatch logic implemented in lib/dataplane/pipeline/pipeline.c.
//
// The tests here lock in the correctness rules of the single-chain fast path
// added to function_ectx_process and of its device-entry counterpart added to
// device_entry_ectx_dispatch:
//
//  1. A function whose chains are all zero-weight must DROP all packets
//     rather than route them to a disabled chain.
//
//  2. The single-chain fast path runs each function on its own packet front,
//     so two single-chain functions chained in one pipeline hand packets from
//     the first to the second intact and drop exactly what the chain drops.
//
//  3. A device entry whose pipelines are all zero-weight must DROP all packets
//     rather than route them to a disabled pipeline.
//
//  4. The single-pipeline fast path delivers the whole batch to the one bound
//     pipeline intact.
//
//  5. Power-of-two padding of the chain and pipeline routing tables never
//     routes traffic to a zero-weight destination, and the demux delivers
//     every packet to exactly one enabled destination.
//
//  6. Destinations whose index falls outside the demux local-tail cache are
//     still routed correctly through the direct-append fallback.
package pipeline_test

import (
	"fmt"
	"net"
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
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
)

// Memory sizes for the dispatch regression harness.
//
// They are generous because a single test compiles two ACL modules in one
// agent arena, and the ACL filter compilation must still fit under sanitizer
// (ASan redzone) builds.
const (
	dispatchCPSize  = 256 * datasize.MB
	dispatchDPSize  = 16 * datasize.MB
	dispatchMemSize = 128 * datasize.MB
)

// dispatchUDPPacket builds a single Ethernet/IPv4/UDP packet whose source and
// destination addresses fall in the ranges used by the test rules.
func dispatchUDPPacket(t *testing.T) gopacket.Packet {
	t.Helper()

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP("192.0.2.1"),
		DstIP:    net.ParseIP("10.0.0.1"),
	}
	udp := layers.UDP{SrcPort: 12345, DstPort: 80}
	require.NoError(t, udp.SetNetworkLayerForChecksum(&ip4))
	return xpacket.LayersToPacket(t, &eth, &ip4, &udp)
}

// setupACLBackend creates a single-device harness with the acl module loaded
// and returns it together with an attached agent and ACL backend.
func setupACLBackend(
	t *testing.T,
	agentName string,
	extraModules ...string,
) (*dataplaneut.Harness, *ffi.Agent, acl.Backend) {
	t.Helper()

	modules := append([]string{"acl"}, extraModules...)
	cfg := dataplaneut.Config{
		CPMemory:      uint64(dispatchCPSize),
		DPMemory:      uint64(dispatchDPSize),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       modules,
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	agent, err := h.SharedMemory().AgentAttach(agentName, 0, dispatchMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	backend := acl.NewBackend(agent)
	return h, agent, backend
}

// publishMatchAllACL creates an ACL module whose single rule matches every
// IPv4 UDP packet with the given action and publishes it to shared memory.
//
// The created handle is freed via t.Cleanup.
func publishMatchAllACL(t *testing.T, backend acl.Backend, name string, action uint32) {
	t.Helper()

	rule := cacl.AclRule{
		Actions:       []cacl.AclAction{{Kind: action}},
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Dst4s:         []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Src6s:         []xnetip.BiContiguous{},
		Dst6s:         []xnetip.BiContiguous{},
		SrcPortRanges: filter.PortRanges{{From: 0, To: 65535}},
		DstPortRanges: filter.PortRanges{{From: 0, To: 65535}},
		ProtoRanges: filter.ProtoRanges{
			filter.NewProtoRange(uint8(layers.IPProtocolUDP), filter.AnySubtype()),
		},
		Fragment: filter.FragmentAny,
	}
	handle, err := backend.NewModule(name, []cacl.AclRule{rule}, "", "", nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Free() })
	require.NoError(t, backend.UpdateModule(handle))
}

// TestZeroWeightFunctionDropsAllPackets verifies that a function whose only
// chain has weight 0 drops every packet instead of running its disabled chain.
//
// The chain is an allow-all ACL that would forward every packet if it ran, so
// the drop is observable as an empty output. Without the dispatch guard a
// zero-weight chain would instead process traffic.
func TestZeroWeightFunctionDropsAllPackets(t *testing.T) {
	const configName = "zw-acl"

	h, agent, backend := setupACLBackend(t, "zw-test")
	publishMatchAllACL(t, backend, configName, cacl.ActionAllow)

	// Register an ACL function whose single chain has weight 0, which the
	// dataplane treats as fully disabled.
	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: configName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 0,
			Chain: ffi.ChainConfig{
				Name:    configName + "_chain",
				Modules: []ffi.ChainModuleConfig{{Type: "acl", Name: configName}},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      configName,
		Functions: []string{configName},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	_, err := plain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)

	const packetCount = 4
	pkts := make([]gopacket.Packet, packetCount)
	for idx := range packetCount {
		pkts[idx] = dispatchUDPPacket(t)
	}

	result, err := h.HandlePackets(pkts...)
	require.NoError(t, err)
	require.Empty(t, result.Output,
		"zero-weight function must not forward any packet to output")
	require.Len(t, result.Drop, packetCount,
		"zero-weight function must drop all packets")
}

// TestSequentialSingleChainFunctions verifies that two single-chain functions
// chained in one pipeline route packets correctly through the single-chain
// fast path.
//
// The pipeline runs funcA (ACL allow-all) followed by funcB (ACL deny-all),
// both single-chain functions. funcA forwards all N packets to funcB, which
// drops every one, so the egress output is empty. funcB's input counter must
// equal N, confirming the fast path handed funcA's output to funcB intact
// rather than losing or smuggling packets.
func TestSequentialSingleChainFunctions(t *testing.T) {
	const (
		aclA       = "allow-all"
		aclB       = "deny-all"
		pipelineAB = "pipe-ab"
	)

	h, agent, backend := setupACLBackend(t, "pf-test")

	// The funcA function allows every matching UDP IPv4 packet.
	// The funcB function then drops them all.
	publishMatchAllACL(t, backend, aclA, cacl.ActionAllow)
	publishMatchAllACL(t, backend, aclB, cacl.ActionDeny)

	// Wire both single-chain functions into one pipeline in order.
	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: aclA,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    aclA + "_chain",
				Modules: []ffi.ChainModuleConfig{{Type: "acl", Name: aclA}},
			},
		}},
	}))
	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: aclB,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    aclB + "_chain",
				Modules: []ffi.ChainModuleConfig{{Type: "acl", Name: aclB}},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      pipelineAB,
		Functions: []string{aclA, aclB},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	_, err := plain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: pipelineAB, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)

	const packetCount = 5
	pkts := make([]gopacket.Packet, packetCount)
	for idx := range packetCount {
		pkts[idx] = dispatchUDPPacket(t)
	}

	result, err := h.HandlePackets(pkts...)
	require.NoError(t, err)
	require.Empty(t, result.Output,
		"allow-then-deny pipeline must not forward any packet to egress")
	require.Len(t, result.Drop, packetCount,
		"allow-then-deny pipeline must drop all packets")

	// The funcB input counter must equal N: the single-chain fast path handed
	// funcA's forwarded packets to funcB intact.
	counters := h.SharedMemory().DPConfig(0).FunctionCounters("port0", pipelineAB, aclB)
	byName := dataplaneut.SingleValueCounters(counters)
	require.Equal(t, uint64(packetCount), byName["input"],
		"funcB must receive exactly the packets funcA forwarded (all N)")
}

// TestZeroWeightDeviceEntryDropsAllPackets verifies that a device entry whose
// only input pipeline has weight 0 drops every packet instead of routing it to
// the disabled pipeline.
//
// The pipeline is an allow-all ACL that would forward every packet if it ran,
// so the drop is observable as an empty output. This is the device-entry analog
// of the zero-weight function rule: a zero pipeline-map size must drop, not
// dispatch.
func TestZeroWeightDeviceEntryDropsAllPackets(t *testing.T) {
	const configName = "zw-dev-acl"

	h, agent, backend := setupACLBackend(t, "zw-dev-test")
	publishMatchAllACL(t, backend, configName, cacl.ActionAllow)

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: configName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    configName + "_chain",
				Modules: []ffi.ChainModuleConfig{{Type: "acl", Name: configName}},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      configName,
		Functions: []string{configName},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))

	// Bind the input pipeline with weight 0, which leaves the device entry's
	// pipeline-map size at 0, so every packet is unroutable.
	_, err := plain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 0}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)

	const packetCount = 4
	pkts := make([]gopacket.Packet, packetCount)
	for idx := range packetCount {
		pkts[idx] = dispatchUDPPacket(t)
	}

	result, err := h.HandlePackets(pkts...)
	require.NoError(t, err)
	require.Empty(t, result.Output,
		"zero-weight device entry must not forward any packet to output")
	require.Len(t, result.Drop, packetCount,
		"zero-weight device entry must drop all packets")
}

// TestSinglePipelineDeviceForwardsAllPackets verifies that a device entry with a
// single bound pipeline delivers every packet to that pipeline via the
// single-pipeline fast path, rather than losing or duplicating any.
//
// The pipeline chain allows every packet through an ACL and then forwards it to
// the device's output stage via a forward rule, so reaching egress proves the
// fast path delivered the whole batch intact. The input entry point is not
// allowed to transmit directly: only packets routed to the output stage survive.
// Accordingly the function input counter must equal the number of packets handed
// in.
func TestSinglePipelineDeviceForwardsAllPackets(t *testing.T) {
	const configName = "sp-dev-acl"
	const forwardName = configName + "-fwd"

	h, agent, backend := setupACLBackend(t, "sp-dev-test", "forward")
	publishMatchAllACL(t, backend, configName, cacl.ActionAllow)

	forwardBackend := forward.NewBackend(agent)
	rule := cforward.ForwardRule{
		Target:  "port0",
		Mode:    cforward.ModeOut,
		Counter: forwardName,
		Src4s:   []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Dst4s:   []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("10.0.0.0/8")},
	}
	handle, err := forwardBackend.UpdateModule(
		forwardName, []cforward.ForwardRule{rule},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Free() })

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: configName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: configName + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "acl", Name: configName},
					{Type: "forward", Name: forwardName},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      configName,
		Functions: []string{configName},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	_, err = plain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)

	const packetCount = 5
	pkts := make([]gopacket.Packet, packetCount)
	for idx := range packetCount {
		pkts[idx] = dispatchUDPPacket(t)
	}

	result, err := h.HandlePackets(pkts...)
	require.NoError(t, err)
	require.Len(t, result.Output, packetCount,
		"single-pipeline device must forward every packet to egress")
	require.Empty(t, result.Drop,
		"single-pipeline device must not drop any packet")

	counters := h.SharedMemory().DPConfig(0).FunctionCounters("port0", configName, configName)
	byName := dataplaneut.SingleValueCounters(counters)
	require.Equal(t, uint64(packetCount), byName["input"],
		"single-pipeline device dispatch must deliver every packet to the pipeline")
}

// TestEmptyPipelineDeviceDropsAllPackets verifies that a device entry with no
// bound input pipelines drops every packet without entering the demux.
//
// This is the no-pipeline edge of the zero-weight rule: there is nothing to
// route to and nothing to force-poll, so the entry must drop and return rather
// than size a zero-length scheduling array in the demux.
func TestEmptyPipelineDeviceDropsAllPackets(t *testing.T) {
	h, agent, _ := setupACLBackend(t, "empty-dev-test")

	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	// The device input binds no pipeline at all, so the entry has nothing to
	// dispatch to.
	_, err := plain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)

	const packetCount = 4
	pkts := make([]gopacket.Packet, packetCount)
	for idx := range packetCount {
		pkts[idx] = dispatchUDPPacket(t)
	}

	result, err := h.HandlePackets(pkts...)
	require.NoError(t, err)
	require.Empty(t, result.Output,
		"device entry with no pipeline must not forward any packet")
	require.Len(t, result.Drop, packetCount,
		"device entry with no pipeline must drop all packets")
}

// dispatchFlowPacket builds an Ethernet/IPv4/UDP packet whose source address
// varies with the flow index, so a batch of them carries distinct flow hashes
// and spreads across a hash demux instead of piling onto one destination.
func dispatchFlowPacket(t *testing.T, flowIdx int) gopacket.Packet {
	t.Helper()

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP(fmt.Sprintf("192.0.2.%d", 10+flowIdx)),
		DstIP:    net.ParseIP("10.0.0.1"),
	}
	udp := layers.UDP{SrcPort: 12345, DstPort: 80}
	require.NoError(t, udp.SetNetworkLayerForChecksum(&ip4))
	return xpacket.LayersToPacket(t, &eth, &ip4, &udp)
}

// dispatchFlowPackets builds a batch of packets with distinct flow hashes.
func dispatchFlowPackets(t *testing.T, count int) []gopacket.Packet {
	t.Helper()

	pkts := make([]gopacket.Packet, count)
	for idx := range count {
		pkts[idx] = dispatchFlowPacket(t, idx)
	}
	return pkts
}

// publishTwoRuleACLBackend creates a harness with one allow-all and one
// deny-all ACL module published under the given names.
func publishTwoRuleACLBackend(
	t *testing.T,
	agentName, allowName, denyName string,
	extraModules ...string,
) (*dataplaneut.Harness, *ffi.Agent) {
	t.Helper()

	h, agent, backend := setupACLBackend(t, agentName, extraModules...)
	publishMatchAllACL(t, backend, allowName, cacl.ActionAllow)
	publishMatchAllACL(t, backend, denyName, cacl.ActionDeny)
	return h, agent
}

// verifies that padding the chain routing table to a power of two never
// leaks traffic to a zero-weight chain, and that the demux hands every packet
// to exactly one enabled chain.
//
// The weights 2/0/1 sum to 3, so the table is padded well past their sum;
// the deny chain holds zero slots by construction, so any packet reaching it
// would show up as a drop. All packets must survive to the egress.
func Test_FunctionDemux_PaddedWeights_KeepZeroWeightChainIsolated(t *testing.T) {
	const (
		allowACL = "pw-allow"
		denyACL  = "pw-deny"
		fn       = "pw-fn"
		fwdName  = "pw-fwd"
	)

	h, agent := publishTwoRuleACLBackend(t, "pw-test", allowACL, denyACL, "forward")

	forwardBackend := forward.NewBackend(agent)
	rule := cforward.ForwardRule{
		Target:  "port0",
		Mode:    cforward.ModeOut,
		Counter: fwdName,
		Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/8")},
	}
	handle, err := forwardBackend.UpdateModule(
		fwdName, []cforward.ForwardRule{rule},
	)
	require.NoError(t, err)
	t.Cleanup(handle.Free)

	// The allow chains forward their packets to the output stage; the
	// input entry itself never transmits, so reaching egress proves the
	// packet traversed a chain intact.
	allowModules := []ffi.ChainModuleConfig{
		{Type: "acl", Name: allowACL},
		{Type: "forward", Name: fwdName},
	}

	// Three chains with weights 2/0/1: two allow chains around a disabled
	// deny chain.
	chains := []ffi.FunctionChainConfig{
		{
			Weight: 2,
			Chain: ffi.ChainConfig{
				Name:    fn + "-a",
				Modules: allowModules,
			},
		},
		{
			Weight: 0,
			Chain: ffi.ChainConfig{
				Name:    fn + "-b",
				Modules: []ffi.ChainModuleConfig{{Type: "acl", Name: denyACL}},
			},
		},
		{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    fn + "-c",
				Modules: allowModules,
			},
		},
	}
	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name:   fn,
		Chains: chains,
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      fn,
		Functions: []string{fn},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	require.NoError(t, agent.UpdatePlainDevices([]ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: fn, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}))

	const packetCount = 16
	result, err := h.HandlePackets(dispatchFlowPackets(t, packetCount)...)
	require.NoError(t, err)
	require.Len(t, result.Output, packetCount,
		"every packet must reach egress through one of the enabled chains")
	require.Empty(t, result.Drop,
		"the zero-weight deny chain must never receive a packet")

	counters := h.SharedMemory().DPConfig(0).FunctionCounters("port0", fn, fn)
	byName := dataplaneut.SingleValueCounters(counters)
	require.Equal(t, uint64(packetCount), byName["input"],
		"the function must see every packet exactly once")
}

// verifies that a chain whose index falls outside the demux local-tail cache
// still receives its packets through the direct-append fallback.
//
// Sixteen zero-weight deny chains push the one enabled allow chain to index
// 16, beyond the cached destinations. The single enabled chain holds the
// only routing slot, so every packet must reach it and survive to egress.
func Test_FunctionDemux_TailCacheOverflow_RoutesThroughDirectAppend(t *testing.T) {
	const (
		allowACL   = "bc-allow"
		denyACL    = "bc-deny"
		fn         = "bc-fn"
		fwdName    = "bc-fwd"
		chainCount = 17
		allowIdx   = chainCount - 1
	)

	h, agent := publishTwoRuleACLBackend(t, "bc-test", allowACL, denyACL, "forward")

	forwardBackend := forward.NewBackend(agent)
	rule := cforward.ForwardRule{
		Target:  "port0",
		Mode:    cforward.ModeOut,
		Counter: fwdName,
		Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/8")},
	}
	handle, err := forwardBackend.UpdateModule(
		fwdName, []cforward.ForwardRule{rule},
	)
	require.NoError(t, err)
	t.Cleanup(handle.Free)

	// The enabled chain forwards to the output stage; the input entry
	// itself never transmits.
	allowModules := []ffi.ChainModuleConfig{
		{Type: "acl", Name: allowACL},
		{Type: "forward", Name: fwdName},
	}

	chains := make([]ffi.FunctionChainConfig, 0, chainCount)
	for idx := range chainCount {
		weight := uint64(0)
		modules := []ffi.ChainModuleConfig{{Type: "acl", Name: denyACL}}
		if idx == allowIdx {
			weight = 1
			modules = allowModules
		}
		chains = append(chains, ffi.FunctionChainConfig{
			Weight: weight,
			Chain: ffi.ChainConfig{
				Name:    fmt.Sprintf("%s-%d", fn, idx),
				Modules: modules,
			},
		})
	}
	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name:   fn,
		Chains: chains,
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      fn,
		Functions: []string{fn},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	require.NoError(t, agent.UpdatePlainDevices([]ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: fn, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}))

	const packetCount = 8
	result, err := h.HandlePackets(dispatchFlowPackets(t, packetCount)...)
	require.NoError(t, err)
	require.Len(t, result.Output, packetCount,
		"every packet must reach the single enabled chain past the tail cache")
	require.Empty(t, result.Drop,
		"no packet may leak to a zero-weight chain while routing past the cache")
}

// verifies that padding the device-entry pipeline routing table to a power
// of two never leaks traffic to a zero-weight pipeline, and that every packet
// reaches exactly one enabled pipeline.
//
// The input entry binds three pipelines with weights 2/0/1; the deny
// pipeline holds zero slots by construction, so a leak would surface as a
// drop while the two forward pipelines carry everything to egress.
func Test_DeviceEntryDemux_PaddedWeights_KeepZeroWeightPipelineIsolated(t *testing.T) {
	const (
		allowACL = "pp-allow"
		denyACL  = "pp-deny"
		allowFn  = "pp-allow-fn"
		denyFn   = "pp-deny-fn"
		fwdName  = "pp-fwd"
	)

	h, agent := publishTwoRuleACLBackend(t, "pp-test", allowACL, denyACL, "forward")

	forwardBackend := forward.NewBackend(agent)
	rule := cforward.ForwardRule{
		Target:  "port0",
		Mode:    cforward.ModeOut,
		Counter: fwdName,
		Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/8")},
	}
	handle, err := forwardBackend.UpdateModule(
		fwdName, []cforward.ForwardRule{rule},
	)
	require.NoError(t, err)
	t.Cleanup(handle.Free)

	// The allow pipeline forwards matching traffic to the output stage;
	// the deny pipeline drops everything it receives.
	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: allowFn,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: allowFn + "-chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "acl", Name: allowACL},
					{Type: "forward", Name: fwdName},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: denyFn,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    denyFn + "-chain",
				Modules: []ffi.ChainModuleConfig{{Type: "acl", Name: denyACL}},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "pp-allow-a",
		Functions: []string{allowFn},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "pp-deny",
		Functions: []string{denyFn},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "pp-allow-c",
		Functions: []string{allowFn},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))

	require.NoError(t, agent.UpdatePlainDevices([]ffi.DeviceConfig{{
		Name: "port0",
		Input: []ffi.DevicePipelineConfig{
			{Name: "pp-allow-a", Weight: 2},
			{Name: "pp-deny", Weight: 0},
			{Name: "pp-allow-c", Weight: 1},
		},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}))

	const packetCount = 16
	result, err := h.HandlePackets(dispatchFlowPackets(t, packetCount)...)
	require.NoError(t, err)
	require.Len(t, result.Output, packetCount,
		"every packet must reach egress through one of the enabled pipelines")
	require.Empty(t, result.Drop,
		"the zero-weight deny pipeline must never receive a packet")
}

// verifies that a pipeline whose index falls outside the demux local-tail
// cache still receives its packets through the direct-append fallback.
//
// Sixteen zero-weight deny pipelines push the one enabled forward pipeline
// to index 16, beyond the cached destinations. The single enabled pipeline
// holds the only routing slot, so every packet must survive to egress.
func Test_DeviceEntryDemux_TailCacheOverflow_RoutesThroughDirectAppend(t *testing.T) {
	const (
		allowACL      = "bp-allow"
		denyACL       = "bp-deny"
		allowFn       = "bp-allow-fn"
		denyFn        = "bp-deny-fn"
		fwdName       = "bp-fwd"
		pipelineCount = 17
		allowIdx      = pipelineCount - 1
	)

	h, agent := publishTwoRuleACLBackend(t, "bp-test", allowACL, denyACL, "forward")

	forwardBackend := forward.NewBackend(agent)
	rule := cforward.ForwardRule{
		Target:  "port0",
		Mode:    cforward.ModeOut,
		Counter: fwdName,
		Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/8")},
	}
	handle, err := forwardBackend.UpdateModule(
		fwdName, []cforward.ForwardRule{rule},
	)
	require.NoError(t, err)
	t.Cleanup(handle.Free)

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: allowFn,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: allowFn + "-chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "acl", Name: allowACL},
					{Type: "forward", Name: fwdName},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: denyFn,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    denyFn + "-chain",
				Modules: []ffi.ChainModuleConfig{{Type: "acl", Name: denyACL}},
			},
		}},
	}))

	input := make([]ffi.DevicePipelineConfig, 0, pipelineCount)
	for idx := range pipelineCount {
		if idx == allowIdx {
			input = append(input, ffi.DevicePipelineConfig{
				Name:   "bp-allow",
				Weight: 1,
			})
			continue
		}
		name := fmt.Sprintf("bp-deny-%d", idx)
		require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
			Name:      name,
			Functions: []string{denyFn},
		}))
		input = append(input, ffi.DevicePipelineConfig{
			Name:   name,
			Weight: 0,
		})
	}
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "bp-allow",
		Functions: []string{allowFn},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))

	require.NoError(t, agent.UpdatePlainDevices([]ffi.DeviceConfig{{
		Name:   "port0",
		Input:  input,
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}))

	const packetCount = 8
	result, err := h.HandlePackets(dispatchFlowPackets(t, packetCount)...)
	require.NoError(t, err)
	require.Len(t, result.Output, packetCount,
		"every packet must reach the single enabled pipeline past the tail cache")
	require.Empty(t, result.Drop,
		"no packet may leak to a zero-weight pipeline while routing past the cache")
}
