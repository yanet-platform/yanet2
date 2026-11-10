package dataplaneut_test

import (
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
)

// The addresses of the frames the rewriting tests run, and where an IPv4
// frame over Ethernet holds them.
var (
	rewritingSource      = net.IPv4(10, 0, 0, 1).To4()
	rewritingDestination = net.IPv4(10, 0, 0, 2).To4()
)

const (
	ipv4SourceOffset      = 14 + 12
	ipv4DestinationOffset = 14 + 16
)

// rewritingCounter names the counter of the forward rule each rewriting
// test installs.
const rewritingCounter = "matched"

// newRewritingHarness returns a one-worker harness whose device port0 runs
// one forward module with the rule, which counts the packets it matches
// and passes them on.
func newRewritingHarness(
	t *testing.T,
	rule cforward.ForwardRule,
) *dataplaneut.Harness {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"forward"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach(
		"rewriting", 0, 16*datasize.MB,
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, agent.CleanUp()) })

	handle, err := forward.NewBackend(agent).Update(
		"bench",
		forward.RuleSet{Compiled: []cforward.ForwardRule{rule}},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Free() })

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: "bench",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: "bench_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "forward", Name: "bench"},
				},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "bench",
		Functions: []string{"bench"},
	}))
	_, err = cplain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:  "port0",
		Input: []ffi.DevicePipelineConfig{{Name: "bench", Weight: 1}},
	}})
	require.NoError(t, err)
	return harness
}

// matchedPackets returns how many packets the rule of newRewritingHarness
// has matched on worker 0.
func matchedPackets(tb testing.TB, harness *dataplaneut.Harness) uint64 {
	tb.Helper()

	counters := harness.SharedMemory().DPConfig(0).ModuleCounters(
		"port0", "bench", "bench", "bench_chain", "forward", "bench",
		[]string{rewritingCounter},
	)
	require.Len(tb, counters, 1)
	return counters[0].Values[0][0]
}

// rewritingPackets returns two ICMP echo requests from the rewriting source
// to the rewriting destination.
func rewritingPackets(t *testing.T) []gopacket.Packet {
	t.Helper()

	packets := make([]gopacket.Packet, 0, 2)
	for seq := range uint16(2) {
		packets = append(packets, xpacket.LayersToPacket(t,
			&layers.Ethernet{
				SrcMAC:       net.HardwareAddr{0, 1, 2, 3, 4, 5},
				DstMAC:       net.HardwareAddr{0, 1, 2, 3, 4, 6},
				EthernetType: layers.EthernetTypeIPv4,
			},
			&layers.IPv4{
				Version:  4,
				TTL:      64,
				Protocol: layers.IPProtocolICMPv4,
				SrcIP:    rewritingSource,
				DstIP:    rewritingDestination,
			},
			&layers.ICMPv4{
				TypeCode: layers.CreateICMPv4TypeCode(
					layers.ICMPv4TypeEchoRequest, 0,
				),
				Seq: seq,
			},
		))
	}
	return packets
}

// Test_BenchRewriting_RewritesBeforeEachRound verifies that the callback
// runs once before each round, with an index equal to the rounds run before
// it.
func Test_BenchRewriting_RewritesBeforeEachRound(t *testing.T) {
	harness := newRewritingHarness(t, cforward.ForwardRule{
		Target:  "port0",
		Mode:    cforward.ModeNone,
		Counter: rewritingCounter,
		Src4s:   []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Dst4s:   []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
	})
	packets := rewritingPackets(t)

	// The rule matches every packet, so its count tells the rounds run. A
	// call is misplaced when its index is not the number of calls before
	// it or not the number of rounds run before it.
	type tally struct{ rounds, calls, misplaced int }
	var got tally
	result := testing.Benchmark(func(b *testing.B) {
		start := matchedPackets(b, harness)
		calls := 0
		harness.BenchRewriting(b, packets, func(round int, frames [][]byte) {
			roundsRun := int(matchedPackets(b, harness)-start) / len(frames)
			if round != calls || round != roundsRun {
				got.misplaced++
			}
			calls++
		})
		got.rounds += b.N
		got.calls += calls
	})
	require.NotZero(t, result.N, "the benchmark failed")
	require.Equal(t, tally{rounds: got.rounds, calls: got.rounds}, got)
}

// Test_BenchRewriting_ModuleReadsRewrittenFrames verifies that the module
// reads every round's frames as the callback rewrote them.
func Test_BenchRewriting_ModuleReadsRewrittenFrames(t *testing.T) {
	harness := newRewritingHarness(t, cforward.ForwardRule{
		Target:  "port0",
		Mode:    cforward.ModeNone,
		Counter: rewritingCounter,
		Src4s: []xnetip.Contiguous[xnetip.Network4]{
			xnetip.MustParseContiguous4(rewritingDestination.String() + "/32"),
		},
		Dst4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
	})
	packets := rewritingPackets(t)

	// The rule matches only the reply direction, which the callback turns
	// every frame into; swapping the addresses keeps the header checksum.
	rounds := 0
	result := testing.Benchmark(func(b *testing.B) {
		harness.BenchRewriting(b, packets, func(_ int, frames [][]byte) {
			for _, frame := range frames {
				copy(frame[ipv4SourceOffset:], rewritingDestination)
				copy(frame[ipv4DestinationOffset:], rewritingSource)
			}
		})
		rounds += b.N
	})
	require.NotZero(t, result.N, "the benchmark failed")
	require.Equal(t, rounds*len(packets), int(matchedPackets(t, harness)))
}
