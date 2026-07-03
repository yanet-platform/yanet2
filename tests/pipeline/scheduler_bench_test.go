// Package pipeline_test benchmarks the dataplane worker's per-round device
// scheduler in isolation.
//
// The code under test is the per-round loop that distributes packets to
// per-device fronts and drives each device's input and output pipelines. The
// forward module appears here only as a minimal traffic source: a single
// redirect rule moves every packet to one egress device, so raising the
// registered-device count adds scheduler bookkeeping without adding packet
// work. That isolates the scheduling cost from module processing.
package pipeline_test

import (
	"fmt"
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
)

// Memory sizes for the scheduler benchmark harness.
const (
	benchCPSize  = 256 * datasize.MB
	benchDPSize  = 16 * datasize.MB
	benchMemSize = 128 * datasize.MB
)

// schedDeviceCounts lists the registered-device counts swept by the benchmark.
var schedDeviceCounts = []int{4, 16, 64}

// schedBatchSizes lists the packet-batch sizes swept by the benchmark.
var schedBatchSizes = []int{1, 8, 32}

// benchPorts returns n logical port names port0..portN-1.
func benchPorts(n int) []string {
	ports := make([]string, n)
	for idx := range n {
		ports[idx] = fmt.Sprintf("port%d", idx)
	}
	return ports
}

// benchPacket builds an Ethernet/IPv4/UDP packet whose destination falls in
// 10.0.0.0/8 so the redirect rule matches it.
func benchPacket(b *testing.B) gopacket.Packet {
	b.Helper()

	eth := layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		DstMAC:       net.HardwareAddr{0x11, 0x22, 0x33, 0x44, 0x55, 0x66},
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    net.ParseIP("192.0.2.1"),
		DstIP:    net.ParseIP("10.0.0.5"),
	}
	udp := layers.UDP{SrcPort: 12345, DstPort: 80}
	_ = udp.SetNetworkLayerForChecksum(&ip4)

	pkt, err := xpacket.LayersToPacketChecked(&eth, &ip4, &udp)
	require.NoError(b, err)
	return pkt
}

func setupHarness(
	b *testing.B,
	devices []string,
) (*dataplaneut.Harness, *ffi.Agent, forward.Backend) {
	b.Helper()

	cfg := dataplaneut.Config{
		CPMemory:      uint64(benchCPSize),
		DPMemory:      uint64(benchDPSize),
		WorkerCount:   1,
		Devices:       devices,
		Modules:       []string{"forward"},
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(b, err)
	b.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("sched-bench", 0, benchMemSize)
	require.NoError(b, err)
	b.Cleanup(func() { _ = agent.CleanUp() })

	backend := forward.NewBackend(agent)
	return h, agent, backend
}

// wireRedirect installs a redirect rule on the primary ingress device that
// sends every matching packet to the egress pipeline of the first extra device,
// and wires pass-through pipelines for the remaining devices.
//
// The result is one device front carrying traffic each round while the rest
// only inflate the device-registry capacity the scheduler walks. extra must be
// non-empty: its first element is the egress target.
func wireRedirect(
	b *testing.B,
	agent *ffi.Agent,
	backend forward.Backend,
	primary string,
	extra []string,
) {
	b.Helper()

	const name = "bench"

	rule := cforward.ForwardRule{
		Target:  extra[0],
		Mode:    cforward.ModeOut,
		Counter: name,
		Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
		Dst4s:   filter.IPNets{filter.MustParseIPNet("10.0.0.0/8")},
	}
	handle, err := backend.UpdateModule(name, []cforward.ForwardRule{rule})
	require.NoError(b, err)
	b.Cleanup(handle.Free)

	require.NoError(b, agent.UpdateFunction(ffi.FunctionConfig{
		Name: name,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    name + "_chain",
				Modules: []ffi.ChainModuleConfig{{Type: "forward", Name: name}},
			},
		}},
	}))
	require.NoError(b, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      name,
		Functions: []string{name},
	}))

	// A pipeline with no functions passes packets straight through.
	require.NoError(b, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy"}))

	for _, dev := range extra {
		require.NoError(b, agent.UpdatePipeline(ffi.PipelineConfig{
			Name: "dummy_out_" + dev,
		}))
		require.NoError(b, agent.UpdatePipeline(ffi.PipelineConfig{
			Name: "dummy_in_" + dev,
		}))
	}

	devices := []ffi.DeviceConfig{{
		Name:   primary,
		Input:  []ffi.DevicePipelineConfig{{Name: name, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}
	for _, dev := range extra {
		devices = append(devices, ffi.DeviceConfig{
			Name:   dev,
			Input:  []ffi.DevicePipelineConfig{{Name: "dummy_in_" + dev, Weight: 1}},
			Output: []ffi.DevicePipelineConfig{{Name: "dummy_out_" + dev, Weight: 1}},
		})
	}
	require.NoError(b, agent.UpdatePlainDevices(devices))
}

func buildBench(
	b *testing.B,
	deviceCount, batch int,
) (*dataplaneut.Harness, []gopacket.Packet) {
	b.Helper()

	ports := benchPorts(deviceCount)
	h, agent, backend := setupHarness(b, ports)
	wireRedirect(b, agent, backend, ports[0], ports[1:])

	pkts := make([]gopacket.Packet, batch)
	for idx := range batch {
		pkts[idx] = benchPacket(b)
	}

	result, err := h.HandlePackets(pkts...)
	require.NoError(b, err)
	require.Len(b, result.Output, batch, "all packets must egress")
	require.Empty(b, result.Drop, "no packet must be dropped")

	return h, pkts
}

// BenchmarkScheduler measures single-worker throughput through the per-round
// device scheduler as the registered-device count and packet-batch size vary.
//
// A higher device count adds scheduling work without adding packet work; a
// larger batch amortizes the fixed per-round cost over more packets.
//
// Sub-benchmarks are named devices=D/batch=N.
func BenchmarkScheduler(b *testing.B) {
	for _, devices := range schedDeviceCounts {
		for _, batch := range schedBatchSizes {
			b.Run(fmt.Sprintf("devices=%d/batch=%d", devices, batch), func(b *testing.B) {
				h, pkts := buildBench(b, devices, batch)
				h.Bench(b, pkts)
			})
		}
	}
}
