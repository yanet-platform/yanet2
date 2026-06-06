package dataplaneut

import (
	"fmt"
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// benchBatchSizes lists the packet-batch sizes swept by each sub-benchmark.
var benchBatchSizes = []int{1, 2, 4, 8, 16, 32}

// BenchmarkPipelineRound measures how per-round overhead amortizes with batch
// size through an empty chain (no module processing).
//
// Sub-benchmarks are named batch=N for each N in benchBatchSizes.
func BenchmarkPipelineRound(b *testing.B) {
	cfg := Config{
		CPMemory:      uint64(32 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       nil,
		DevicesToLoad: []string{"plain"},
	}
	h, err := NewHarness(cfg)
	require.NoError(b, err)
	defer h.Free()

	agent, err := h.SharedMemory().AgentAttach("bench", 0, 2*datasize.MB)
	require.NoError(b, err)
	defer func() { _ = agent.CleanUp() }()

	require.NoError(b, agent.UpdateFunction(ffi.FunctionConfig{
		Name: "bench",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    "bench_chain",
				Modules: nil,
			},
		}},
	}))
	require.NoError(b, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "bench",
		Functions: []string{"bench"},
	}))
	require.NoError(b, agent.UpdatePipeline(ffi.PipelineConfig{
		Name: "dummy",
	}))
	require.NoError(b, agent.UpdatePlainDevices([]ffi.DeviceConfig{{
		Name:   "port0",
		Input:  []ffi.DevicePipelineConfig{{Name: "bench", Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}))

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
		DstIP:    net.ParseIP("10.0.0.1"),
	}
	udp := layers.UDP{SrcPort: 12345, DstPort: 80}
	_ = udp.SetNetworkLayerForChecksum(&ip4)

	for _, n := range benchBatchSizes {
		b.Run(fmt.Sprintf("batch=%d", n), func(b *testing.B) {
			pkts := make([]gopacket.Packet, n)
			for idx := range n {
				pkt, err := xpacket.LayersToPacketChecked(&eth, &ip4, &udp)
				require.NoError(b, err)
				pkts[idx] = pkt
			}
			h.Bench(b, pkts...)
		})
	}
}
