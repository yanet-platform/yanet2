//go:build sdk_rust_fixtures

package cfixture_test

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	"github.com/yanet-platform/yanet2/modules/acl/tests/rulegen"
)

// stressFrames selects flows of original rules on the first declared device.
// Header identifiers distinguish every frame without changing its conditions.
func stressFrames(rules []cacl.ACLRule, count int) [][]byte {
	frames := make([][]byte, 0, count)
	for _, rule := range rules {
		if len(frames) == count {
			break
		}
		if !slices.ContainsFunc(rule.Devices, func(d filter.Device) bool { return d.Name == rulegen.StressDevices[0] }) {
			continue
		}
		protocol := byte(6)
		subtype := byte(0)
		if len(rule.ProtoRanges) > 0 {
			protocol = byte(rule.ProtoRanges[0].From >> 8)
			subtype = byte(rule.ProtoRanges[0].From)
		}
		if protocol != 6 && protocol != 17 {
			continue
		}
		var source, destination netip.Addr
		if len(rule.Src4s) > 0 {
			source = rule.Src4s[0].Network().Addr()
			destination = rule.Dst4s[0].Network().Addr()
		} else {
			source = rule.Src6s[0].Network().Addr()
			destination = rule.Dst6s[0].Network().Addr()
		}
		sourcePort := uint16(1024 + len(frames))
		destinationPort := uint16(80)
		if len(rule.SrcPortRanges) > 0 {
			sourcePort = rule.SrcPortRanges[0].From
		}
		if len(rule.DstPortRanges) > 0 {
			destinationPort = rule.DstPortRanges[0].From
		}
		frame := transportFrame(source.Is6(), protocol, subtype, sourcePort, destinationPort)
		if source.Is4() {
			copy(frame[26:30], source.AsSlice())
			copy(frame[30:34], destination.AsSlice())
			binary.BigEndian.PutUint16(frame[18:20], uint16(len(frames)))
		} else {
			copy(frame[22:38], source.AsSlice())
			copy(frame[38:54], destination.AsSlice())
			frame[15] = byte(len(frames) >> 16)
			frame[16] = byte(len(frames) >> 8)
			frame[17] = byte(len(frames))
		}
		frames = append(frames, frame)
	}
	original := len(frames)
	for original > 0 && len(frames) < count {
		frame := slices.Clone(frames[len(frames)%original])
		if frame[12] == 8 {
			binary.BigEndian.PutUint16(frame[18:20], uint16(len(frames)))
		} else {
			frame[15] = byte(len(frames) >> 16)
			frame[16] = byte(len(frames) >> 8)
			frame[17] = byte(len(frames))
		}
		frames = append(frames, frame)
	}
	return frames
}

// stressBed reserves one worker and a separate bounded control-plane agent.
func stressBed(t testing.TB, cpMemory, agentMemory datasize.ByteSize) *bed {
	return newBed(
		t,
		func(c *dataplaneut.Config) {
			c.CPMemory = uint64(cpMemory)
			c.DPMemory = uint64(64 * datasize.MB)
			c.WorkerCount = 1
			c.Devices = rulegen.StressDevices
			c.Modules = append(c.Modules, "acl", "firewall")
		},
	).withAgent("stress", agentMemory)
}

// stressVerdicts snapshots packet fates and all named packet/byte counters,
// then retires the config so the subsequent measured release can reclaim it.
func (m *bed) stressVerdicts(inst *instance, frames [][]byte) ([]fate, map[string][][]uint64) {
	m.t.Helper()
	device := rulegen.StressDevices[0]
	options := fixtureOf(cfixture.ModeForward)
	options.name = "forward"
	options.device = device
	m.publish(route{device: device, input: []*instance{inst, m.fixtureInstance(options)}})
	fates := fatesOf(m.run(0, frames...), frames)
	path := inst.path
	counters, err := m.harness.SharedMemory().DPConfig(0).ModuleRuntimeCounters(path.Device, path.Pipeline, path.Function, path.Chain, path.ModuleType, path.ModuleName, nil)
	require.NoError(m.t, err)
	pairs := map[string][][]uint64{}
	for _, counter := range counters {
		pairs[counter.Name] = counter.Values
	}
	require.NoError(m.t, m.agent.UpdatePipeline(ffi.PipelineConfig{Name: device + "-in"}))
	require.NoError(m.t, m.agent.DeleteFunction(device+"-in"))
	require.NoError(m.t, m.agent.DeleteModuleConfig(inst.moduleType, inst.name))
	return fates, pairs
}

// Test_Firewall_StressRulesMatchC verifies that identical original rule inputs
// produce identical per-packet fates and named packet/byte counter pairs.
//
// Large counts require an explicit environment opt-in; the default stays small.
func Test_Firewall_StressRulesMatchC(t *testing.T) {
	count := 300
	if value := os.Getenv("YANET_CLASSIFY_RULES"); value != "" {
		parsed, err := strconv.Atoi(value)
		require.NoError(t, err)
		require.Positive(t, parsed)
		count = parsed
	}
	cpMemory, agentMemory := datasize.ByteSize(1*datasize.GB), datasize.ByteSize(512*datasize.MB)
	if count > 1000 {
		cpMemory = 4 * datasize.GB
		agentMemory = 3 * datasize.GB
	}
	if count > 17000 {
		cpMemory = 14 * datasize.GB
		agentMemory = 12 * datasize.GB
	}
	rules := rulegen.GenerateStressRules(1, count)
	frames := stressFrames(rules, 128)
	require.Len(t, frames, 128)
	bed := stressBed(t, cpMemory, agentMemory)
	native, err := cacl.NewModuleConfig(bed.agent, "native", rules, "", "")
	require.NoError(t, err)
	want, pairs := bed.stressVerdicts(&instance{moduleType: "acl", name: "native", config: native}, frames)
	require.NoError(t, native.Free())
	require.Contains(t, want, fateOutput)
	require.Contains(t, want, fateDrop)
	passed := 0
	for _, fate := range want {
		if fate == fateOutput {
			passed++
		}
	}
	t.Logf("rules=%d packets=%d pass=%d drop=%d", count, len(frames), passed, len(frames)-passed)
	prepared, err := firewallInput(t, rules)
	require.NoError(t, err)

	rust, err := cfixture.NewFirewallConfig(bed.agent, "rust", prepared)
	require.NoError(t, err)
	got, actualPairs := bed.stressVerdicts(&instance{moduleType: "firewall", name: "rust", config: rust}, frames)
	require.Equal(t, want, got)
	require.Equal(t, pairs, actualPairs)
	require.NoError(t, rust.Free())
}

// Test_Firewall_StressLateFailureRestoresCapacity verifies that a final
// projection error returns the stress graph's entire allocation capacity.
func Test_Firewall_StressLateFailureRestoresCapacity(t *testing.T) {
	value := os.Getenv("YANET_CLASSIFY_RULES")
	if value == "" {
		t.Skip("set YANET_CLASSIFY_RULES to opt into the stress rollback test")
	}
	count, err := strconv.Atoi(value)
	require.NoError(t, err)
	require.Positive(t, count)
	cpMemory, agentMemory := datasize.ByteSize(1*datasize.GB), datasize.ByteSize(512*datasize.MB)
	if count > 1000 {
		cpMemory = 4 * datasize.GB
		agentMemory = 3 * datasize.GB
	}
	if count > 17000 {
		cpMemory = 14 * datasize.GB
		agentMemory = 12 * datasize.GB
	}
	rules := rulegen.GenerateStressRules(1, count)
	prepared, err := firewallInput(t, rules)
	require.NoError(t, err)

	bed := stressBed(t, cpMemory, agentMemory)
	before := bed.agent.BlockAllocatorFreeSize()
	_, err = cfixture.NewFirewallLateFault(bed.agent, "stress-late", prepared)
	require.ErrorContains(t, err, "late firewall projection fault")
	require.Equal(t, before, bed.agent.BlockAllocatorFreeSize())
}

// BenchmarkFirewallBuild measures construction from prepared input records.
//
// Each implementation can run in a fresh process. Packet and counter comparison
// belongs to the separate unmeasured test.
func BenchmarkFirewallBuild(b *testing.B) {
	for _, size := range []struct {
		rules                 int
		cpMemory, agentMemory datasize.ByteSize
	}{{17000, 4 * datasize.GB, 3 * datasize.GB}, {54000, 14 * datasize.GB, 12 * datasize.GB}} {
		b.Run(
			fmt.Sprintf("rules=%d", size.rules),
			func(b *testing.B) {
				b.Run(
					"impl=c",
					func(b *testing.B) {
						rules := rulegen.GenerateStressRules(1, size.rules)
						prepared := cacl.NewRules(rules)
						defer prepared.Free()
						bed := stressBed(b, size.cpMemory, size.agentMemory)
						bed.benchmarkBuild(b, func() (buildable, error) { return cacl.NewModuleConfigOfRules(bed.agent, "stress", prepared, "", "") }, nil)
					},
				)
				b.Run(
					"impl=sdk",
					func(b *testing.B) {
						rules := rulegen.GenerateStressRules(1, size.rules)
						prepared, err := firewallInput(b, rules)
						require.NoError(b, err)

						bed := stressBed(b, size.cpMemory, size.agentMemory)
						bed.benchmarkBuild(b, func() (buildable, error) { return cfixture.NewFirewallConfig(bed.agent, "stress", prepared) }, nil)
					},
				)
			},
		)
	}
}

// BenchmarkFirewallRound measures one native round of 128 distinct flows over
// the same 17k generated rules, with configuration and packets prepared untimed.
func BenchmarkFirewallRound(b *testing.B) {
	for _, implementation := range []string{"c", "sdk"} {
		b.Run(
			"impl="+implementation,
			func(b *testing.B) {
				rules := rulegen.GenerateStressRules(1, 17000)
				frames := stressFrames(rules, 128)
				require.Len(b, frames, 128)
				packets := make([]gopacket.Packet, len(frames))
				flows := map[[2]gopacket.Flow]struct{}{}
				for idx, frame := range frames {
					packets[idx] = gopacket.NewPacket(frame, layers.LayerTypeEthernet, gopacket.Default)
					network := packets[idx].NetworkLayer()
					transport := packets[idx].TransportLayer()
					require.NotNil(b, network)
					require.NotNil(b, transport)
					flows[[2]gopacket.Flow{network.NetworkFlow(), transport.TransportFlow()}] = struct{}{}
				}
				require.Len(b, flows, 128, "round workload must contain distinct flows")
				bed := stressBed(b, 4*datasize.GB, 3*datasize.GB)
				var module *instance
				if implementation == "c" {
					prepared := cacl.NewRules(rules)
					defer prepared.Free()
					config, err := cacl.NewModuleConfigOfRules(bed.agent, "round", prepared, "", "")
					require.NoError(b, err)
					b.Cleanup(func() { _ = config.Free() })
					module = &instance{moduleType: "acl", name: "round", config: config}
				} else {
					input, err := firewallInput(b, rules)
					require.NoError(b, err)
					config, err := cfixture.NewFirewallConfig(bed.agent, "round", input)
					require.NoError(b, err)
					bed.freeAtEnd(config)
					module = &instance{moduleType: "firewall", name: "round", config: config}
				}
				device := rulegen.StressDevices[0]
				options := fixtureOf(cfixture.ModeForward)
				options.name = "forward"
				options.device = device
				bed.publish(route{device: device, input: []*instance{module, bed.fixtureInstance(options)}})
				bed.harness.Bench(b, packets)
			},
		)
	}
}
