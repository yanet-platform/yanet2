// Package pipeline_test holds regression tests for the dataplane pipeline
// dispatch logic implemented in lib/dataplane/pipeline/pipeline.c.
//
// The tests here lock in two correctness rules introduced by the
// single-chain fast path added to function_ectx_process:
//
//  1. A function whose chains are all zero-weight must DROP all packets
//     rather than route them to a disabled chain.
//
//  2. The single-chain fast path runs each function on its own packet front,
//     so two single-chain functions chained in one pipeline hand packets from
//     the first to the second intact and drop exactly what the chain drops.
package pipeline_test

import (
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
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

	backend := acl.NewBackend(agent, uint64(dispatchMemSize))
	return h, agent, backend
}

// publishMatchAllACL creates an ACL module whose single rule matches every
// IPv4 UDP packet with the given action and publishes it to shared memory.
//
// The created handle is freed via t.Cleanup.
func publishMatchAllACL(t *testing.T, backend acl.Backend, name string, action uint32) {
	t.Helper()

	handle, err := backend.NewModule(name)
	require.NoError(t, err)
	t.Cleanup(handle.Free)

	rule := cacl.AclRule{
		Actions:       []cacl.AclAction{{Kind: action}},
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Src6s:         filter.IPNets{},
		Dst6s:         filter.IPNets{},
		SrcPortRanges: filter.PortRanges{{From: 0, To: 65535}},
		DstPortRanges: filter.PortRanges{{From: 0, To: 65535}},
		ProtoRanges: filter.ProtoRanges{
			filter.NewProtoRange(uint8(layers.IPProtocolUDP), filter.AnySubtype()),
		},
		Fragment: filter.FragmentAny,
	}
	require.NoError(t, handle.UpdateRules([]cacl.AclRule{rule}))
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
	require.NoError(t, agent.UpdatePlainDevices([]ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}))

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

	// funcA allows every matching UDP IPv4 packet; funcB then drops them all.
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
	require.NoError(t, agent.UpdatePlainDevices([]ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: pipelineAB, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}))

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

	// funcB's input counter must equal N: the single-chain fast path handed
	// funcA's forwarded packets to funcB intact.
	counters := h.SharedMemory().DPConfig(0).FunctionCounters("port0", pipelineAB, aclB)
	byName := dataplaneut.SingleValueCounters(counters)
	require.Equal(t, uint64(packetCount), byName["input"],
		"funcB must receive exactly the packets funcA forwarded (all N)")
}
