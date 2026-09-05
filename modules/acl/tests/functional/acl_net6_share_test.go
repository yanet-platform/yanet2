package acl_test

// Differential verdict test for the shared net6 half-classification
// (filter_net6_share_init / acl_module_init_net6_share): the same rules and
// the same packets must produce identical per-packet actions whether the
// ACL module classifies filter_ip6 and filter_ip6_port independently or
// through the shared union tries.
//
// The gate compares actions and the full per-rule counter vector, and
// includes addresses chosen so the two local classifiers' partitions
// genuinely differ, a pair of destination networks reaching past /64 (so
// the dst-lo union partition is non-degenerate), a non-contiguous-across-
// 128-bit (but bi-contiguous) mask, and a later IPv6 fragment that only
// ever reaches filter_ip6. TestACL_Net6Share_VerdictParity_Scale
// additionally runs the same comparison at a larger rule count, and
// TestACL_Net6Share_VerdictParity_ScaleHeavy at a production-ish one,
// since a degenerate union partition at small scale can leave a remap bug
// undetectable.

import (
	"fmt"
	"net"
	"os"
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
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// net6ShareDisableEnv mirrors acl_module_init_net6_share's operational kill
// switch, checked once at ACL config compile time.
const net6ShareDisableEnv = "YANET_ACL_NET6_SHARE_DISABLE"

// setNet6ShareDisabled sets the sharing kill switch for the calling test
// when disabled is true. t.Setenv restores the previous value
// automatically on cleanup, so there is nothing to do in the false case:
// the ambient environment (expected unset) is left alone, and whether
// sharing actually built is verified independently in the "shared" subtest
// of runNet6ShareVerdictParity via GetInfo rather than by forcing the
// variable absent here.
func setNet6ShareDisabled(t *testing.T, disabled bool) {
	t.Helper()

	if disabled {
		t.Setenv(net6ShareDisableEnv, "1")
	}
}

// net6ShareEdgeCaseRules builds v6 rules chosen to stress the shared
// classification mechanism rather than the ACL action logic: the specific
// actions only need to be distinguishable, not meaningful.
func net6ShareEdgeCaseRules() []cacl.AclRule {
	// The divergeBroad rule has no port constraint (filter_ip6). The
	// divergeNested rule is a narrower network scoped to a destination port
	// (filter_ip6_port).
	// An address inside the nested network lands in a partition. The filter_ip6
	// classifier never has to distinguish it, exactly the case a broken remap
	// would smooth over.
	divergeBroad := xnetip.MustParseBiContiguous("2001:db8:1::/48")
	divergeNested := xnetip.MustParseBiContiguous("2001:db8:1:1::/ffff:ffff:ffff:ffff::")

	// The deepLoA and deepLoB networks are disjoint port-scoped ranges reaching
	// 112 bits deep, 48 past the hi/lo split at byte 8. Their dst-lo union has
	// three classes (default, deepLoA, deepLoB), with no network or port overlap.
	// Port scoping compiles them only into filter_ip6_port. filter_ip6 has no
	// matching fallback, so a dst-lo remap failure for either network cannot be
	// masked by the other filter's independent result.
	deepLoA := xnetip.MustParseBiContiguous("2001:db8:9::1000:0/ffff:ffff:ffff:ffff:ffff:ffff:ffff:0000")
	deepLoB := xnetip.MustParseBiContiguous("2001:db8:9::2000:0/ffff:ffff:ffff:ffff:ffff:ffff:ffff:0000")

	// The nonContiguous rule has a mask that is bi-contiguous (5 hi bytes, then 3
	// lo bytes) but not contiguous across the full 128 bits: exactly the
	// shape the net6 compiler accepts and a union partition can obscure.
	nonContiguous := xnetip.MustParseBiContiguous("bbbb:bbbb:bb00:0000:aaaa:aa00:0000:0000/ffff:ffff:ff00:0000:ffff:ff00:0000:0000")

	return []cacl.AclRule{
		{
			Counter:       "diverge_broad",
			Actions:       []cacl.AclAction{{Kind: cacl.ActionAllow}},
			Src6s:         []xnetip.BiContiguous{divergeBroad},
			Dst6s:         []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			SrcPortRanges: allPorts,
			DstPortRanges: allPorts,
			ProtoRanges:   tcpProto,
		},
		{
			Counter:       "diverge_nested",
			Actions:       []cacl.AclAction{{Kind: cacl.ActionCount}, {Kind: cacl.ActionDeny}},
			Src6s:         []xnetip.BiContiguous{divergeNested},
			Dst6s:         []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			SrcPortRanges: allPorts,
			DstPortRanges: filter.PortRanges{{From: 7000, To: 7000}},
			ProtoRanges:   tcpProto,
		},
		{
			Counter:       "deep_lo_a",
			Actions:       []cacl.AclAction{{Kind: cacl.ActionCount}, {Kind: cacl.ActionAllow}},
			Src6s:         []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			Dst6s:         []xnetip.BiContiguous{deepLoA},
			SrcPortRanges: allPorts,
			DstPortRanges: filter.PortRanges{{From: 8000, To: 8000}},
			ProtoRanges:   tcpProto,
		},
		{
			Counter:       "deep_lo_b",
			Actions:       []cacl.AclAction{{Kind: cacl.ActionCount}, {Kind: cacl.ActionAllow}},
			Src6s:         []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			Dst6s:         []xnetip.BiContiguous{deepLoB},
			SrcPortRanges: allPorts,
			DstPortRanges: filter.PortRanges{{From: 8001, To: 8001}},
			ProtoRanges:   tcpProto,
		},
		{
			Counter:       "non_contig_broad",
			Actions:       []cacl.AclAction{{Kind: cacl.ActionAllow}},
			Src6s:         []xnetip.BiContiguous{nonContiguous},
			Dst6s:         []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			SrcPortRanges: allPorts,
			DstPortRanges: allPorts,
			ProtoRanges:   udpProto,
		},
		{
			Counter:       "non_contig_port",
			Actions:       []cacl.AclAction{{Kind: cacl.ActionCount}, {Kind: cacl.ActionAllow}},
			Src6s:         []xnetip.BiContiguous{nonContiguous},
			Dst6s:         []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			SrcPortRanges: allPorts,
			DstPortRanges: filter.PortRanges{{From: 9000, To: 9000}},
			ProtoRanges:   udpProto,
		},
	}
}

// net6ShareEdgeCasePackets builds packets matching the rules from
// net6ShareEdgeCaseRules, plus a later IPv6 fragment addressed inside
// divergeBroad's network: fragments with a non-zero offset skip the port
// filter entirely (see acl_handle_packets), so this packet only ever
// reaches filter_ip6, exercising an address whose src half is classified
// for one signature but never the other.
func net6ShareEdgeCasePackets(t *testing.T) []gopacket.Packet {
	t.Helper()

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv6,
	}

	packets := make([]gopacket.Packet, 0, 6)

	buildTCP := func(src, dst string, dstPort uint16) {
		ip6 := layers.IPv6{
			Version:    6,
			HopLimit:   64,
			NextHeader: layers.IPProtocolTCP,
			SrcIP:      net.ParseIP(src),
			DstIP:      net.ParseIP(dst),
		}
		tcp := layers.TCP{SrcPort: 12345, DstPort: layers.TCPPort(dstPort), SYN: true}
		require.NoError(t, tcp.SetNetworkLayerForChecksum(&ip6))
		packets = append(packets, xpacket.LayersToPacket(t, &eth, &ip6, &tcp))
	}

	buildUDP := func(src, dst string, dstPort uint16) {
		ip6 := layers.IPv6{
			Version:    6,
			HopLimit:   64,
			NextHeader: layers.IPProtocolUDP,
			SrcIP:      net.ParseIP(src),
			DstIP:      net.ParseIP(dst),
		}
		udp := layers.UDP{SrcPort: 54321, DstPort: layers.UDPPort(dstPort)}
		require.NoError(t, udp.SetNetworkLayerForChecksum(&ip6))
		packets = append(packets, xpacket.LayersToPacket(t, &eth, &ip6, &udp))
	}

	// Lands in divergeBroad only (filter_ip6), not divergeNested.
	buildTCP("2001:db8:1:2::1", "2001:db8:aaaa::1", 6000)
	// Lands in both divergeBroad and divergeNested (nested inside the
	// broad supernet), the partition-divergence case proper.
	buildTCP("2001:db8:1:1::5", "2001:db8:aaaa::2", 7000)
	// Matches deepLoA on its own scoped port: filter_ip6 has no rule
	// covering this network at all, so this packet's action depends
	// entirely on filter_ip6_port's dst-lo classification of deepLoA.
	buildTCP("2001:db8:5::1", "2001:db8:9::1000:1", 8000)
	// Matches deepLoB the same way, on a disjoint network and port.
	buildTCP("2001:db8:5::2", "2001:db8:9::2000:1", 8001)
	// The non-contiguous-mask network matches via UDP and a scoped port,
	// reaching filter_ip6_port.
	buildUDP("bbbb:bbbb:bb00:0000:aaaa:aa00:0000:0001", "2001:db8:bbbb::1", 9000)

	// A later IPv6 fragment (FragmentOffset != 0) skips the port filter
	// regardless of its inner protocol, so only filter_ip6 ever sees it.
	fragIP6 := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolIPv6Fragment,
		SrcIP:      net.ParseIP("2001:db8:1:3::1"),
		DstIP:      net.ParseIP("2001:db8:cccc::1"),
	}
	frag := layers.IPv6Fragment{
		NextHeader:     layers.IPProtocolTCP,
		FragmentOffset: 8,
		MoreFragments:  false,
		Identification: 0xdeadbeef,
	}
	payload := gopacket.Payload([]byte{
		0x00, 0x00, 0x01, 0xBB, // src-port=0, dst-port=443
		0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
		0x50, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
	})
	packets = append(packets, serializeFragPacket(t, &eth, &fragIP6, &frag, payload))

	return packets
}

// net6SharePacketKey identifies a packet by its flow tuple, stable across
// the baseline and shared-classification runs regardless of output order.
func net6SharePacketKey(info *framework.PacketInfo) string {
	proto := info.Protocol
	if info.IsIPv6 {
		proto = info.NextHeader
	}
	return fmt.Sprintf(
		"%s|%s|%d|%d|%d",
		info.SrcIP, info.DstIP, info.SrcPort, info.DstPort, proto,
	)
}

// net6ShareResult holds the outcome of one compile-and-classify run: the
// per-flow verdicts, the resulting per-rule counter vectors, and the
// compiled configuration metadata used to confirm sharing actually had
// something to build.
type net6ShareResult struct {
	verdicts     map[string]string
	ruleCounters map[string][]uint64
	info         *cacl.AclConfigInfo
}

// collectNet6ShareVerdicts compiles rules once under the given sharing
// state and returns each surviving/dropped packet's verdict keyed by flow,
// and the per-rule counter vectors read back from shared memory.
func collectNet6ShareVerdicts(
	t *testing.T,
	rules []cacl.AclRule,
	packets []gopacket.Packet,
	shareDisabled bool,
	cpMemory, agentMemory datasize.ByteSize,
) net6ShareResult {
	t.Helper()

	setNet6ShareDisabled(t, shareDisabled)

	h, agent, backend := setupACLHarnessSized(t, []string{"port0"}, cpMemory, agentMemory)
	handle := applyACLRules(t, backend, "test", rules)
	wireACLPipeline(t, agent, "port0", "test")

	result, err := h.HandlePackets(packets...)
	require.NoError(t, err)

	verdicts := make(map[string]string, len(packets))
	for _, out := range result.Output {
		verdicts[net6SharePacketKey(out.PacketInfo)] = "output"
	}
	for _, info := range result.Drop {
		verdicts[net6SharePacketKey(info)] = "drop"
	}

	path := aclCounterPath("port0", "test")
	counters := dataplaneut.RuleCounters(t, h, path, nil)

	return net6ShareResult{
		verdicts:     verdicts,
		ruleCounters: dataplaneut.ValueCounters(counters),
		info:         handle.GetInfo(),
	}
}

// runNet6ShareVerdictParity builds a ruleset of datasetRuleCount dataset
// rules plus the fixed edge-case family, synthesizes packetCount flow
// packets, and asserts that the shared and unshared net6 classification
// paths agree on every packet's verdict and every per-rule counter.
//
// acl_module_init_net6_share returns without building the shared trie in
// three cases: the kill switch, filter_rule_count_ip6 == 0, and
// filter_rule_count_ip6_port == 0. The kill switch is closed off by the
// ambient-environment check below. The other two are closed off by
// asserting both rule counts are non-zero on the shared side, since
// net6ShareEdgeCaseRules always supplies both port-scoped and
// non-port-scoped v6 rules. Without that assertion, a ruleset lacking
// either family would make this subtest compile the unshared path on both
// sides and compare it against itself.
func runNet6ShareVerdictParity(
	t *testing.T,
	datasetRuleCount, packetCount int,
	cpMemory, agentMemory datasize.ByteSize,
) {
	rules := append(net6ShareEdgeCaseRules(), generateDatasetRules(datasetRuleCount)...)

	packets := net6ShareEdgeCasePackets(t)
	packets = append(packets, synthesizeDatasetPackets(t, rules, packetCount)...)

	var baseline, shared net6ShareResult
	t.Run("baseline", func(t *testing.T) {
		baseline = collectNet6ShareVerdicts(t, rules, packets, true, cpMemory, agentMemory)
	})
	t.Run("shared", func(t *testing.T) {
		_, present := os.LookupEnv(net6ShareDisableEnv)
		require.False(
			t, present,
			"%s is present in the ambient environment (acl_module_init_net6_share "+
				"keys off getenv() != NULL, not the value); this subtest would "+
				"compile the unshared path on both sides and compare it "+
				"against itself, making the differential check vacuous",
			net6ShareDisableEnv,
		)
		shared = collectNet6ShareVerdicts(t, rules, packets, false, cpMemory, agentMemory)
		require.True(
			t,
			shared.info.FilterRuleCountIp6 > 0 && shared.info.FilterRuleCountIp6Port > 0,
			"ruleset has filter_rule_count_ip6=%d filter_rule_count_ip6_port=%d; "+
				"acl_module_init_net6_share returns without building the "+
				"shared trie when either is zero, so this subtest would be "+
				"comparing the unshared path against itself",
			shared.info.FilterRuleCountIp6, shared.info.FilterRuleCountIp6Port,
		)
	})

	require.NotEmpty(t, baseline.verdicts)
	require.Equal(
		t, len(baseline.verdicts), len(shared.verdicts),
		"shared and baseline runs classified a different number of packets",
	)
	for key, wantVerdict := range baseline.verdicts {
		gotVerdict, ok := shared.verdicts[key]
		require.True(t, ok, "packet %q missing from the shared-classification result", key)
		require.Equal(
			t, wantVerdict, gotVerdict,
			"packet %q verdict diverges between baseline and shared net6 classification", key,
		)
	}

	require.Equal(
		t, baseline.ruleCounters, shared.ruleCounters,
		"per-rule counters diverge between baseline and shared net6 classification",
	)
}

// TestACL_Net6Share_VerdictParity verifies that enabling the shared net6
// half-classification never changes an ACL verdict: the same production-
// shaped ruleset and packet set, compiled once with sharing forced off and
// once with sharing left on, must agree on every packet's action and every
// per-rule counter.
//
// This uses its own sizes rather than the package-default aclCPSize/
// aclMemSize: under `-fsanitize=address,undefined` the filter compiler's
// allocator overhead pushes the default 64/16 MB harness out of memory
// while compiling filter_ip6_port, even for the unshared baseline subtest,
// so the failure tracks the sanitizer build, not the sharing change. 128/32
// MB was the smallest size found to pass under ASan. 192/48 MB adds
// headroom for allocator-layout and runner-to-runner variance while still
// measuring well under 512 MiB peak RSS in either build mode.
func TestACL_Net6Share_VerdictParity(t *testing.T) {
	const (
		net6ShareCPMemory  = 192 * datasize.MB
		net6ShareAgentSize = 48 * datasize.MB
	)
	runNet6ShareVerdictParity(t, 384, 512, net6ShareCPMemory, net6ShareAgentSize)
}

// TestACL_Net6Share_VerdictParity_Scale runs the same differential check at
// a rule count well past TestACL_Net6Share_VerdictParity's, so union
// partitions carry more than the tens-to-low-hundreds of classes a small
// ruleset produces.
//
// lib/dataplane_ut/dataplane_ut.c memsets its whole requested CPMemory
// arena up front, so RSS tracks the requested size directly regardless of
// how much of it the compiled config actually uses. In a normal build,
// 12000 dataset rules (plus the fixed edge-case family) measured a
// repeatable ~1.3-1.4 GiB peak RSS at 1300/975 MB. Under
// `-fsanitize=address,undefined` the filter compiler's allocator overhead
// is materially larger: 1300/975 MB no longer even compiles
// filter_ip6_port, and 1330/998 MB was the smallest size found to pass.
// 1500/1125 MB adds headroom over that empirical minimum and measured
// ~1.74 GiB peak RSS under ASan, still comfortably under the 2 GiB a
// standard CI runner can spare. This tier runs in the default `make test`
// and `make test-asan` paths, so it must stay well inside that budget in
// both build modes. TestACL_Net6Share_VerdictParity_ScaleHeavy carries the
// production-scale rule count instead.
func TestACL_Net6Share_VerdictParity_Scale(t *testing.T) {
	const (
		scaleRuleCount   = 12000
		scaleCPMemory    = 1500 * datasize.MB
		scaleAgentMemory = 1125 * datasize.MB
	)
	runNet6ShareVerdictParity(t, scaleRuleCount, 4096, scaleCPMemory, scaleAgentMemory)
}

// net6ShareScaleHeavyEnv gates
// TestACL_Net6Share_VerdictParity_ScaleHeavy, which compiles two
// ~40000-rule configs and peaks at roughly 24 GiB RSS: fine for a human to
// run on demand, far too heavy for the runners `make test` and
// `make test-asan` share with the rest of CI.
//
// Run it directly with:
//
//	ACL_NET6_SHARE_SCALE_HEAVY=1 go test -run TestACL_Net6Share_VerdictParity_ScaleHeavy \
//	    ./modules/acl/tests/functional/
const net6ShareScaleHeavyEnv = "ACL_NET6_SHARE_SCALE_HEAVY"

// TestACL_Net6Share_VerdictParity_ScaleHeavy runs the same differential
// check as TestACL_Net6Share_VerdictParity_Scale at a production-ish rule
// count.
//
// At TestACL_Net6Share_VerdictParity_Scale's rule count the union
// partitions are already non-degenerate, but production rulesets reach
// tens of thousands of rules, and a remap bug that only manifests once a
// partition grows past that count would slip through the smaller gates.
// Gated behind net6ShareScaleHeavyEnv rather than -short: neither
// `make test` nor `make test-asan` passes
// -short, so that alone would not have kept this off CI.
func TestACL_Net6Share_VerdictParity_ScaleHeavy(t *testing.T) {
	if os.Getenv(net6ShareScaleHeavyEnv) == "" {
		t.Skipf("set %s=1 to run the production-scale net6 share differential", net6ShareScaleHeavyEnv)
	}

	const (
		scaleCPMemory    = 24 * datasize.GB
		scaleAgentMemory = 16 * datasize.GB
	)
	runNet6ShareVerdictParity(t, 40000, 4096, scaleCPMemory, scaleAgentMemory)
}

// TestACL_Net6Share_EmptyRoundForcePoll runs one force-polled empty round.
//
// It builds the shared net6 trie, then calls acl_handle_packets with an
// empty input front. No Go-visible assertion can observe the zero-length-
// VLA defect this accompanies: it is undefined behavior the handler never
// indexes. This is the only test in acl's own suite that reaches an empty
// front, and, under a sanitizer build, the only one that extends that
// reach to the shared-classification arrays.
func TestACL_Net6Share_EmptyRoundForcePoll(t *testing.T) {
	const (
		emptyRoundCPMemory    = 192 * datasize.MB
		emptyRoundAgentMemory = 48 * datasize.MB
	)

	_, present := os.LookupEnv(net6ShareDisableEnv)
	require.False(
		t, present,
		"%s is present in the ambient environment (acl_module_init_net6_share "+
			"keys off getenv() != NULL, not the value); this test would compile "+
			"the unshared path and cover none of the shared-classification arrays",
		net6ShareDisableEnv,
	)

	harness, agent, backend := setupACLHarnessSized(t, []string{"port0"}, emptyRoundCPMemory, emptyRoundAgentMemory)
	handle := applyACLRules(t, backend, "test", net6ShareEdgeCaseRules())
	wireACLPipeline(t, agent, "port0", "test")

	info := handle.GetInfo()
	require.True(
		t,
		info.FilterRuleCountIp6 > 0 && info.FilterRuleCountIp6Port > 0,
		"ruleset has filter_rule_count_ip6=%d filter_rule_count_ip6_port=%d; "+
			"acl_module_init_net6_share returns without building the shared "+
			"trie when either is zero, so this test would exercise only the "+
			"unshared main-body arrays",
		info.FilterRuleCountIp6, info.FilterRuleCountIp6Port,
	)

	result, err := harness.HandlePackets()
	require.NoError(t, err)
	require.Empty(t, result.Output, "empty round must not produce output")
	require.Empty(t, result.Drop, "empty round must not produce drops")

	path := aclCounterPath("port0", "test")
	counters := harness.SharedMemory().DPConfig(0).ModuleCounters(
		path.Device, path.Pipeline, path.Function, path.Chain,
		path.ModuleType, path.ModuleName, nil,
	)
	counters = append(counters, dataplaneut.RuleCounters(t, harness, path, nil)...)
	for name, values := range dataplaneut.ValueCounters(counters) {
		for _, value := range values {
			require.Zero(
				t, value,
				"counter %q holds %d after an empty round; today every acl "+
					"counter stays at zero with no packet input, so this "+
					"failing means behavior changed and is worth checking, "+
					"not necessarily a defect",
				name, value,
			)
		}
	}

	// Positive control: proves the wiring is live and that this rule set
	// really does route through filter_ip6_port on the shared-classification
	// path, and that the guard above did not break that non-empty path. It
	// says nothing about the empty round itself, which this test cannot
	// observe for the reason given above.
	ethernetLayer := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv6,
	}
	ipv6Layer := layers.IPv6{
		Version:    6,
		HopLimit:   64,
		NextHeader: layers.IPProtocolTCP,
		SrcIP:      net.ParseIP("2001:db8:5::1"),
		DstIP:      net.ParseIP("2001:db8:9::1000:1"),
	}
	tcpLayer := layers.TCP{SrcPort: 12345, DstPort: 8000, SYN: true}
	require.NoError(t, tcpLayer.SetNetworkLayerForChecksum(&ipv6Layer))
	packet := xpacket.LayersToPacket(t, &ethernetLayer, &ipv6Layer, &tcpLayer)
	packetSize := uint64(len(packet.Data()))

	result, err = harness.HandlePackets(packet)
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "deep_lo_a packet must be allowed through")
	require.Empty(t, result.Drop)
	dataplaneut.RequireRuleCounter(t, harness, path, "deep_lo_a", 1, packetSize)
}
