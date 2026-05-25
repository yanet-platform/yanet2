package acl_test

import (
	"net"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
)

// Memory sizes for the ACL functional harness.
const (
	aclCPSize  = 64 * datasize.MB
	aclDPSize  = 4 * datasize.MB
	aclMemSize = 16 * datasize.MB
)

// udpProto matches any UDP packet.
var udpProto = filter.ProtoRanges{
	filter.NewProtoRange(uint8(layers.IPProtocolUDP), filter.AnySubtype()),
}

// tcpProto matches any TCP packet.
var tcpProto = filter.ProtoRanges{
	filter.NewProtoRange(uint8(layers.IPProtocolTCP), filter.AnySubtype()),
}

// icmp4Proto matches ICMP echo request.
var icmp4Proto = filter.ProtoRanges{
	filter.NewProtoRange(uint8(layers.IPProtocolICMPv4), filter.ExactSubtype(layers.ICMPv4TypeEchoRequest)),
}

// icmp6Proto matches ICMPv6 echo request.
var icmp6Proto = filter.ProtoRanges{
	filter.NewProtoRange(uint8(layers.IPProtocolICMPv6), filter.ExactSubtype(layers.ICMPv6TypeEchoRequest)),
}

// allPorts is a port range covering every port number.
var allPorts = filter.PortRanges{{From: 0, To: 65535}}

// setupACLHarness builds a dataplane harness with the ACL module loaded and
// attaches a control-plane agent.
//
// devices is the set of logical port names to register in the harness topology.
// Cleanup is wired via t.Cleanup in LIFO order.
func setupACLHarness(
	t *testing.T,
	devices []string,
) (*dataplaneut.Harness, *ffi.Agent, acl.Backend) {
	t.Helper()

	cfg := dataplaneut.Config{
		CPMemory:      uint64(aclCPSize),
		DPMemory:      uint64(aclDPSize),
		WorkerCount:   1,
		Devices:       devices,
		Modules:       []string{"acl"},
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("acl-test", 0, aclMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	backend := acl.NewBackend(agent, uint64(aclMemSize))
	return h, agent, backend
}

// applyACLRules pushes rules into a new ACL module config and publishes it to
// the dataplane. The module handle is freed via t.Cleanup.
func applyACLRules(
	t *testing.T,
	backend acl.Backend,
	name string,
	rules []cacl.AclRule,
) acl.ModuleHandle {
	t.Helper()

	handle, err := backend.NewModule(name)
	require.NoError(t, err)
	t.Cleanup(handle.Free)

	require.NoError(t, handle.UpdateRules(rules))
	require.NoError(t, backend.UpdateModule(handle))
	return handle
}

// wireACLPipeline wires chain[acl:configName] -> function -> pipeline ->
// plain-device topology so the harness routes packets through the ACL module.
//
// Must be called after applyACLRules, because the ACL module config must exist
// before UpdatePlainDevices resolves chain module references.
func wireACLPipeline(
	t *testing.T,
	agent *ffi.Agent,
	device, configName string,
) {
	t.Helper()

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: configName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: configName + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "acl", Name: configName},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      configName,
		Functions: []string{configName},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name: "dummy",
	}))
	require.NoError(t, agent.UpdatePlainDevices([]ffi.DeviceConfig{{
		Name:   device,
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}))
}

// aclCounterPath returns the CounterPath for an ACL module in the test topology.
func aclCounterPath(device, configName string) dataplaneut.CounterPath {
	return dataplaneut.CounterPath{
		Device:     device,
		Pipeline:   configName,
		Function:   configName,
		Chain:      configName + "_chain",
		ModuleType: "acl",
		ModuleName: configName,
	}
}

// requireModuleCounterPackets asserts that the named size-1 module counter
// holds wantPackets at worker 0.
func requireModuleCounterPackets(
	t *testing.T,
	h *dataplaneut.Harness,
	path dataplaneut.CounterPath,
	counterName string,
	wantPackets uint64,
) {
	t.Helper()

	counters := h.SharedMemory().DPConfig(0).ModuleCounters(
		path.Device, path.Pipeline, path.Function, path.Chain,
		path.ModuleType, path.ModuleName, []string{counterName},
	)
	byName := map[string][]uint64{}
	for _, c := range counters {
		if len(c.Values) > 0 {
			byName[c.Name] = c.Values[0]
		}
	}
	vals, ok := byName[counterName]
	require.True(t, ok, "counter %q not found", counterName)
	require.GreaterOrEqual(t, len(vals), 1)
	require.Equal(t, wantPackets, vals[0], "counter %q packet count mismatch", counterName)
}

// allow4Rule builds an IPv4 ALLOW rule for the given source and destination host
// addresses and protocol range.
func allow4Rule(src4, dst4 filter.IPNets, protos filter.ProtoRanges) cacl.AclRule {
	return cacl.AclRule{
		Actions:       []cacl.AclAction{{Kind: cacl.ActionAllow}},
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         src4,
		Dst4s:         dst4,
		Src6s:         filter.IPNets{},
		Dst6s:         filter.IPNets{},
		SrcPortRanges: allPorts,
		DstPortRanges: allPorts,
		ProtoRanges:   protos,
	}
}

// deny4Rule builds an IPv4 DENY rule for the given source and destination host
// addresses and protocol range.
func deny4Rule(src4, dst4 filter.IPNets, protos filter.ProtoRanges) cacl.AclRule {
	r := allow4Rule(src4, dst4, protos)
	r.Actions = []cacl.AclAction{{Kind: cacl.ActionDeny}}
	return r
}

// allow6Rule builds an IPv6 ALLOW rule for the given source and destination
// prefixes and protocol range.
func allow6Rule(src6, dst6 filter.IPNets, protos filter.ProtoRanges) cacl.AclRule {
	return cacl.AclRule{
		Actions:       []cacl.AclAction{{Kind: cacl.ActionAllow}},
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         filter.IPNets{},
		Dst4s:         filter.IPNets{},
		Src6s:         src6,
		Dst6s:         dst6,
		SrcPortRanges: allPorts,
		DstPortRanges: allPorts,
		ProtoRanges:   protos,
	}
}

// TestACL_NoMatch_Drop verifies that a packet not matched by any rule is
// dropped and the acl_no_match counter increments.
func TestACL_NoMatch_Drop(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP("192.0.2.1"),
		DstIP:    net.ParseIP("10.0.0.1"),
	}
	icmp4 := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}
	// Rule matches a different source address, so the test packet misses it.
	rules := []cacl.AclRule{
		allow4Rule(
			filter.IPNets{{Addr: netip.MustParseAddr("10.10.10.10"), Mask: netip.MustParseAddr("255.255.255.255")}},
			filter.IPNets{filter.UnspecifiedIPv4},
			icmp4Proto,
		),
	}
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp4)

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, result.Output, "no-match packet must not reach output")
	require.Len(t, result.Drop, 1, "no-match packet must be dropped")

	requireModuleCounterPackets(t, h, aclCounterPath("port0", "test"), "acl_no_match", 1)
}

// TestACL_TCP_SYNFlag verifies the proto subtype byte selects on TCP flags.
//
// A rule that targets TCP with exact subtype 0x02 (SYN only) matches a
// SYN-only handshake packet and misses a RST-only packet.
func TestACL_TCP_SYNFlag(t *testing.T) {
	rules := []cacl.AclRule{{
		Actions:       []cacl.AclAction{{Kind: cacl.ActionAllow}},
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Src6s:         filter.IPNets{},
		Dst6s:         filter.IPNets{},
		SrcPortRanges: allPorts,
		DstPortRanges: allPorts,
		ProtoRanges: filter.ProtoRanges{
			filter.NewProtoRange(uint8(layers.IPProtocolTCP), filter.ExactSubtype(0x02)),
		},
	}}

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.ParseIP("192.0.2.1"),
		DstIP:    net.ParseIP("10.0.0.1"),
	}
	synTCP := layers.TCP{SrcPort: 12345, DstPort: 80, SYN: true}
	synTCP.SetNetworkLayerForChecksum(&ip4)
	synPkt := xpacket.LayersToPacket(t, &eth, &ip4, &synTCP)

	rstTCP := layers.TCP{SrcPort: 12345, DstPort: 80, RST: true}
	rstTCP.SetNetworkLayerForChecksum(&ip4)
	rstPkt := xpacket.LayersToPacket(t, &eth, &ip4, &rstTCP)

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	t.Run("syn_matches", func(t *testing.T) {
		result, err := h.HandlePackets(synPkt)
		require.NoError(t, err)
		require.Len(t, result.Output, 1, "SYN packet must match the SYN-only rule")
	})

	t.Run("rst_no_match", func(t *testing.T) {
		result, err := h.HandlePackets(rstPkt)
		require.NoError(t, err)
		require.Len(t, result.Output, 0, "RST packet must not match the SYN-only rule")
	})
}

// TestACL_Allow_UDP_IPv4 verifies that a UDP IPv4 packet matching an ALLOW
// rule passes through and increments acl_action_allow.
func TestACL_Allow_UDP_IPv4(t *testing.T) {
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
	udp.SetNetworkLayerForChecksum(&ip4)
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp)

	rules := []cacl.AclRule{
		allow4Rule(
			filter.IPNets{filter.UnspecifiedIPv4},
			filter.IPNets{filter.UnspecifiedIPv4},
			udpProto,
		),
	}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "UDP ALLOW packet must reach output")
	require.Empty(t, result.Drop, "UDP ALLOW packet must not be dropped")

	requireModuleCounterPackets(t, h, aclCounterPath("port0", "test"), "acl_action_allow", 1)
}

// TestACL_Deny_UDP_IPv4 verifies that a UDP IPv4 packet matching a DENY rule
// is dropped and increments acl_action_deny.
func TestACL_Deny_UDP_IPv4(t *testing.T) {
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
	udp.SetNetworkLayerForChecksum(&ip4)
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp)

	rules := []cacl.AclRule{
		deny4Rule(
			filter.IPNets{filter.UnspecifiedIPv4},
			filter.IPNets{filter.UnspecifiedIPv4},
			udpProto,
		),
	}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, result.Output, "UDP DENY packet must not reach output")
	require.Len(t, result.Drop, 1, "UDP DENY packet must be dropped")

	requireModuleCounterPackets(t, h, aclCounterPath("port0", "test"), "acl_action_deny", 1)
}

// TestACL_PortRange_UDP verifies that port-range filtering works correctly for
// UDP packets via the filter_ip4_port branch.
func TestACL_PortRange_UDP(t *testing.T) {
	// Rule allows UDP dst ports 150-450 only.
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

	rules := []cacl.AclRule{{
		Actions:       []cacl.AclAction{{Kind: cacl.ActionAllow}},
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Src6s:         filter.IPNets{},
		Dst6s:         filter.IPNets{},
		SrcPortRanges: allPorts,
		DstPortRanges: filter.PortRanges{{From: 150, To: 450}},
		ProtoRanges:   udpProto,
	}}

	t.Run("in_range", func(t *testing.T) {
		udp := layers.UDP{SrcPort: 12345, DstPort: 300}
		udp.SetNetworkLayerForChecksum(&ip4)
		pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp)

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		require.Len(t, result.Output, 1, "UDP dst port 300 in range [150,450] must pass")
		require.Empty(t, result.Drop)
	})

	t.Run("out_of_range", func(t *testing.T) {
		udp := layers.UDP{SrcPort: 12345, DstPort: 600}
		udp.SetNetworkLayerForChecksum(&ip4)
		pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp)

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		require.Empty(t, result.Output, "UDP dst port 600 outside range [150,450] must be dropped")
		require.Len(t, result.Drop, 1)
	})
}

// TestACL_Subnet_IPv4 verifies that a /24 source CIDR rule correctly allows
// packets from within the subnet.
func TestACL_Subnet_IPv4(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		// Host 192.0.2.100 is inside the /24 subnet 192.0.2.0/24.
		SrcIP: net.ParseIP("192.0.2.100"),
		DstIP: net.ParseIP("10.0.0.1"),
	}
	udp := layers.UDP{SrcPort: 12345, DstPort: 80}
	udp.SetNetworkLayerForChecksum(&ip4)
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp)

	rules := []cacl.AclRule{
		allow4Rule(
			filter.IPNets{filter.MustParseIPNet("192.0.2.0/24")},
			filter.IPNets{filter.UnspecifiedIPv4},
			udpProto,
		),
	}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "source inside /24 subnet must match ALLOW rule")
	require.Empty(t, result.Drop)
}

// TestACL_TCP_IPv4 verifies that a TCP rule routes through the
// filter_ip4_port branch (TCP + port ranges).
func TestACL_TCP_IPv4(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.ParseIP("192.0.2.1"),
		DstIP:    net.ParseIP("10.0.0.1"),
	}
	tcp := layers.TCP{SrcPort: 12345, DstPort: 443, SYN: true}
	tcp.SetNetworkLayerForChecksum(&ip4)
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &tcp)

	rules := []cacl.AclRule{{
		Actions:       []cacl.AclAction{{Kind: cacl.ActionAllow}},
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Src6s:         filter.IPNets{},
		Dst6s:         filter.IPNets{},
		SrcPortRanges: allPorts,
		DstPortRanges: allPorts,
		ProtoRanges:   tcpProto,
	}}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "TCP ALLOW packet via filter_ip4_port must reach output")
	require.Empty(t, result.Drop)

	requireModuleCounterPackets(t, h, aclCounterPath("port0", "test"), "acl_action_allow", 1)
}

// TestACL_ICMP_IPv4 verifies that an ICMP echo request matching a rule goes
// through the filter_ip4 branch (no port filter for ICMP).
func TestACL_ICMP_IPv4(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP("192.0.2.1"),
		DstIP:    net.ParseIP("10.0.0.1"),
	}
	icmp4 := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp4)

	// ICMP has no port concept, so the rule uses the ip4 filter, not ip4_port.
	rules := []cacl.AclRule{
		allow4Rule(
			filter.IPNets{filter.UnspecifiedIPv4},
			filter.IPNets{filter.UnspecifiedIPv4},
			icmp4Proto,
		),
	}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "ICMP echo request matching filter_ip4 rule must reach output")
	require.Empty(t, result.Drop)

	requireModuleCounterPackets(t, h, aclCounterPath("port0", "test"), "acl_action_allow", 1)
}

// TestACL_IPv6_TCP verifies that a TCP IPv6 packet routes through the
// filter_ip6_port branch and is allowed.
func TestACL_IPv6_TCP(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolTCP,
		SrcIP:      net.ParseIP("2001:db8::1"),
		DstIP:      net.ParseIP("2001:db8::2"),
	}
	tcp := layers.TCP{SrcPort: 12345, DstPort: 443, SYN: true}
	tcp.SetNetworkLayerForChecksum(&ip6)
	pkt := xpacket.LayersToPacket(t, &eth, &ip6, &tcp)

	rules := []cacl.AclRule{{
		Actions:       []cacl.AclAction{{Kind: cacl.ActionAllow}},
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         filter.IPNets{},
		Dst4s:         filter.IPNets{},
		Src6s:         filter.IPNets{filter.UnspecifiedIPv6},
		Dst6s:         filter.IPNets{filter.UnspecifiedIPv6},
		SrcPortRanges: allPorts,
		DstPortRanges: allPorts,
		ProtoRanges:   tcpProto,
	}}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "IPv6 TCP ALLOW packet via filter_ip6_port must reach output")
	require.Empty(t, result.Drop)

	requireModuleCounterPackets(t, h, aclCounterPath("port0", "test"), "acl_action_allow", 1)
}

// TestACL_IPv6_ICMP verifies that an ICMPv6 echo request routes through the
// filter_ip6 branch (no port filter for ICMPv6) and is allowed.
func TestACL_IPv6_ICMP(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolICMPv6,
		SrcIP:      net.ParseIP("2001:db8::1"),
		DstIP:      net.ParseIP("2001:db8::2"),
	}
	icmp6 := layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(layers.ICMPv6TypeEchoRequest, 0),
	}
	icmp6.SetNetworkLayerForChecksum(&ip6)
	pkt := xpacket.LayersToPacket(t, &eth, &ip6, &icmp6)

	rules := []cacl.AclRule{
		allow6Rule(
			filter.IPNets{filter.UnspecifiedIPv6},
			filter.IPNets{filter.UnspecifiedIPv6},
			icmp6Proto,
		),
	}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "ICMPv6 echo request matching filter_ip6 rule must reach output")
	require.Empty(t, result.Drop)

	requireModuleCounterPackets(t, h, aclCounterPath("port0", "test"), "acl_action_allow", 1)
}

// TestACL_Overlapping_RulePriority verifies that when multiple rules match a
// packet, the ACL handler selects the action with the lowest rule index via
// the min-action logic in acl_handle_packets.
//
// Rule 0 (index 0): ALLOW for /31 (192.0.2.0-192.0.2.1)
// Rule 1 (index 1): DENY  for /28 (192.0.2.0-192.0.2.15)
// Rule 2 (index 2): ALLOW for /24 (192.0.2.0/24)
// Rule 3 (index 3): DENY  for /16 (192.0.0.0/16)
//
// A packet from 192.0.2.1 (/31) matches rules 0, 1, 2, 3 — min index is 0
// (ALLOW). A packet from 192.0.2.5 (/28 but not /31) matches rules 1, 2, 3 —
// min index is 1 (DENY). A packet from 192.0.2.100 (/24 but not /28) matches
// rules 2 and 3 — min index is 2 (ALLOW). A packet from 192.0.10.1 (/16 but
// not /24) matches only rule 3 — min index is 3 (DENY).
func TestACL_Overlapping_RulePriority(t *testing.T) {
	rules := []cacl.AclRule{
		allow4Rule(
			filter.IPNets{{
				Addr: netip.MustParseAddr("192.0.2.0"),
				Mask: netip.MustParseAddr("255.255.255.254"),
			}},
			filter.IPNets{filter.UnspecifiedIPv4}, udpProto,
		),
		deny4Rule(
			filter.IPNets{{
				Addr: netip.MustParseAddr("192.0.2.0"),
				Mask: netip.MustParseAddr("255.255.255.240"),
			}},
			filter.IPNets{filter.UnspecifiedIPv4}, udpProto,
		),
		allow4Rule(
			filter.IPNets{filter.MustParseIPNet("192.0.2.0/24")},
			filter.IPNets{filter.UnspecifiedIPv4},
			udpProto,
		),
		deny4Rule(
			filter.IPNets{filter.MustParseIPNet("192.0.0.0/16")},
			filter.IPNets{filter.UnspecifiedIPv4},
			udpProto,
		),
	}

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

	t.Run("allow_/31", func(t *testing.T) {
		ip := ip4
		ip.SrcIP = net.ParseIP("192.0.2.1")
		udp := layers.UDP{SrcPort: 12345, DstPort: 80}
		udp.SetNetworkLayerForChecksum(&ip)
		pkt := xpacket.LayersToPacket(t, &eth, &ip, &udp)

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		require.Len(t, result.Output, 1, "source in /31 (rule 0 = ALLOW) must pass")
		require.Empty(t, result.Drop)
	})

	t.Run("deny_/28", func(t *testing.T) {
		ip := ip4
		ip.SrcIP = net.ParseIP("192.0.2.5")
		udp := layers.UDP{SrcPort: 12345, DstPort: 80}
		udp.SetNetworkLayerForChecksum(&ip)
		pkt := xpacket.LayersToPacket(t, &eth, &ip, &udp)

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		require.Empty(t, result.Output, "source in /28 (rule 1 = DENY) must be dropped")
		require.Len(t, result.Drop, 1)
	})

	t.Run("allow_/24", func(t *testing.T) {
		ip := ip4
		ip.SrcIP = net.ParseIP("192.0.2.100")
		udp := layers.UDP{SrcPort: 12345, DstPort: 80}
		udp.SetNetworkLayerForChecksum(&ip)
		pkt := xpacket.LayersToPacket(t, &eth, &ip, &udp)

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		require.Len(t, result.Output, 1, "source in /24 (rule 2 = ALLOW) must pass")
		require.Empty(t, result.Drop)
	})

	t.Run("deny_/16", func(t *testing.T) {
		ip := ip4
		ip.SrcIP = net.ParseIP("192.0.10.1")
		udp := layers.UDP{SrcPort: 12345, DstPort: 80}
		udp.SetNetworkLayerForChecksum(&ip)
		pkt := xpacket.LayersToPacket(t, &eth, &ip, &udp)

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		require.Empty(t, result.Output, "source in /16 (rule 3 = DENY) must be dropped")
		require.Len(t, result.Drop, 1)
	})
}

// TestACL_NonIP_Drop verifies that a non-IP packet (ARP) is dropped and
// increments acl_no_match when no vlan/L2-only rule matches.
//
// An ARP packet has no IP header, so neither the ip4 nor ip6 branch is taken.
// With no matching vlan rule (the vlan filter result is FILTER_RULE_INVALID),
// target stays NULL and the packet is counted as no_match and dropped.
func TestACL_NonIP_Drop(t *testing.T) {
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
		SourceHwAddress:   xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		SourceProtAddress: net.ParseIP("10.0.0.1").To4(),
		DstHwAddress:      net.HardwareAddr{0, 0, 0, 0, 0, 0},
		DstProtAddress:    net.ParseIP("10.0.0.2").To4(),
	}
	pkt := xpacket.LayersToPacket(t, &eth, &arp)

	// The rule only matches UDP IPv4 — it is invisible to the vlan filter for
	// the ARP packet, so the vlan filter returns FILTER_RULE_INVALID.
	rules := []cacl.AclRule{
		allow4Rule(
			filter.IPNets{filter.UnspecifiedIPv4},
			filter.IPNets{filter.UnspecifiedIPv4},
			udpProto,
		),
	}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Empty(t, result.Output, "non-IP ARP packet must not reach output")
	require.Len(t, result.Drop, 1, "non-IP ARP packet must be dropped")

	requireModuleCounterPackets(t, h, aclCounterPath("port0", "test"), "acl_no_match", 1)
}

// serializeFragPacket serializes layers into a raw Ethernet frame and returns
// it as a gopacket.Packet without asserting that all layers decoded cleanly.
//
// Used for fragment packets whose inner payload cannot be decoded by gopacket
// because no L4 header is present (or the layer type is reported as Fragment).
func serializeFragPacket(t *testing.T, lyrs ...gopacket.SerializableLayer) gopacket.Packet {
	t.Helper()

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	require.NoError(t, gopacket.SerializeLayers(buf, opts, lyrs...))
	return xpacket.ParseEtherPacket(buf.Bytes())
}

// TestACL_Counters exercises both size-2 per-rule COUNT counters and size-1
// action counters to verify the counter semantics documented in
// modules/acl/api/controlplane.c.
func TestACL_Counters(t *testing.T) {
	t.Run("per_rule_count_action", func(t *testing.T) {
		// Rule: COUNT (size 2: packets+bytes) then ALLOW.
		rules := []cacl.AclRule{{
			Actions:       []cacl.AclAction{{Kind: cacl.ActionCount}, {Kind: cacl.ActionAllow}},
			Counter:       "acl_http",
			Devices:       filter.Devices{{Name: "port0"}},
			Src4s:         filter.IPNets{filter.UnspecifiedIPv4},
			Dst4s:         filter.IPNets{filter.UnspecifiedIPv4},
			Src6s:         filter.IPNets{},
			Dst6s:         filter.IPNets{},
			SrcPortRanges: allPorts,
			DstPortRanges: allPorts,
			ProtoRanges:   udpProto,
		}}

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
		udp.SetNetworkLayerForChecksum(&ip4)
		pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp)
		pktSize := uint64(len(pkt.Data()))

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		for range 3 {
			result, err := h.HandlePackets(pkt)
			require.NoError(t, err)
			require.Len(t, result.Output, 1)
		}

		path := aclCounterPath("port0", "test")
		dataplaneut.RequireModuleCounter(t, h, path, "acl_http", 3, 3*pktSize)
	})

	t.Run("multiple_named_counters", func(t *testing.T) {
		// Two rules, each with a distinct named counter, matched by different
		// source IPs.  Verifies that per-rule COUNT counters are independent.
		rules := []cacl.AclRule{
			{
				Actions:       []cacl.AclAction{{Kind: cacl.ActionCount}, {Kind: cacl.ActionAllow}},
				Counter:       "http",
				Devices:       filter.Devices{{Name: "port0"}},
				Src4s:         filter.IPNets{{Addr: netip.MustParseAddr("10.0.0.1"), Mask: netip.MustParseAddr("255.255.255.255")}},
				Dst4s:         filter.IPNets{filter.UnspecifiedIPv4},
				Src6s:         filter.IPNets{},
				Dst6s:         filter.IPNets{},
				SrcPortRanges: allPorts,
				DstPortRanges: allPorts,
				ProtoRanges:   udpProto,
			},
			{
				Actions:       []cacl.AclAction{{Kind: cacl.ActionCount}, {Kind: cacl.ActionAllow}},
				Counter:       "dns",
				Devices:       filter.Devices{{Name: "port0"}},
				Src4s:         filter.IPNets{{Addr: netip.MustParseAddr("10.0.0.2"), Mask: netip.MustParseAddr("255.255.255.255")}},
				Dst4s:         filter.IPNets{filter.UnspecifiedIPv4},
				Src6s:         filter.IPNets{},
				Dst6s:         filter.IPNets{},
				SrcPortRanges: allPorts,
				DstPortRanges: allPorts,
				ProtoRanges:   udpProto,
			},
		}

		eth := layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
			EthernetType: layers.EthernetTypeIPv4,
		}
		ip4 := layers.IPv4{
			Version:  4,
			TTL:      64,
			Protocol: layers.IPProtocolUDP,
			SrcIP:    net.ParseIP("10.0.0.1"),
			DstIP:    net.ParseIP("10.1.0.1"),
		}

		// Build a packet from 10.0.0.1 (matches rule 0, counter "http").
		udp0 := layers.UDP{SrcPort: 12345, DstPort: 80}
		udp0.SetNetworkLayerForChecksum(&ip4)
		pkt0 := xpacket.LayersToPacket(t, &eth, &ip4, &udp0)
		pktSize0 := uint64(len(pkt0.Data()))

		// Build a packet from 10.0.0.2 (matches rule 1, counter "dns").
		ip4b := ip4
		ip4b.SrcIP = net.ParseIP("10.0.0.2")
		udp1 := layers.UDP{SrcPort: 53, DstPort: 53}
		udp1.SetNetworkLayerForChecksum(&ip4b)
		pkt1 := xpacket.LayersToPacket(t, &eth, &ip4b, &udp1)
		pktSize1 := uint64(len(pkt1.Data()))

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		// Send 3 packets through rule 0.
		for range 3 {
			result, err := h.HandlePackets(pkt0)
			require.NoError(t, err)
			require.Len(t, result.Output, 1)
		}

		// Send 2 packets through rule 1.
		for range 2 {
			result, err := h.HandlePackets(pkt1)
			require.NoError(t, err)
			require.Len(t, result.Output, 1)
		}

		path := aclCounterPath("port0", "test")
		dataplaneut.RequireModuleCounter(t, h, path, "http", 3, 3*pktSize0)
		dataplaneut.RequireModuleCounter(t, h, path, "dns", 2, 2*pktSize1)
	})

	t.Run("default_counter_name", func(t *testing.T) {
		// A rule with no Counter field causes the C config layer to synthesise
		// the name "rule 0" (0-based index).  Verifies the synthetic name is
		// observable from the harness.
		rules := []cacl.AclRule{{
			Actions: []cacl.AclAction{{Kind: cacl.ActionCount}, {Kind: cacl.ActionAllow}},
			// Counter intentionally left empty — C layer synthesises "rule 0".
			Devices:       filter.Devices{{Name: "port0"}},
			Src4s:         filter.IPNets{filter.UnspecifiedIPv4},
			Dst4s:         filter.IPNets{filter.UnspecifiedIPv4},
			Src6s:         filter.IPNets{},
			Dst6s:         filter.IPNets{},
			SrcPortRanges: allPorts,
			DstPortRanges: allPorts,
			ProtoRanges:   udpProto,
		}}

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
		udp.SetNetworkLayerForChecksum(&ip4)
		pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp)
		pktSize := uint64(len(pkt.Data()))

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		for range 2 {
			result, err := h.HandlePackets(pkt)
			require.NoError(t, err)
			require.Len(t, result.Output, 1)
		}

		path := aclCounterPath("port0", "test")
		dataplaneut.RequireModuleCounter(t, h, path, "rule 0", 2, 2*pktSize)
	})

	t.Run("named_counter_no_count_action", func(t *testing.T) {
		// A rule with a named Counter but no ACTION_COUNT registers the counter
		// unconditionally (counter_registry_register is called per-target in the
		// C config layer regardless of actions), but the counter stays at zero
		// because ACTION_COUNT is what increments it at packet-processing time.
		rules := []cacl.AclRule{{
			Actions:       []cacl.AclAction{{Kind: cacl.ActionAllow}},
			Counter:       "unused",
			Devices:       filter.Devices{{Name: "port0"}},
			Src4s:         filter.IPNets{filter.UnspecifiedIPv4},
			Dst4s:         filter.IPNets{filter.UnspecifiedIPv4},
			Src6s:         filter.IPNets{},
			Dst6s:         filter.IPNets{},
			SrcPortRanges: allPorts,
			DstPortRanges: allPorts,
			ProtoRanges:   udpProto,
		}}

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
		udp.SetNetworkLayerForChecksum(&ip4)
		pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp)

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		result, err := h.HandlePackets(pkt)
		require.NoError(t, err)
		require.Len(t, result.Output, 1, "ALLOW-only rule must pass the packet")
		require.Empty(t, result.Drop)

		// The "unused" counter is registered but stays at zero because no
		// ACTION_COUNT is present in the rule's action list.
		path := aclCounterPath("port0", "test")
		dataplaneut.RequireModuleCounter(t, h, path, "unused", 0, 0)
		requireModuleCounterPackets(t, h, path, "acl_action_allow", 1)
	})

	t.Run("action_counters_size1", func(t *testing.T) {
		// Two rules: first allows packets from 10.0.0.1, second denies from 10.0.0.2.
		rules := []cacl.AclRule{
			allow4Rule(
				filter.IPNets{{Addr: netip.MustParseAddr("10.0.0.1"), Mask: netip.MustParseAddr("255.255.255.255")}},
				filter.IPNets{filter.UnspecifiedIPv4},
				udpProto,
			),
			deny4Rule(
				filter.IPNets{{Addr: netip.MustParseAddr("10.0.0.2"), Mask: netip.MustParseAddr("255.255.255.255")}},
				filter.IPNets{filter.UnspecifiedIPv4},
				udpProto,
			),
		}

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

		makeUDP := func(srcIP string) layers.UDP {
			ip := ip4
			ip.SrcIP = net.ParseIP(srcIP)
			udp := layers.UDP{SrcPort: 12345, DstPort: 80}
			udp.SetNetworkLayerForChecksum(&ip)
			return udp
		}

		h, agent, backend := setupACLHarness(t, []string{"port0"})
		applyACLRules(t, backend, "test", rules)
		wireACLPipeline(t, agent, "port0", "test")

		// Send two allow packets (from 10.0.0.1).
		for range 2 {
			ip := ip4
			ip.SrcIP = net.ParseIP("10.0.0.1")
			udp := makeUDP("10.0.0.1")
			pkt := xpacket.LayersToPacket(t, &eth, &ip, &udp)
			_, err := h.HandlePackets(pkt)
			require.NoError(t, err)
		}

		// Send one deny packet (from 10.0.0.2).
		{
			ip := ip4
			ip.SrcIP = net.ParseIP("10.0.0.2")
			udp := makeUDP("10.0.0.2")
			pkt := xpacket.LayersToPacket(t, &eth, &ip, &udp)
			_, err := h.HandlePackets(pkt)
			require.NoError(t, err)
		}

		path := aclCounterPath("port0", "test")
		requireModuleCounterPackets(t, h, path, "acl_action_allow", 2)
		requireModuleCounterPackets(t, h, path, "acl_action_deny", 1)
	})
}

// TestACL_Log_AllowPasses verifies that ACTION_LOG is a non-terminating no-op
// and the subsequent ACTION_ALLOW is reached, allowing the packet through.
//
// Covers the ACTION_LOG case in the action switch and the ACTION_ALLOW goto
// in the same target's action list.
func TestACL_Log_AllowPasses(t *testing.T) {
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
	udp.SetNetworkLayerForChecksum(&ip4)
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp)

	rules := []cacl.AclRule{{
		Actions:       []cacl.AclAction{{Kind: cacl.ActionLog}, {Kind: cacl.ActionAllow}},
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Src6s:         filter.IPNets{},
		Dst6s:         filter.IPNets{},
		SrcPortRanges: allPorts,
		DstPortRanges: allPorts,
		ProtoRanges:   udpProto,
	}}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	assert.Len(t, result.Output, 1, "LOG+ALLOW packet must reach output")
	assert.Empty(t, result.Drop, "LOG+ALLOW packet must not be dropped")

	requireModuleCounterPackets(t, h, aclCounterPath("port0", "test"), "acl_action_allow", 1)
}

// TestACL_NoTerminatingAction_Drop verifies the no-terminating-action path.
//
// A rule with only ACTION_COUNT causes the inner loop to exit without hitting
// goto apply. The code falls through into the apply label with allow=false,
// incrementing acl_action_non_term and then acl_action_deny before dropping.
// The per-rule COUNT counter also increments.
func TestACL_NoTerminatingAction_Drop(t *testing.T) {
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
	udp.SetNetworkLayerForChecksum(&ip4)
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &udp)
	pktSize := uint64(len(pkt.Data()))

	rules := []cacl.AclRule{{
		Actions:       []cacl.AclAction{{Kind: cacl.ActionCount}},
		Counter:       "acl_count_only",
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Src6s:         filter.IPNets{},
		Dst6s:         filter.IPNets{},
		SrcPortRanges: allPorts,
		DstPortRanges: allPorts,
		ProtoRanges:   udpProto,
	}}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	assert.Empty(t, result.Output, "count-only packet must not reach output")
	assert.Len(t, result.Drop, 1, "count-only packet must be dropped")

	path := aclCounterPath("port0", "test")
	requireModuleCounterPackets(t, h, path, "acl_action_non_term", 1)
	requireModuleCounterPackets(t, h, path, "acl_action_deny", 1)
	dataplaneut.RequireModuleCounter(t, h, path, "acl_count_only", 1, pktSize)
}

// TestACL_VLAN_Match asserts that a VLAN-tagged packet matches a rule whose
// VlanRanges include the packet's VLAN ID.
func TestACL_VLAN_Match(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	dot1q := layers.Dot1Q{
		VLANIdentifier: 150,
		Type:           layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP("192.0.2.1"),
		DstIP:    net.ParseIP("10.0.0.1"),
	}
	udp := layers.UDP{SrcPort: 12345, DstPort: 80}
	udp.SetNetworkLayerForChecksum(&ip4)
	pkt := serializeFragPacket(t, &eth, &dot1q, &ip4, &udp)

	rules := []cacl.AclRule{{
		Actions:       []cacl.AclAction{{Kind: cacl.ActionAllow}},
		Devices:       filter.Devices{{Name: "port0"}},
		VlanRanges:    filter.VlanRanges{{From: 100, To: 200}},
		Src4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Src6s:         filter.IPNets{},
		Dst6s:         filter.IPNets{},
		SrcPortRanges: allPorts,
		DstPortRanges: allPorts,
		ProtoRanges:   udpProto,
	}}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	assert.Len(t, result.Output, 1, "VLAN-tagged packet with VID inside the rule's range must be allowed")
	assert.Empty(t, result.Drop)

	requireModuleCounterPackets(t, h, aclCounterPath("port0", "test"), "acl_action_allow", 1)
}

// TestACL_IPv4Fragment_FirstFragment documents the parser's behavior for the
// first fragment of a UDP datagram (FragOffset=0, MF=1).
//
// The first fragment carries the full L4 header, so the parser reads the real
// UDP source and destination ports. A port-based ALLOW rule that matches those
// ports therefore allows the packet through. This is the expected behavior.
func TestACL_IPv4Fragment_FirstFragment(t *testing.T) {
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
	// First fragment: MoreFragments=1, FragOffset=0 — carries the L4 header.
	ip4.Protocol = layers.IPProtocolUDP
	ip4.Flags = layers.IPv4MoreFragments
	ip4.FragOffset = 0
	udp := layers.UDP{SrcPort: 12345, DstPort: 80}
	udp.SetNetworkLayerForChecksum(&ip4)

	// serializeFragPacket is used because gopacket decodes this as a Fragment
	// layer (not a UDP layer) on the receive side, which would cause
	// LayersToPacket to report an error layer.
	pkt := serializeFragPacket(t, &eth, &ip4, &udp)

	rules := []cacl.AclRule{{
		Actions:       []cacl.AclAction{{Kind: cacl.ActionAllow}},
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Src6s:         filter.IPNets{},
		Dst6s:         filter.IPNets{},
		SrcPortRanges: allPorts,
		DstPortRanges: filter.PortRanges{{From: 80, To: 80}},
		ProtoRanges:   udpProto,
	}}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	// First fragment has a real L4 header so port matching succeeds.
	assert.Len(t, result.Output, 1, "first fragment with matching ports must be allowed")
	assert.Empty(t, result.Drop)

	requireModuleCounterPackets(t, h, aclCounterPath("port0", "test"), "acl_action_allow", 1)
}

// TestACL_IPv4Fragment_LaterFragment_PortRule asserts that a non-first IPv4
// fragment does NOT match a port-based rule, because the fragment has no
// L4 header and its "ports" are undefined.
//
// Currently SKIPPED because lib/dataplane/packet/packet.c does not check the
// IPv4 fragment flags or offset (see FIXME at line 95). The parser sets
// transport_header.type to next_proto_id unconditionally, so payload bytes
// are read as ports. Remove the skip when the parser is fixed.
func TestACL_IPv4Fragment_LaterFragment_PortRule(t *testing.T) {
	t.Skip("TODO: parser does not detect non-first IPv4 fragments — fix lib/dataplane/packet/packet.c FIXME at line 95")
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
	// Non-first fragment: FragOffset=8 (64 bytes into the original datagram),
	// MF=0 — no L4 header, just raw payload.
	ip4.Protocol = layers.IPProtocolUDP
	ip4.Flags = 0
	ip4.FragOffset = 8

	// Payload bytes at offset 0-1 will be read as src-port, bytes 2-3 as
	// dst-port. We place 0x00 0x00 0x00 0x50 so dst-port reads as 80 (0x0050).
	payload := gopacket.Payload([]byte{0x00, 0x00, 0x00, 0x50, 0xAA, 0xBB, 0xCC, 0xDD})

	pkt := serializeFragPacket(t, &eth, &ip4, payload)

	rules := []cacl.AclRule{{
		Actions:       []cacl.AclAction{{Kind: cacl.ActionAllow}},
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:         filter.IPNets{filter.UnspecifiedIPv4},
		Src6s:         filter.IPNets{},
		Dst6s:         filter.IPNets{},
		SrcPortRanges: allPorts,
		DstPortRanges: filter.PortRanges{{From: 80, To: 80}},
		ProtoRanges:   udpProto,
	}}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	assert.Empty(t, result.Output, "non-first fragment must NOT match port-based rule")
	require.Len(t, result.Drop, 1, "non-first fragment must be dropped")
	requireModuleCounterPackets(t, h, aclCounterPath("port0", "test"), "acl_no_match", 1)
}

// TestACL_IPv4Fragment_LaterFragment_NoPortRule verifies that a non-first IPv4
// fragment matches a rule that uses only IP-level criteria (no port restriction).
//
// The filter_ip4 path (no port filter) matches on src/dst IP and protocol
// range only. Since the parser always sets transport_header.type to the
// IP next-proto field, a UDP proto-range rule matches even when no UDP header
// is present — the fragment passes through unchanged.
func TestACL_IPv4Fragment_LaterFragment_NoPortRule(t *testing.T) {
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
	ip4.Flags = 0
	ip4.FragOffset = 8

	payload := gopacket.Payload([]byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03, 0x04})
	pkt := serializeFragPacket(t, &eth, &ip4, payload)

	// allPorts for both src and dst — rule enters filter_ip4 AND filter_ip4_port.
	// Because filter_ip4 has a lower or equal result index, the packet is
	// allowed regardless of what the "port" bytes contain.
	rules := []cacl.AclRule{
		allow4Rule(
			filter.IPNets{filter.UnspecifiedIPv4},
			filter.IPNets{filter.UnspecifiedIPv4},
			udpProto,
		),
	}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	assert.Len(t, result.Output, 1, "non-first fragment must be allowed by IP-only rule")
	assert.Empty(t, result.Drop)

	requireModuleCounterPackets(t, h, aclCounterPath("port0", "test"), "acl_action_allow", 1)
}

// TestACL_IPv6Fragment_FirstFragment documents the parser's behavior for the
// first fragment of an IPv6 TCP datagram.
//
// The IPv6 parser walks the Fragment extension header (IPPROTO_FRAGMENT),
// advances offset past the 8-byte fragment header, and sets transport_header.type
// to TCP. For a first fragment (FragmentOffset=0) the TCP header immediately
// follows, so port matching operates on the real TCP ports.
func TestACL_IPv6Fragment_FirstFragment(t *testing.T) {
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolIPv6Fragment,
		SrcIP:      net.ParseIP("2001:db8::1"),
		DstIP:      net.ParseIP("2001:db8::2"),
	}
	// IPv6 with a Fragment extension header: NextHeader -> Fragment -> TCP.

	frag := layers.IPv6Fragment{
		NextHeader:     layers.IPProtocolTCP,
		FragmentOffset: 0,
		MoreFragments:  true,
		Identification: 0x12345678,
	}

	tcp := layers.TCP{SrcPort: 54321, DstPort: 443, SYN: true}
	tcp.SetNetworkLayerForChecksum(&ip6)

	pkt := serializeFragPacket(t, &eth, &ip6, &frag, &tcp)

	rules := []cacl.AclRule{{
		Actions:       []cacl.AclAction{{Kind: cacl.ActionAllow}},
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         filter.IPNets{},
		Dst4s:         filter.IPNets{},
		Src6s:         filter.IPNets{filter.UnspecifiedIPv6},
		Dst6s:         filter.IPNets{filter.UnspecifiedIPv6},
		SrcPortRanges: allPorts,
		DstPortRanges: filter.PortRanges{{From: 443, To: 443}},
		ProtoRanges:   tcpProto,
	}}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	// First fragment: real TCP header is present, port 443 matches the rule.
	assert.Len(t, result.Output, 1, "IPv6 first fragment with matching TCP port must be allowed")
	assert.Empty(t, result.Drop)

	requireModuleCounterPackets(t, h, aclCounterPath("port0", "test"), "acl_action_allow", 1)
}

// TestACL_IPv6Fragment_LaterFragment asserts that a non-first IPv6 fragment
// does NOT match a port-based rule, because the fragment has no L4 header
// and its "ports" are undefined.
//
// Currently SKIPPED because lib/dataplane/packet/packet.c does not check the
// IPv6 fragment offset (see FIXME at lines 174-180). The parser sets
// transport_header.type to the next-header field unconditionally, so payload
// bytes are read as ports. Remove the skip when the parser is fixed.
func TestACL_IPv6Fragment_LaterFragment(t *testing.T) {
	t.Skip("TODO: parser does not detect non-first IPv6 fragments — fix lib/dataplane/packet/packet.c FIXME at lines 174-180")
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv6,
	}
	ip6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolIPv6Fragment,
		SrcIP:      net.ParseIP("2001:db8::1"),
		DstIP:      net.ParseIP("2001:db8::2"),
	}

	// Non-first fragment: FragmentOffset=8 (64 bytes in), MoreFragments=false.
	frag := layers.IPv6Fragment{
		NextHeader:     layers.IPProtocolTCP,
		FragmentOffset: 8,
		MoreFragments:  false,
		Identification: 0x12345678,
	}

	// Bytes 0-1 = src-port = 0x0000, bytes 2-3 = dst-port = 0x01BB (443).
	// The payload must be at least sizeof(rte_tcp_hdr)=20 bytes so that
	// parse_packet's length check does not reject the packet before the
	// ACL handler is reached.
	payload := gopacket.Payload([]byte{
		0x00, 0x00, 0x01, 0xBB, // src-port=0, dst-port=443 (0x01BB)
		0x00, 0x00, 0x00, 0x00, // seq number
		0x00, 0x00, 0x00, 0x00, // ack number
		0x50, 0x00, 0x00, 0x00, // data offset + flags
		0x00, 0x00, 0x00, 0x00, // window + checksum
	})

	pkt := serializeFragPacket(t, &eth, &ip6, &frag, payload)

	rules := []cacl.AclRule{{
		Actions:       []cacl.AclAction{{Kind: cacl.ActionAllow}},
		Devices:       filter.Devices{{Name: "port0"}},
		Src4s:         filter.IPNets{},
		Dst4s:         filter.IPNets{},
		Src6s:         filter.IPNets{filter.UnspecifiedIPv6},
		Dst6s:         filter.IPNets{filter.UnspecifiedIPv6},
		SrcPortRanges: allPorts,
		DstPortRanges: filter.PortRanges{{From: 443, To: 443}},
		ProtoRanges:   tcpProto,
	}}

	h, agent, backend := setupACLHarness(t, []string{"port0"})
	applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	assert.Empty(t, result.Output, "non-first fragment must NOT match port-based rule")
	require.Len(t, result.Drop, 1, "non-first fragment must be dropped")
	requireModuleCounterPackets(t, h, aclCounterPath("port0", "test"), "acl_no_match", 1)
}
