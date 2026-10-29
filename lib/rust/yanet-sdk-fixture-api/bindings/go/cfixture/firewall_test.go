//go:build sdk_rust_fixtures

package cfixture_test

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"slices"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/xnetip"
	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	"github.com/yanet-platform/yanet2/modules/acl/tests/rulegen"
)

// The IP protocol numbers of the test frames.
const (
	protoTCP = 6
	protoUDP = 17
)

// protocol returns the authored range of every transport byte of the
// protocol.
func protocol(number uint16) filter.ProtoRanges {
	return filter.ProtoRanges{{From: number << 8, To: number<<8 | 0xff}}
}

// port returns the range of the single port.
func port(number uint16) filter.PortRanges {
	return filter.PortRanges{{From: number, To: number}}
}

// nets4 returns the parsed IPv4 networks, which must be valid.
func nets4(texts ...string) []xnetip.Contiguous[xnetip.Network4] {
	parsed := make([]xnetip.Contiguous[xnetip.Network4], 0, len(texts))
	for _, text := range texts {
		parsed = append(parsed, xnetip.MustParseContiguous4(text))
	}
	return parsed
}

// nets6 returns the parsed IPv6 networks, which must be valid.
func nets6(texts ...string) []xnetip.BiContiguous {
	parsed := make([]xnetip.BiContiguous, 0, len(texts))
	for _, text := range texts {
		parsed = append(parsed, xnetip.MustParseBiContiguous(text))
	}
	return parsed
}

var (
	allow = []cacl.ACLAction{{Kind: cacl.ActionAllow}}
	deny  = []cacl.ACLAction{{Kind: cacl.ActionDeny}}
)

// firewallInput returns a ruleset over both families, where a broad deny
// follows a narrow allow of TCP to port 80 and of UDP to port 53, and a
// catch-all allow of IPv4 UDP comes last.
//
// Two rules lead it scoped to a device: a deny of the TCP flow to port 80 on
// port1, which the packets of port0 must not meet, and an allow of TCP to port
// 8080 on port0, ahead of the broad deny.
func firewallInput(t testing.TB) *cfixture.FirewallInput {
	t.Helper()

	var input cfixture.FirewallInput
	for _, rule := range []cacl.ACLRule{
		{
			Devices: filter.Devices{{Name: "port1"}},
			Src4s:   nets4("10.0.0.0/8"), Dst4s: nets4("192.168.1.0/24"),
			ProtoRanges: protocol(protoTCP), DstPortRanges: port(80), Actions: deny,
		},
		{
			Devices: filter.Devices{{Name: "port0"}},
			Src4s:   nets4("10.0.0.0/8"), Dst4s: nets4("0.0.0.0/0"),
			ProtoRanges: protocol(protoTCP), DstPortRanges: port(8080), Actions: allow,
		},
		{
			Src4s: nets4("10.0.0.0/8"), Dst4s: nets4("192.168.1.0/24"),
			ProtoRanges: protocol(protoTCP), DstPortRanges: port(80), Actions: allow,
		},
		{
			Src4s: nets4("10.0.0.0/8"), Dst4s: nets4("0.0.0.0/0"),
			ProtoRanges: protocol(protoTCP), Actions: deny,
		},
		{
			Src6s: nets6("2001:db8::/32"), Dst6s: nets6("::/0"),
			ProtoRanges: protocol(protoUDP), DstPortRanges: port(53), Actions: allow,
		},
		{
			Src6s: nets6("::/0"), Dst6s: nets6("::/0"), Actions: deny,
		},
		{
			Src4s: nets4("0.0.0.0/0"), Dst4s: nets4("0.0.0.0/0"),
			ProtoRanges: protocol(protoUDP), Actions: allow,
		},
	} {
		require.NoError(t, input.Add(rule))
	}
	return &input
}

// transport returns a TCP or UDP header between the ports.
func transport(proto byte, srcPort, dstPort uint16) []byte {
	size := 8
	if proto == protoTCP {
		size = 20
	}
	header := make([]byte, size)
	binary.BigEndian.PutUint16(header[0:], srcPort)
	binary.BigEndian.PutUint16(header[2:], dstPort)
	if proto == protoTCP {
		header[12] = 0x50
	} else {
		binary.BigEndian.PutUint16(header[4:], uint16(size))
	}
	return header
}

// firewallFrame returns an IPv4 or IPv6 frame of a TCP or UDP header between
// the addresses and the ports.
func firewallFrame(proto byte, src, dst string, srcPort, dstPort uint16) []byte {
	srcAddr, dstAddr := netip.MustParseAddr(src), netip.MustParseAddr(dst)
	transportHeader := transport(proto, srcPort, dstPort)
	frame := make([]byte, 14, 14+40+len(transportHeader))
	copy(frame[0:], []byte{0x02, 0, 0, 0, 0, 0xff})
	copy(frame[6:], []byte{0x02, 0, 0, 0, 0, 0xaa})
	if srcAddr.Is4() {
		frame[12], frame[13] = 0x08, 0x00
		networkHeader := make([]byte, 20)
		networkHeader[0] = 0x45
		binary.BigEndian.PutUint16(networkHeader[2:], uint16(20+len(transportHeader)))
		networkHeader[8] = 64
		networkHeader[9] = proto
		copy(networkHeader[12:], srcAddr.AsSlice())
		copy(networkHeader[16:], dstAddr.AsSlice())
		frame = append(frame, networkHeader...)
	} else {
		frame[12], frame[13] = 0x86, 0xdd
		networkHeader := make([]byte, 40)
		networkHeader[0] = 0x60
		binary.BigEndian.PutUint16(networkHeader[4:], uint16(len(transportHeader)))
		networkHeader[6] = proto
		networkHeader[7] = 64
		copy(networkHeader[8:], srcAddr.AsSlice())
		copy(networkHeader[24:], dstAddr.AsSlice())
		frame = append(frame, networkHeader...)
	}
	return append(frame, transportHeader...)
}

// newFirewallBed starts a bed of the devices port0 and port1 whose port0
// filters by the test ruleset before a forwarding instance, so that a packet
// the firewall passes reaches the output.
func newFirewallBed(t *testing.T) *bed {
	t.Helper()

	bed := newBed(t, withDevices("port0", "port1"))
	config, err := cfixture.NewFirewallConfig(bed.agent, "firewall", firewallInput(t))
	require.NoError(t, err)
	bed.freeAtEnd(config)
	bed.serveBeforeForward(&instance{moduleType: "firewall", name: "firewall", config: config})
	return bed
}

// firewallProbe is a flow of a frame and where the test ruleset sends it: to
// the output when the first rule it matches allows, dropped when that rule
// denies or no rule matches.
type firewallProbe struct {
	name     string
	proto    byte
	src, dst string
	dstPort  uint16
	want     fate
}

// frame returns the frame of the flow from the source port.
func (m firewallProbe) frame(srcPort uint16) []byte {
	return firewallFrame(m.proto, m.src, m.dst, srcPort, m.dstPort)
}

// deviceScopedProbes meet a rule of the test ruleset scoped to a device before
// any other; the bed receives them on port0.
var deviceScopedProbes = []firewallProbe{
	{
		name:  "IPv4 TCP to the allowed port, denied on another device only",
		proto: protoTCP, src: "10.1.1.1", dst: "192.168.1.5", dstPort: 80,
		want: fateOutput,
	},
	{
		name:  "IPv4 TCP to the port allowed on its own device",
		proto: protoTCP, src: "10.1.1.1", dst: "192.168.1.5", dstPort: 8080,
		want: fateOutput,
	},
}

// firstMatchProbes meet only the rules of the test ruleset that every device
// shares.
var firstMatchProbes = []firewallProbe{
	{
		name:  "IPv4 TCP to another port meets the broad deny",
		proto: protoTCP, src: "10.1.1.1", dst: "192.168.1.5", dstPort: 81,
		want: fateDrop,
	},
	{
		name:  "IPv4 TCP from outside every source matches no rule",
		proto: protoTCP, src: "11.0.0.1", dst: "192.168.1.5", dstPort: 80,
		want: fateDrop,
	},
	{
		name:  "IPv4 UDP meets the catch-all allow",
		proto: protoUDP, src: "11.0.0.1", dst: "1.2.3.4", dstPort: 9,
		want: fateOutput,
	},
	{
		name:  "IPv6 UDP to the allowed port",
		proto: protoUDP, src: "2001:db8::1", dst: "2001:db8:ffff::1", dstPort: 53,
		want: fateOutput,
	},
	{
		name:  "IPv6 UDP to another port meets the IPv6 deny",
		proto: protoUDP, src: "2001:db8::1", dst: "2001:db8:ffff::1", dstPort: 54,
		want: fateDrop,
	},
	{
		name:  "IPv6 TCP from outside the allowed source meets the IPv6 deny",
		proto: protoTCP, src: "2002::1", dst: "2001:db8::1", dstPort: 53,
		want: fateDrop,
	},
}

// requireProbes runs each probe's frame alone through the bed and requires
// its fate.
func requireProbes(t *testing.T, bed *bed, probes []firewallProbe) {
	for _, tc := range probes {
		t.Run(tc.name, func(t *testing.T) {
			frame := tc.frame(1000)

			result := bed.run(0, frame)

			require.Equal(t, []fate{tc.want}, fatesOf(result, [][]byte{frame}))
		})
	}
}

// Test_Firewall_Packet_FirstMatchingRuleDecides verifies that a packet whose
// first matching rule allows reaches the output, and that one whose first
// matching rule denies, or which matches no rule, is dropped.
func Test_Firewall_Packet_FirstMatchingRuleDecides(t *testing.T) {
	requireProbes(t, newFirewallBed(t), firstMatchProbes)
}

// Test_Firewall_DeviceScopedRule_AppliesOnItsDeviceOnly verifies that a rule
// scoped to a device decides the packets received on that device and is
// skipped for those of another.
func Test_Firewall_DeviceScopedRule_AppliesOnItsDeviceOnly(t *testing.T) {
	requireProbes(t, newFirewallBed(t), deviceScopedProbes)
}

// Test_Firewall_MixedBatch_ClassifiesEachPacketInOrder verifies that one
// round of frames of both families and protocols, more than one
// classification takes, gives each frame the verdict of its own first
// matching rule.
func Test_Firewall_MixedBatch_ClassifiesEachPacketInOrder(t *testing.T) {
	bed := newFirewallBed(t)
	var frames [][]byte
	var want []fate
	// A source port per round keeps every frame distinct.
	for round := range 20 {
		for _, probe := range slices.Concat(deviceScopedProbes, firstMatchProbes) {
			frames = append(frames, probe.frame(uint16(2000+round)))
			want = append(want, probe.want)
		}
	}

	result := bed.run(0, frames...)

	require.Equal(t, want, fatesOf(result, frames))
}

// Test_Firewall_Free_ReturnsMemoryToBaseline verifies that freeing a firewall
// gives back everything its build took, the C classifiers included.
func Test_Firewall_Free_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t, withDevices("port0", "port1"))
	config, err := cfixture.NewFirewallConfig(bed.agent, "firewall", firewallInput(t))
	require.NoError(t, err)

	require.NoError(t, config.Free())

	require.Equal(t, bed.baseline, bed.agent.BlockAllocatorFreeSize())
}

// Test_Firewall_Build_CMemoryRunsOut_ReturnsMemoryToBaseline verifies that a
// build whose memory runs out, in the header, the targets or any compile, join
// or decode of the classifier, gives back all it took, at every capacity.
func Test_Firewall_Build_CMemoryRunsOut_ReturnsMemoryToBaseline(t *testing.T) {
	bed := newBed(t, withDevices("port0", "port1"), func(config *dataplaneut.Config) {
		config.CPMemory = uint64(512 * datasize.MB)
	})
	input := firewallInput(t)
	build := func(agent *ffi.Agent) (*cfixture.ModuleConfig, error) {
		return cfixture.NewFirewallConfig(agent, "firewall", input)
	}

	fits := sweep(t, bed, build, 0, 8<<20, 64<<10)

	require.NotZero(t, fits, "the ruleset fits below 8 MiB")
}

// stressSample returns frames of up to count TCP and UDP rules of the ruleset
// scoped to the first stress device: each from the first address of the
// rule's first source network to that of its first destination network, to
// its first destination port, from a source port of its own.
func stressSample(rules []cacl.ACLRule, count int) [][]byte {
	frames := make([][]byte, 0, count)
	for _, rule := range rules {
		if len(frames) == count {
			break
		}
		if !slices.ContainsFunc(rule.Devices, func(device filter.Device) bool {
			return device.Name == rulegen.StressDevices[0]
		}) {
			continue
		}
		proto := byte(protoTCP)
		if len(rule.ProtoRanges) > 0 {
			proto = byte(rule.ProtoRanges[0].From >> 8)
		}
		if proto != protoTCP && proto != protoUDP {
			continue
		}
		var src, dst netip.Addr
		if len(rule.Src4s) > 0 {
			src, dst = rule.Src4s[0].Network().Addr(), rule.Dst4s[0].Network().Addr()
		} else {
			src, dst = rule.Src6s[0].Network().Addr(), rule.Dst6s[0].Network().Addr()
		}
		dstPort := uint16(80)
		if len(rule.DstPortRanges) > 0 {
			dstPort = rule.DstPortRanges[0].From
		}
		srcPort := uint16(1024 + len(frames))
		frames = append(frames, firewallFrame(proto, src.String(), dst.String(), srcPort, dstPort))
	}
	return frames
}

// serveStress publishes the instance before a forwarding instance on the first
// stress device, and returns what retires it, after which it can be freed.
func (m *bed) serveStress(inst *instance) func() {
	m.t.Helper()

	device := rulegen.StressDevices[0]
	options := fixtureOf(cfixture.ModeForward)
	options.name = "forward"
	options.device = device
	m.publish(route{device: device, input: []*instance{inst, m.fixtureInstance(options)}})
	return func() {
		require.NoError(m.t, m.agent.UpdatePipeline(ffi.PipelineConfig{Name: device + "-in"}))
		require.NoError(m.t, m.agent.DeleteFunction(device+"-in"))
		require.NoError(m.t, m.agent.DeleteModuleConfig(inst.moduleType, inst.name))
	}
}

// verdicts serves the instance on the first stress device, runs the frames
// through it and returns their fates, and then retires it.
func (m *bed) verdicts(inst *instance, frames [][]byte) []fate {
	m.t.Helper()

	fates, _ := m.countedVerdicts(inst, frames, nil)
	return fates
}

// countedVerdicts serves the instance on the first stress device, runs the
// frames through it and returns their fates and, by name, the counters the
// read finds where the instance runs, and then retires it.
//
// A nil read reads none.
func (m *bed) countedVerdicts(
	inst *instance,
	frames [][]byte,
	read func(path dataplaneut.CounterPath) []ffi.CounterInfo,
) ([]fate, map[string][][]uint64) {
	m.t.Helper()

	retire := m.serveStress(inst)
	defer retire()
	fates := fatesOf(m.run(0, frames...), frames)
	counters := map[string][][]uint64{}
	if read != nil {
		for _, counter := range read(inst.path) {
			counters[counter.Name] = counter.Values
		}
	}
	return fates, counters
}

// ruleCounterNames returns the names of the counters of the rules, each once:
// the rule's own, or rule and its index for none, as the C ACL module names
// them.
func ruleCounterNames(rules []cacl.ACLRule) []string {
	var names []string
	for idx, rule := range rules {
		name := rule.Counter
		if name == "" {
			name = fmt.Sprintf("rule %d", idx)
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// withDevices sets the devices of a bed's harness.
func withDevices(names ...string) func(*dataplaneut.Config) {
	return func(config *dataplaneut.Config) {
		config.Devices = names
	}
}

// newStressBed starts a bed of one worker and the stress devices, which also
// knows the C ACL module, with an agent of that memory of its own.
func newStressBed(t testing.TB, cpMemory, agentMemory datasize.ByteSize) *bed {
	t.Helper()

	return newBed(t, withDevices(rulegen.StressDevices...), func(config *dataplaneut.Config) {
		config.CPMemory = uint64(cpMemory)
		config.DPMemory = uint64(64 * datasize.MB)
		config.WorkerCount = 1
		config.Modules = append(config.Modules, "acl")
	}).withAgent("stress", agentMemory)
}

// countedPackets returns the packets the counters hold, the first value of
// each on every worker.
func countedPackets(counters map[string][][]uint64) uint64 {
	var packets uint64
	for _, workers := range counters {
		for _, values := range workers {
			packets += values[0]
		}
	}
	return packets
}

// Test_Firewall_StressRules_VerdictsAndCountersAreTheCACLs verifies that the
// firewall of a production-shaped ruleset, device-scoped rules and counting
// rules included, sends the flows of its rules where the C ACL module of the
// same rules does, and counts the same packets and bytes in every rule
// counter.
func Test_Firewall_StressRules_VerdictsAndCountersAreTheCACLs(t *testing.T) {
	rules := rulegen.GenerateStressRules(1, 300)
	frames := stressSample(rules, 128)
	names := ruleCounterNames(rules)
	bed := newStressBed(t, 1*datasize.GB, 512*datasize.MB)
	acl, err := cacl.NewModuleConfig(bed.agent, "stress", rules, "", "")
	require.NoError(t, err)
	bed.freeAtEnd(acl)
	wantFates, wantCounters := bed.countedVerdicts(
		&instance{moduleType: "acl", name: "stress", config: acl},
		frames,
		func(path dataplaneut.CounterPath) []ffi.CounterInfo {
			counters, err := bed.harness.SharedMemory().DPConfig(0).ModuleRuntimeCounters(
				path.Device,
				path.Pipeline,
				path.Function,
				path.Chain,
				path.ModuleType,
				path.ModuleName,
				names,
			)
			require.NoError(t, err)
			return counters
		},
	)
	var input cfixture.FirewallInput
	for _, rule := range rules {
		require.NoError(t, input.Add(rule))
	}
	config, err := cfixture.NewFirewallConfig(bed.agent, "stress", &input)
	require.NoError(t, err)
	bed.freeAtEnd(config)

	gotFates, gotCounters := bed.countedVerdicts(
		&instance{moduleType: "firewall", name: "stress", config: config},
		frames,
		func(path dataplaneut.CounterPath) []ffi.CounterInfo {
			return bed.harness.SharedMemory().DPConfig(0).ModuleCounters(
				path.Device,
				path.Pipeline,
				path.Function,
				path.Chain,
				path.ModuleType,
				path.ModuleName,
				names,
			)
		},
	)

	require.Equal(t, wantFates, gotFates)
	require.Len(t, wantCounters, len(names), "the C ACL has a counter of every name")
	require.NotZero(t, countedPackets(wantCounters), "the sample meets a counting rule")
	require.Equal(t, wantCounters, gotCounters)
}

// BenchmarkFirewallBuild measures the build of a production-shaped ruleset of
// 17k and 54k rules: the SDK firewall through the api, and the C ACL module's
// config from the same rules.
//
// The last build of each samples the verdicts of a few rules' flows, and the
// firewall's must be the C ACL's, so the SDK side runs after the C side only.
func BenchmarkFirewallBuild(b *testing.B) {
	for _, size := range []struct {
		rules       int
		cpMemory    datasize.ByteSize
		agentMemory datasize.ByteSize
	}{
		{rules: 17_000, cpMemory: 4 * datasize.GB, agentMemory: 3 * datasize.GB},
		{rules: 54_000, cpMemory: 14 * datasize.GB, agentMemory: 12 * datasize.GB},
	} {
		b.Run(fmt.Sprintf("rules=%d", size.rules), func(b *testing.B) {
			rules := rulegen.GenerateStressRules(1, size.rules)
			frames := stressSample(rules, 128)
			bed := newStressBed(b, size.cpMemory, size.agentMemory)
			var want []fate

			// Both sides lay the rules out in their C records before the
			// timer runs.
			b.Run("impl=c", func(b *testing.B) {
				bed := bed.on(b)
				prepared := cacl.NewRules(rules)
				defer prepared.Free()
				bed.benchmarkBuild(b, func() (buildable, error) {
					return cacl.NewModuleConfigOfRules(bed.agent, "stress", prepared, "", "")
				}, func(config buildable) {
					inst := &instance{moduleType: "acl", name: "stress", config: config.(moduleConfig)}
					want = bed.verdicts(inst, frames)
				})
			})
			b.Run("impl=sdk", func(b *testing.B) {
				bed := bed.on(b)
				var input cfixture.FirewallInput
				for _, rule := range rules {
					require.NoError(b, input.Add(rule))
				}
				bed.benchmarkBuild(b, func() (buildable, error) {
					return cfixture.NewFirewallConfig(bed.agent, "stress", &input)
				}, func(config buildable) {
					inst := &instance{moduleType: "firewall", name: "stress", config: config.(moduleConfig)}
					got := bed.verdicts(inst, frames)
					require.NotNil(b, want, "the verdicts compare to those of the impl=c sub-benchmark, which did not run")
					require.Equal(b, want, got)
				})
			})
		})
	}
}

// BenchmarkFirewallRound measures a round of 128 flows of a 17k-rule ruleset
// through the C ACL module and through the SDK firewall, each before a
// forwarding instance, timing the rounds of the harness alone.
//
// Each run builds its configuration untimed and frees it before the next.
func BenchmarkFirewallRound(b *testing.B) {
	rules := rulegen.GenerateStressRules(1, 17_000)
	var packets []gopacket.Packet
	for _, frame := range stressSample(rules, 128) {
		packets = append(packets, gopacket.NewPacket(frame, layers.LayerTypeEthernet, gopacket.Default))
	}
	bed := newStressBed(b, 4*datasize.GB, 3*datasize.GB)

	for _, impl := range []struct {
		name       string
		moduleType string
		build      func(agent *ffi.Agent) (moduleConfig, error)
	}{
		{"c", "acl", func(agent *ffi.Agent) (moduleConfig, error) {
			return cacl.NewModuleConfig(agent, "stress", rules, "", "")
		}},
		{"sdk", "firewall", func(agent *ffi.Agent) (moduleConfig, error) {
			var input cfixture.FirewallInput
			for _, rule := range rules {
				if err := input.Add(rule); err != nil {
					return nil, err
				}
			}
			return cfixture.NewFirewallConfig(agent, "stress", &input)
		}},
	} {
		b.Run("impl="+impl.name, func(b *testing.B) {
			bed := bed.on(b)
			config, err := impl.build(bed.agent)
			require.NoError(b, err)
			defer func() { require.NoError(b, config.Free()) }()
			retire := bed.serveStress(&instance{moduleType: impl.moduleType, name: "stress", config: config})
			defer retire()

			bed.harness.Bench(b, packets)
		})
	}
}
