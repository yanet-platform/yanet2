package acl_test

import (
	"fmt"
	"net"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
)

// benchBatchSizes lists the packet-batch sizes swept by each sub-benchmark.
var benchBatchSizes = []int{1, 2, 4, 8, 16, 32}

// BenchmarkACLAllow measures how per-round overhead amortizes with batch size
// through the ACL module for packets that each match a UDP allow rule.
//
// Sub-benchmarks are named batch=N for each N in benchBatchSizes.
func BenchmarkACLAllow(b *testing.B) {
	rules := []cacl.AclRule{
		allow4Rule(
			filter.IPNets{{
				Addr: netip.MustParseAddr("192.0.2.0"),
				Mask: netip.MustParseAddr("255.255.255.0"),
			}},
			filter.IPNets{filter.UnspecifiedIPv4},
			udpProto,
		),
	}

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

	h, agent, backend := setupACLHarness(b, []string{"port0"})
	applyACLRules(b, backend, "bench", rules)
	wireACLPipeline(b, agent, "port0", "bench")

	for _, n := range benchBatchSizes {
		b.Run(fmt.Sprintf("batch=%d", n), func(b *testing.B) {
			pkts := make([]gopacket.Packet, n)
			for idx := range n {
				pkt, err := xpacket.LayersToPacketChecked(&eth, &ip4, &udp)
				require.NoError(b, err)
				pkts[idx] = pkt
			}
			h.Bench(b, pkts)
		})
	}
}

// BenchmarkACLDeny measures how per-round overhead amortizes with batch size
// through the ACL module for packets that each match a UDP deny rule,
// exercising the drop-recycling path (result.drop non-empty each round).
//
// Sub-benchmarks are named batch=N for each N in benchBatchSizes.
func BenchmarkACLDeny(b *testing.B) {
	rules := []cacl.AclRule{
		deny4Rule(
			filter.IPNets{{
				Addr: netip.MustParseAddr("192.0.2.0"),
				Mask: netip.MustParseAddr("255.255.255.0"),
			}},
			filter.IPNets{filter.UnspecifiedIPv4},
			udpProto,
		),
	}

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

	h, agent, backend := setupACLHarness(b, []string{"port0"})
	applyACLRules(b, backend, "bench", rules)
	wireACLPipeline(b, agent, "port0", "bench")

	for _, n := range benchBatchSizes {
		b.Run(fmt.Sprintf("batch=%d", n), func(b *testing.B) {
			pkts := make([]gopacket.Packet, n)
			for idx := range n {
				pkt, err := xpacket.LayersToPacketChecked(&eth, &ip4, &udp)
				require.NoError(b, err)
				pkts[idx] = pkt
			}
			h.Bench(b, pkts)
		})
	}
}

// benchHostIP maps idx to a distinct IPv4 host address in the 10.0.0.0/8
// range by distributing idx across the three variable octets.
func benchHostIP(idx int) net.IP {
	return net.IP{10, byte((idx >> 16) & 0xff), byte((idx >> 8) & 0xff), byte(idx & 0xff)}
}

// benchDiversePacket builds an Ethernet/IPv4/UDP packet whose SrcIP, DstIP,
// SrcPort, and DstPort are all derived from idx, producing distinct per-packet
// flow tuples across a batch.
//
// The DstIP is taken from a second address sequence offset by a large constant
// so src and dst never coincide. Port values cycle through the ephemeral
// range via modulo arithmetic.
func benchDiversePacket(b *testing.B, idx int) gopacket.Packet {
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
		SrcIP:    benchHostIP(idx),
		DstIP:    benchHostIP(idx + 0x010000),
	}
	udp := layers.UDP{
		SrcPort: layers.UDPPort(1024 + idx%64511),
		DstPort: layers.UDPPort(1024 + (idx*7+13)%64511),
	}
	_ = udp.SetNetworkLayerForChecksum(&ip4)

	pkt, err := xpacket.LayersToPacketChecked(&eth, &ip4, &udp)
	require.NoError(b, err)
	return pkt
}

// setupACLHarnessLarge constructs a harness and agent with enlarged memory
// regions for benchmarks that compile a large number of rules.
//
// The shared package constants (aclCPSize/aclDPSize/aclMemSize) are sized for
// functional tests; 10 000 compiled ACL rules require a significantly larger
// agent arena and CP region.
func setupACLHarnessLarge(b *testing.B, devices []string) (*dataplaneut.Harness, *ffi.Agent, acl.Backend) {
	b.Helper()

	const (
		largeCPSize  = 256 * datasize.MB
		largeDPSize  = 4 * datasize.MB
		largeMemSize = 128 * datasize.MB
	)

	cfg := dataplaneut.Config{
		CPMemory:      uint64(largeCPSize),
		DPMemory:      uint64(largeDPSize),
		WorkerCount:   1,
		Devices:       devices,
		Modules:       []string{"acl"},
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(b, err)
	b.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("acl-bench-large", 0, largeMemSize)
	require.NoError(b, err)
	b.Cleanup(func() { _ = agent.CleanUp() })

	backend := acl.NewBackend(agent, uint64(largeMemSize))
	return h, agent, backend
}

// benchRuleScaleBatch is the fixed batch size used by BenchmarkACLAllowRuleScale.
const benchRuleScaleBatch = 32

// BenchmarkACLAllowRuleScale sweeps the number of compiled ACL rules from 1
// to 10 000 to expose how LPM table depth and value-table breadth affect
// per-packet throughput.
//
// Each sub-benchmark loads N distinct /32 ALLOW rules, each matching a unique
// source host in the 10.0.0.0/8 range, and drives a fixed-size batch of
// benchRuleScaleBatch packets whose target rules are sampled evenly across the
// full installed range [0, N) via stride (packet idx targets rule
// idx*N/benchRuleScaleBatch). This spreads matched prefixes across the whole
// table rather than clustering at the start, while keeping the per-round
// packet count constant as the table grows.
//
// Note: dataplane_ut_run_rounds reuses one packet list across b.N rounds, so
// the working set is bounded by batch size. Only benchRuleScaleBatch distinct
// prefixes are matched per round, sampled evenly across the table, so
// true cross-round LPM cache-miss stress is out of scope here.
func BenchmarkACLAllowRuleScale(b *testing.B) {
	for _, n := range []int{1, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("rules=%d", n), func(b *testing.B) {
			rules := make([]cacl.AclRule, n)
			for idx := range n {
				hostIP := benchHostIP(idx)
				addr, ok := netip.AddrFromSlice(hostIP.To4())
				require.True(b, ok, "benchHostIP must produce a valid IPv4 address")
				mask := netip.MustParseAddr("255.255.255.255")
				rules[idx] = allow4Rule(
					filter.IPNets{{Addr: addr, Mask: mask}},
					filter.IPNets{filter.UnspecifiedIPv4},
					udpProto,
				)
			}

			h, agent, backend := setupACLHarnessLarge(b, []string{"port0"})
			applyACLRules(b, backend, "bench-scale", rules)
			wireACLPipeline(b, agent, "port0", "bench-scale")

			pkts := make([]gopacket.Packet, benchRuleScaleBatch)
			for idx := range benchRuleScaleBatch {
				pkts[idx] = benchDiversePacket(b, idx*n/benchRuleScaleBatch)
			}
			h.Bench(b, pkts)
		})
	}
}

// BenchmarkACLAllowDiverseFlow sweeps the batch size using a single broad
// allow rule (0.0.0.0/0 source) while sending distinct per-packet flow tuples.
//
// Unlike BenchmarkACLAllow, every packet in the batch carries a unique SrcIP,
// DstIP, and port pair, so successive packets resolve distinct IP headers and
// filter entries rather than hitting the same cached results. This isolates
// the per-packet header-resolution cost at varying batch sizes.
//
// Sub-benchmarks are named batch=N for each N in benchBatchSizes.
func BenchmarkACLAllowDiverseFlow(b *testing.B) {
	rules := []cacl.AclRule{
		allow4Rule(
			filter.IPNets{filter.UnspecifiedIPv4},
			filter.IPNets{filter.UnspecifiedIPv4},
			udpProto,
		),
	}

	h, agent, backend := setupACLHarness(b, []string{"port0"})
	applyACLRules(b, backend, "bench-diverse", rules)
	wireACLPipeline(b, agent, "port0", "bench-diverse")

	for _, n := range benchBatchSizes {
		b.Run(fmt.Sprintf("batch=%d", n), func(b *testing.B) {
			pkts := make([]gopacket.Packet, n)
			for idx := range n {
				pkts[idx] = benchDiversePacket(b, idx)
			}
			h.Bench(b, pkts)
		})
	}
}
