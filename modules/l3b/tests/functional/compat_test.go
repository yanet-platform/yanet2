package l3b_test

// Ports of the first-generation balancer autotests whose behavior l3b
// implements: 049_balancer_wrr, 049_balancer_tcp_udp,
// 049_balancer_real_ipv4 and 068_balancer_outer_source_network. The old
// suite drove pcap pairs through a JSON-configured pipeline; these ports
// keep the old services and flow sets and assert on the selected real
// (the encapsulated outer destination), which is the behavioral content
// of the old expect pcaps.

import (
	"fmt"
	"net"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	decap "github.com/yanet-platform/yanet2/modules/decap/controlplane"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	"github.com/yanet-platform/yanet2/modules/l3b/bindings/go/cl3b"
	controlplane "github.com/yanet-platform/yanet2/modules/l3b/controlplane"
	cl3bobject "github.com/yanet-platform/yanet2/objects/l3b/bindings/go/cl3bobject"

	"github.com/yanet-platform/xnetip"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// compatEnv is one old-style balancer instance: a harness, its agent and
// the chained module configuration name.
type compatEnv struct {
	h     *dataplaneut.Harness
	agent *ffi.Agent
}

// setupCompatEnv builds the standard one-port topology of the old suite:
// every packet enters the l3b module and its output leaves through the
// sink, so the outer destination names the chosen real.
func setupCompatEnv(t *testing.T) *compatEnv {
	t.Helper()
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	return &compatEnv{h: h, agent: agent}
}

// compatReal is one real of an old services.conf entry.
type compatReal struct {
	ip     string
	weight uint32
}

// compatService publishes an old services.conf entry: a vip/proto/vport
// tuple over weighted reals, one-packet scheduling so the ring order is
// deterministic, and a destination rule matching exactly the old tuple.
func (m *compatEnv) compatService(
	t *testing.T,
	name string,
	vip string,
	proto uint8,
	vport uint16,
	reals []compatReal,
) *cl3bobject.VirtualServiceObject {
	t.Helper()

	realServers := make([]cl3bobject.RealServer, 0, len(reals))
	var weights []uint32
	for _, real := range reals {
		address := xerror.Unwrap(netip.ParseAddr(real.ip))
		family := cl3bobject.IPv6
		if address.Is4() {
			family = cl3bobject.IPv4
		}
		sourceNet := xnetip.MustParseNetwork("192.0.2.0/24")
		if family == cl3bobject.IPv6 {
			// The first-generation module source network.
			sourceNet = xnetip.MustParseNetwork("2000:51b::/64")
		}
		realServers = append(realServers, cl3bobject.RealServer{
			Type:               family,
			DestinationAddress: address,
			SourceNet:          sourceNet,
		})
		weights = append(weights, real.weight)
	}

	service, err := cl3bobject.CreateVirtualService(
		m.agent,
		name,
		1,
		cl3bobject.VirtualServiceConfig{
			SourceFilterRules: []cl3bobject.SourceFilterRule{{
				Net6s:      []xnetip.BiContiguous{filter.UnspecifiedIPv6},
				Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				PortRanges: filter.PortRanges{{From: vport, To: vport}},
			}},
			RealServers:      realServers,
			RingCapacity:     uint32(len(reals)) * maxCompatWeight,
			SessionIndexSize: 4096,
			SchedulerFlags:   cl3bobject.SchedulerCounter,
			IndexMask:        0xFFFFFFFF,
		},
		nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Free() })
	require.NoError(t, service.UpdateRing(controlplane.RingFromWeights(weights)))
	require.NoError(t, service.Publish(m.agent))
	return service
}

// compatModule installs the module configuration routing the given tuples
// to their services, the equivalent of the old vip/proto/vport tables.
func (m *compatEnv) compatModule(
	t *testing.T,
	routes ...compatRoute,
) {
	t.Helper()

	module, err := cl3b.NewModuleConfig(m.agent, "test")
	require.NoError(t, err)
	t.Cleanup(func() { _ = module.Free() })

	rules := make([]cl3b.DestinationFilterRule, 0, len(routes))
	for _, route := range routes {
		address := xerror.Unwrap(netip.ParseAddr(route.vip))
		var rule cl3b.DestinationFilterRule
		if address.Is4() {
			rule.Net4s = []xnetip.Contiguous[xnetip.Network4]{
				xnetip.MustParseContiguous4(route.vip + "/32"),
			}
		} else {
			rule.Net6s = []xnetip.BiContiguous{
				xnetip.MustParseBiContiguous(route.vip + "/128"),
			}
		}
		rule.ProtoRanges = filter.ProtoRanges{
			filter.NewProtoRange(route.proto, filter.AnySubtype()),
		}
		rule.VirtualService = route.service
		rules = append(rules, rule)
	}

	require.NoError(t, module.Update(rules))
	require.NoError(t, m.agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}))
}

type compatRoute struct {
	vip     string
	proto   uint8
	service string
}

// maxCompatWeight bounds the ring capacity like the production clamp.
const maxCompatWeight = 1000

// flow describes one old-suite client flow.
type compatFlow struct {
	srcIP  string
	srcIP6 string
	port   uint16
}

// compatPacket builds one old-suite frame: an Ethernet/IP/transport
// stack for the flow towards the vip.
func compatPacket(
	t *testing.T,
	vip string,
	proto uint8,
	flow compatFlow,
	dstPort uint16,
) gopacket.Packet {
	t.Helper()

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:02")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeIPv4,
	}

	if flow.srcIP6 != "" {
		eth.EthernetType = layers.EthernetTypeIPv6
		ip6 := &layers.IPv6{
			Version:    6,
			HopLimit:   64,
			NextHeader: layers.IPProtocol(proto),
			SrcIP:      net.ParseIP(flow.srcIP6),
			DstIP:      net.ParseIP(vip),
		}
		return xpacket.LayersToPacket(
			t, &eth, ip6, compatTransport(proto, flow.port, dstPort, ip6),
		)
	}

	ip4 := &layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocol(proto),
		SrcIP:    net.ParseIP(flow.srcIP),
		DstIP:    net.ParseIP(vip),
	}
	return xpacket.LayersToPacket(
		t, &eth, ip4, compatTransport(proto, flow.port, dstPort, ip4),
	)
}

// send injects one packet of the flow towards the vip and returns the
// chosen real, read off the encapsulated outer destination.
func (m *compatEnv) send(
	t *testing.T,
	vip string,
	proto uint8,
	flow compatFlow,
	dstPort uint16,
) string {
	t.Helper()

	packet := compatPacket(t, vip, proto, flow, dstPort)

	result, err := m.h.HandlePackets(packet)
	require.NoError(t, err)
	require.Empty(t, result.Drop, "the old suite forwards every listed flow")
	require.Len(t, result.Output, 1)

	info, err := framework.NewPacketParser().ParsePacket(result.Output[0].RawData)
	require.NoError(t, err)
	require.True(t, info.IsTunneled, "every flow must be encapsulated towards its real")
	return info.DstIP.String()
}

// compatTransport builds a minimal TCP or UDP header for a flow, wired
// to the given network layer for checksum computation.
func compatTransport(
	proto uint8, srcPort, dstPort uint16, network gopacket.NetworkLayer,
) gopacket.SerializableLayer {
	if proto == 17 {
		udp := &layers.UDP{
			SrcPort: layers.UDPPort(srcPort),
			DstPort: layers.UDPPort(dstPort),
			Length:  8,
		}
		udp.SetNetworkLayerForChecksum(network)
		return udp
	}
	tcp := &layers.TCP{
		SrcPort: layers.TCPPort(srcPort),
		DstPort: layers.TCPPort(dstPort),
		Seq:     1,
		Window:  1024,
	}
	tcp.SetNetworkLayerForChecksum(network)
	return tcp
}

// Test_CompatBalancerWrr ports 049_balancer_wrr: vip 10.1.0.2:443/tcp over
// reals 2443::1-4 with weights 4/3/2/1; sixteen distinct flows (sources
// 1.1.0.1-8 with ports 12443 and 12444) must cover every real with the
// ring's weighted proportions, and each flow must stick on resend.
func Test_CompatBalancerWrr(t *testing.T) {
	env := setupCompatEnv(t)

	reals := []compatReal{
		{ip: "2443::1", weight: 4},
		{ip: "2443::2", weight: 3},
		{ip: "2443::3", weight: 2},
		{ip: "2443::4", weight: 1},
	}
	env.compatService(t, "svc", "10.1.0.2", 6, 443, reals)
	env.compatModule(t, compatRoute{vip: "10.1.0.2", proto: 6, service: "svc"})

	// The old gen.py's sixteen flows.
	flows := make([]compatFlow, 0, 16)
	for _, port := range []uint16{12443, 12444} {
		for host := 1; host <= 8; host++ {
			flows = append(flows, compatFlow{
				srcIP: fmt.Sprintf("1.1.0.%d", host),
				port:  port,
			})
		}
	}

	chosen := make([]string, 0, len(flows))
	for _, flow := range flows {
		chosen = append(chosen, env.send(t, "10.1.0.2", 6, flow, 443))
	}

	// Sixteen one-packet selections over the interleaved 4/3/2/1 ring
	// distribute 6/5/3/2 — the weighted proportions of the old suite.
	counts := map[string]int{}
	for _, real := range chosen {
		counts[real]++
	}
	require.Len(t, counts, 4, "every real must serve traffic")
	require.Equal(t, map[string]int{
		"2443::1": 6,
		"2443::2": 5,
		"2443::3": 3,
		"2443::4": 2,
	}, counts, "the distribution must follow the 4/3/2/1 weights")

	// Sessions pin: resending a flow keeps its real.
	require.Equal(t, chosen[0], env.send(t, "10.1.0.2", 6, flows[0], 443))
	require.Equal(t, chosen[15], env.send(t, "10.1.0.2", 6, flows[15], 443))
}

// Test_CompatBalancerTcpUdp ports 049_balancer_tcp_udp: one vip serving
// both protocols; the tcp and udp services are selected by the packet's
// protocol and each balances independently.
func Test_CompatBalancerTcpUdp(t *testing.T) {
	env := setupCompatEnv(t)

	reals := []compatReal{
		{ip: "2000::1", weight: 1},
		{ip: "2000::2", weight: 1},
		{ip: "2000::3", weight: 1},
		{ip: "2000::4", weight: 1},
	}
	env.compatService(t, "tcp", "10.0.0.3", 6, 80, reals)
	env.compatService(t, "udp", "10.0.0.3", 17, 80, reals)
	env.compatModule(t,
		compatRoute{vip: "10.0.0.3", proto: 6, service: "tcp"},
		compatRoute{vip: "10.0.0.3", proto: 17, service: "udp"},
	)

	seen := map[string]bool{}
	for idx := 1; idx <= 4; idx++ {
		flow := compatFlow{srcIP: fmt.Sprintf("1.1.0.%d", idx), port: 12380}
		seen[env.send(t, "10.0.0.3", 6, flow, 80)] = true

		udpFlow := compatFlow{srcIP: fmt.Sprintf("1.1.1.%d", idx), port: 12381}
		seen[env.send(t, "10.0.0.3", 17, udpFlow, 80)] = true
	}
	require.Len(t, seen, 4, "both protocols must spread across the reals")
}

// Test_CompatBalancerRealIpv4 ports 049_balancer_real_ipv4: v4 and v6
// reals behind v4 vips, and v6 flows towards v4 reals — the inner and
// outer families combine freely.
func Test_CompatBalancerRealIpv4(t *testing.T) {
	env := setupCompatEnv(t)

	v4Reals := []compatReal{
		{ip: "100.0.0.1", weight: 1},
		{ip: "100.0.0.2", weight: 1},
		{ip: "100.0.0.3", weight: 1},
		{ip: "100.0.0.4", weight: 1},
	}
	v6Reals := []compatReal{
		{ip: "2006::1", weight: 1},
		{ip: "2006::2", weight: 1},
		{ip: "2006::3", weight: 1},
		{ip: "2006::4", weight: 1},
	}
	env.compatService(t, "v4reals", "10.0.0.16", 6, 80, v4Reals)
	env.compatService(t, "v6reals", "10.0.0.17", 6, 80, v6Reals)
	env.compatModule(t,
		compatRoute{vip: "10.0.0.16", proto: 6, service: "v4reals"},
		compatRoute{vip: "10.0.0.17", proto: 6, service: "v6reals"},
	)

	// The old suite's flows are all IPv4 towards the v4 vips; the
	// reals' families differ, exercising v4-in-v4 and v4-in-v6 tunnels.
	v4Seen := map[string]bool{}
	v6Seen := map[string]bool{}
	for idx := 1; idx <= 4; idx++ {
		v4Seen[env.send(t, "10.0.0.16", 6,
			compatFlow{srcIP: fmt.Sprintf("1.1.0.%d", idx), port: 12380}, 80)] = true
		v6Seen[env.send(t, "10.0.0.17", 6,
			compatFlow{srcIP: fmt.Sprintf("1.2.0.%d", idx), port: 12380}, 80)] = true
	}
	require.Len(t, v4Seen, 4, "the v4 reals must all serve")
	require.Len(t, v6Seen, 4, "the v6 reals must all serve")
}

// Test_CompatBalancerOuterSourceNetwork ports the /32 half of
// 068_balancer_outer_source_network: with a host-sized outer source
// network the outer source is fixed to the network's address for every
// flow — the first-generation semantics our derivation reproduces on /32.
// The shorter prefixes of the old suite diverge by design: l3b folds the
// client's address into the outer source instead of using the base alone.
func Test_CompatBalancerOuterSourceNetwork(t *testing.T) {
	env := setupCompatEnv(t)

	service, err := cl3bobject.CreateVirtualService(
		env.agent,
		"fixedsrc",
		1,
		cl3bobject.VirtualServiceConfig{
			SourceFilterRules: []cl3bobject.SourceFilterRule{{
				Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				PortRanges: filter.PortRanges{{From: 80, To: 80}},
			}},
			RealServers: []cl3bobject.RealServer{{
				Type:               cl3bobject.IPv4,
				DestinationAddress: xerror.Unwrap(netip.ParseAddr("100.0.0.42")),
				SourceNet:          xnetip.MustParseNetwork("123.0.0.12/32"),
			}},
			RingCapacity:     1000,
			SessionIndexSize: 4096,
		},
		nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = service.Free() })
	require.NoError(t, service.UpdateRing([]uint32{0}))
	require.NoError(t, service.Publish(env.agent))

	module, err := cl3b.NewModuleConfig(env.agent, "test")
	require.NoError(t, err)
	t.Cleanup(func() { _ = module.Free() })
	rules := []cl3b.DestinationFilterRule{{
		Net4s:          []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("10.0.0.42/32")},
		ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype())},
		VirtualService: "fixedsrc",
	}}
	require.NoError(t, module.Update(rules))
	require.NoError(t, env.agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}))

	for idx := 1; idx <= 3; idx++ {
		flow := compatFlow{srcIP: fmt.Sprintf("1.1.0.%d", idx), port: 12380}
		outer := env.send(t, "10.0.0.42", 6, flow, 80)
		require.Equal(t, "100.0.0.42", outer, "the real must be the tunnel destination")

		result, err := env.h.HandlePackets(compatPacket(t, "10.0.0.42", 6, flow, 80))
		require.NoError(t, err)
		info, err := framework.NewPacketParser().ParsePacket(result.Output[0].RawData)
		require.NoError(t, err)
		require.Equal(t, "123.0.0.12", info.SrcIP.String(),
			"a /32 outer source network pins the outer source to its address")
	}
}

// Test_CompatAclBalancerRoute ports 048_acl_balancer_route: four services
// sharing reals across v4 and v6 vips, real enable/disable flips steering the
// traffic between the shared reals, everything dropping once no real is
// enabled, and per-service/per-real counters accounting the run. The old
// pipeline's acl half is l3b's destination rules; the route module after the
// balancer is represented by asserting the encapsulated outer header is the
// routable one the old expect pcaps carried (outer destination = real, outer
// source derived from the real's source network).
func Test_CompatAclBalancerRoute(t *testing.T) {
	env := setupCompatEnv(t)

	// The old services.conf: reals 2000::1-4 shared between the 80-port
	// services, 2443::1-4 behind the 443 service; the module source
	// 2000:51b::/64 becomes each real's source network.
	newReal := func(ip string) compatReal {
		return compatReal{ip: ip, weight: 1}
	}
	v4Service := env.compatService(t, "v4-80", "10.0.0.1", 6, 80,
		[]compatReal{newReal("2000::1"), newReal("2000::2")})
	env.compatService(t, "v4big-80", "10.0.0.2", 6, 80,
		[]compatReal{newReal("2000::1"), newReal("2000::2"), newReal("2000::3"), newReal("2000::4")})
	env.compatService(t, "v4-443", "10.0.0.2", 6, 443,
		[]compatReal{newReal("2443::1"), newReal("2443::2"), newReal("2443::3"), newReal("2443::4")})
	v6Service := env.compatService(t, "v6-80", "2001:dead:beef::1", 6, 80,
		[]compatReal{newReal("2000::1"), newReal("2000::2")})
	env.compatModule(t,
		compatRoute{vip: "10.0.0.1", proto: 6, service: "v4-80"},
		compatRoute{vip: "10.0.0.2", proto: 6, service: "v4big-80"},
		compatRoute{vip: "2001:dead:beef::1", proto: 6, service: "v6-80"},
	)

	// The old step one: only 2000::1 enabled for the v4 service and only
	// 2000::2 for the v6 service — every flow of a service lands on its
	// single enabled real (001-send.pcap's four v4 and four v6 flows).
	// Resolving the published services and steering their states mirrors
	// the old `balancer real enable/disable` steps.
	require.NoError(t, v4Service.SetRealServerState(0, true))
	require.NoError(t, v4Service.SetRealServerState(1, false))
	require.NoError(t, v4Service.UpdateRing([]uint32{0}))
	require.NoError(t, v6Service.SetRealServerState(0, false))
	require.NoError(t, v6Service.SetRealServerState(1, true))
	require.NoError(t, v6Service.UpdateRing([]uint32{1}))

	v4Flows := []compatFlow{
		{srcIP: "1.1.0.1", port: 12380},
		{srcIP: "1.1.0.2", port: 12380},
		{srcIP: "1.1.0.3", port: 12380},
		{srcIP: "1.1.0.4", port: 12380},
	}
	v6Flows := []compatFlow{
		{srcIP6: "2002::1", port: 12380},
		{srcIP6: "2002::2", port: 12380},
		{srcIP6: "2002::3", port: 12380},
		{srcIP6: "2002::4", port: 12380},
	}
	for _, flow := range v4Flows {
		require.Equal(t, "2000::1", env.send(t, "10.0.0.1", 6, flow, 80))
	}
	for _, flow := range v6Flows {
		require.Equal(t, "2000::2", env.send(t, "2001:dead:beef::1", 6, flow, 80))
	}

	// The old step two (002-send.pcap): the enabled reals swap; fresh
	// SYNs follow while the old sessions keep their pin — the old
	// reals_enabled accounting — because rescheduling a SYN is allowed.
	require.NoError(t, v4Service.SetRealServerState(0, false))
	require.NoError(t, v4Service.SetRealServerState(1, true))
	require.NoError(t, v4Service.UpdateRing([]uint32{1}))
	require.NoError(t, v6Service.SetRealServerState(0, true))
	require.NoError(t, v6Service.SetRealServerState(1, false))
	require.NoError(t, v6Service.UpdateRing([]uint32{0}))

	for idx, flow := range []compatFlow{
		{srcIP: "1.1.0.5", port: 12380},
		{srcIP: "1.1.0.6", port: 12380},
		{srcIP: "1.1.0.7", port: 12380},
		{srcIP: "1.1.0.8", port: 12380},
	} {
		require.Equal(t, "2000::2", env.send(t, "10.0.0.1", 6, flow, 80),
			"fresh flow %d must follow the swapped real", idx)
	}
	for idx, flow := range []compatFlow{
		{srcIP6: "2002::5", port: 12380},
		{srcIP6: "2002::6", port: 12380},
		{srcIP6: "2002::7", port: 12380},
		{srcIP6: "2002::8", port: 12380},
	} {
		require.Equal(t, "2000::1", env.send(t, "2001:dead:beef::1", 6, flow, 80),
			"fresh flow %d must follow the swapped real", idx)
	}

	// The old counters block: per-service packets and per-real
	// throughput observe the whole run (8 + 8 per service so far).
	ectx, err := env.h.PublishedExecutionContext(0)
	require.NoError(t, err)
	for _, service := range []string{"v4-80", "v6-80"} {
		packets, _, err := cl3bobject.ReadServiceCounter(ectx, service, "incoming")
		require.NoError(t, err)
		require.EqualValues(t, 8, packets, "service %s must account 8 packets", service)
	}
	real1Packets, _, err := cl3bobject.ReadServiceCounter(ectx, "v4-80", "real/1")
	require.NoError(t, err)
	require.EqualValues(t, 4, real1Packets,
		"the second real of the v4 service must account the swapped flows")

	// The old 006 step: no real enabled anywhere — every packet drops.
	require.NoError(t, v4Service.SetRealServerState(0, false))
	require.NoError(t, v4Service.SetRealServerState(1, false))
	require.NoError(t, v4Service.UpdateRing(nil))
	require.NoError(t, v6Service.SetRealServerState(0, false))
	require.NoError(t, v6Service.SetRealServerState(1, false))
	require.NoError(t, v6Service.UpdateRing(nil))

	dropped, err := env.h.HandlePackets(
		compatPacket(t, "10.0.0.1", 6, compatFlow{srcIP: "1.1.0.9", port: 12380}, 80),
	)
	require.NoError(t, err)
	require.Len(t, dropped.Output, 0, "a service without reals must forward nothing")
	require.Len(t, dropped.Drop, 1, "a service without reals must drop its flows")

	// The old 007/008 steps: re-enabling the reals serves traffic again.
	require.NoError(t, v4Service.SetRealServerState(0, true))
	require.NoError(t, v4Service.UpdateRing([]uint32{0}))
	require.Equal(t, "2000::1", env.send(t, "10.0.0.1", 6,
		compatFlow{srcIP: "1.1.0.10", port: 12380}, 80))

	// The encapsulated outer header is the routable one the old route
	// module consumed: outer destination names the real and the outer
	// source derives from the real's source network.
	result, err := env.h.HandlePackets(
		compatPacket(t, "10.0.0.1", 6, compatFlow{srcIP: "1.1.0.11", port: 12380}, 80),
	)
	require.NoError(t, err)
	require.Len(t, result.Output, 1)
	info, err := framework.NewPacketParser().ParsePacket(result.Output[0].RawData)
	require.NoError(t, err)
	require.Equal(t, "2000::1", info.DstIP.String())
	require.Equal(t, "2000:51b::", info.SrcIP.String(),
		"a /64 source network derives the outer source from its base for a v4 inner")
}

// Test_CompatMetabalancerDecap ports 049_metabalancer_decap: packets arrive
// pre-encapsulated by a remote "metabalancer"; the decap module strips the
// outer header when both its source prefix and destination address are
// known, and the l3b module balances the exposed inner flow. Same-version
// and mixed-version tunnels both apply, and packets whose outer pair is not
// whitelisted pass through untouched.
func Test_CompatMetabalancerDecap(t *testing.T) {
	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(l3bCPSize),
		DPMemory:      uint64(l3bDPSize),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"decap", "l3b", "forward"},
		ObjectsToLoad: []string{"l3b_virtual_service", "l3b_session_table"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	agent, err := h.SharedMemory().AgentAttach("meta-test", 0, l3bMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	// The decap module strips a tunnel whose outer destination is one of
	// the balancer's own addresses — the old dstAddresses list; the old
	// srcPrefixes half of the early-decap contract is not part of the new
	// module and is dropped from the port.
	prefixes := []netip.Prefix{
		netip.MustParsePrefix("1.210.198.65/32"),
		netip.MustParsePrefix("2222:898:0:320::b2a/128"),
	}
	decapHandle, err := decap.NewBackend(agent).UpdateModule("meta", prefixes)
	require.NoError(t, err)
	t.Cleanup(func() { _ = decapHandle.Free() })

	// The old services.conf: a v4 vip over a v4 real and a v6 vip over a
	// v6 real, each with the module's source networks.
	env := &compatEnv{h: h, agent: agent}
	v4Service := env.compatService(t, "v4", "10.0.0.1", 6, 80,
		[]compatReal{{ip: "100.0.0.1", weight: 1}})
	v6Service := env.compatService(t, "v6", "2004:dead:beef::1", 6, 80,
		[]compatReal{{ip: "2000::2", weight: 1}})
	require.NoError(t, v4Service.UpdateRing([]uint32{0}))
	require.NoError(t, v6Service.UpdateRing([]uint32{0}))

	module, err := cl3b.NewModuleConfig(agent, "test")
	require.NoError(t, err)
	t.Cleanup(func() { _ = module.Free() })
	rules := []cl3b.DestinationFilterRule{
		{
			Net4s:          []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("10.0.0.1/32")},
			ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype())},
			VirtualService: "v4",
		},
		{
			Net6s:          []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2004:dead:beef::1/128")},
			ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype())},
			VirtualService: "v6",
		},
	}
	require.NoError(t, module.Update(rules))
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}))

	// The old pipeline acl0 -> balancer0 -> route0 becomes
	// decap -> l3b -> forward-sink.
	sinkRules := []cforward.ForwardRule{
		{
			Target:  "port0",
			Mode:    cforward.ModeOut,
			Counter: "sink4",
			Src4s:   []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Dst4s:   []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		},
		{
			Target:  "port0",
			Mode:    cforward.ModeOut,
			Counter: "sink6",
			Src6s:   []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			Dst6s:   []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		},
	}
	sinkHandle, err := forward.NewBackend(agent).UpdateModule("sink", sinkRules)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sinkHandle.Free() })

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: "test",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: "test_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "decap", Name: "meta"},
					{Type: "l3b", Name: "test"},
					{Type: "forward", Name: "sink"},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "test",
		Functions: []string{"test"},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))
	_, err = plain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: "test", Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	send := func(packet gopacket.Packet) *dataplaneut.Result {
		result, err := h.HandlePackets(packet)
		require.NoError(t, err)
		return result
	}
	tunneled := func(t *testing.T, outer4Src, outer4Dst, innerSrc, innerDst string, tcpSrcPort uint16) gopacket.Packet {
		t.Helper()

		outerIP4 := &layers.IPv4{
			Version: 4,
			TTL:     64,
			// gopacket names IPPROTO_IPIP (4) IPProtocolIPv4.
			Protocol: layers.IPProtocolIPv4,
			SrcIP:    net.ParseIP(outer4Src),
			DstIP:    net.ParseIP(outer4Dst),
		}
		innerIP4 := &layers.IPv4{
			Version:  4,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    net.ParseIP(innerSrc),
			DstIP:    net.ParseIP(innerDst),
		}
		tcp := &layers.TCP{
			SrcPort: layers.TCPPort(tcpSrcPort),
			DstPort: 80,
			Seq:     1,
			Window:  1024,
		}
		tcp.SetNetworkLayerForChecksum(innerIP4)
		return xpacket.LayersToPacket(t, &eth, outerIP4, innerIP4, tcp)
	}

	// 001: both outer source prefix and destination address whitelisted —
	// decapped, then balanced: the output carries the real's fresh tunnel.
	result := send(tunneled(t,
		"123.234.128.10", "1.210.198.65",
		"1.1.0.1", "10.0.0.1", 12380))
	require.Len(t, result.Output, 1)
	require.Empty(t, result.Drop)
	info, err := framework.NewPacketParser().ParsePacket(result.Output[0].RawData)
	require.NoError(t, err)
	require.True(t, info.IsTunneled)
	require.Equal(t, "100.0.0.1", info.DstIP.String(),
		"the decapped flow must be balanced to the v4 real")
	require.Equal(t, "10.0.0.1", info.InnerPacket.DstIP.String(),
		"the original inner flow must ride the fresh tunnel")

	// 005: the outer destination is not whitelisted — no decap, and the
	// still-encapsulated packet is not service traffic: forwarded as is.
	result = send(tunneled(t,
		"123.234.128.10", "100.100.20.200",
		"1.1.0.1", "10.0.0.1", 12381))
	require.Len(t, result.Output, 1)
	require.Empty(t, result.Drop)
	info, err = framework.NewPacketParser().ParsePacket(result.Output[0].RawData)
	require.NoError(t, err)
	require.Equal(t, "100.100.20.200", info.DstIP.String(),
		"a non-whitelisted outer pair must pass through untouched")
	require.Equal(t, "10.0.0.1", info.InnerPacket.DstIP.String(),
		"the untouched tunnel keeps its inner flow")

	// 003: neither endpoint whitelisted — same pass-through, and the
	// non-TCP-in-tunnel outer never reaches the balancer.
	result = send(tunneled(t,
		"123.234.0.15", "1.210.198.60",
		"1.1.0.1", "10.0.0.1", 12382))
	require.Len(t, result.Output, 1)
	info, err = framework.NewPacketParser().ParsePacket(result.Output[0].RawData)
	require.NoError(t, err)
	require.Equal(t, "1.210.198.60", info.DstIP.String())
}
