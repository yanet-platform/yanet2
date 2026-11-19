//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"net"
	"testing"
	"unsafe"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

// icmpPackets returns count echo requests through port0.
func icmpPackets(t *testing.T, count int) []gopacket.Packet {
	t.Helper()
	packets := make([]gopacket.Packet, 0, count)
	for idx := range count {
		eth := layers.Ethernet{
			SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
			DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
			EthernetType: layers.EthernetTypeIPv4,
		}
		ip := layers.IPv4{
			Version: 4, TTL: 64, Protocol: layers.IPProtocolICMPv4,
			SrcIP: net.IPv4(10, 0, 0, byte(idx+1)), DstIP: net.IPv4(192, 168, 1, 1),
		}
		icmp := layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}
		packets = append(packets, xpacket.LayersToPacket(t, &eth, &ip, &icmp))
	}
	return packets
}

// moduleDrops returns the drop count of blackhole "a" in the wired chain.
func moduleDrops(t *testing.T, memory *publishedFixture) uint64 {
	t.Helper()
	counters := memory.Harness.SharedMemory().DPConfig(0).ModuleCounters(
		"port0", "drop", "drop", "drop_chain", "blackhole", "a", []string{"drop"},
	)
	require.Len(t, counters, 1)
	total := uint64(0)
	for _, values := range counters[0].Values {
		total += values[0]
	}
	return total
}

// Test_UpdateModules_DataplaneRunsGoGeneration verifies that a module
// configuration Go publishes is run by the dataplane on execution
// contexts Go built: the workers take the Go generation, its packets are
// dropped by the module, and the module's counters continue from the
// generation C published.
func Test_UpdateModules_DataplaneRunsGoGeneration(t *testing.T) {
	memory := newPublishedFixture(t)
	memory.RunAssigner(t)
	instance := memory.Instance()

	result, err := memory.Harness.HandlePackets(icmpPackets(t, 3)...)
	require.NoError(t, err)
	require.Len(t, result.Drop, 3, "the C generation drops through blackhole a")
	before := moduleDrops(t, memory)
	cGeneration := instance.Cp.CpConfigGen.Get().Gen

	module, err := cpconfig.NewBlackhole(memory.owner, "a")
	require.NoError(t, err)
	require.NoError(t, cpconfig.UpdateModules(instance, []*layout.CpModule{module}))

	published := instance.Cp.CpConfigGen.Get()
	require.Equal(t, cGeneration+1, published.Gen)
	roots := unsafe.Slice(published.ConfigGenEctxs.Get(), published.ConfigGenEctxCount)
	workers := unsafe.Slice(instance.Dp.Workers.Get(), instance.Dp.WorkerCount)
	for idx := range workers {
		require.Same(t, roots[idx].Get(), workers[idx].Get().ConfigGenEctx.Get(),
			"worker %d runs the contexts Go built", idx)
	}
	var listed bool
	for _, config := range memory.Harness.SharedMemory().DPConfig(0).CPConfigs() {
		if config.Type == "blackhole" && config.Name == "a" {
			listed = true
		}
	}
	require.True(t, listed, "the cgo control plane lists the Go configuration")

	result, err = memory.Harness.HandlePackets(icmpPackets(t, 5)...)
	require.NoError(t, err)
	require.Len(t, result.Drop, 5, "the Go generation drops through blackhole a")
	require.Equal(t, before+5, moduleDrops(t, memory), "module counters continue across the Go publish")
}

// Test_UpdateModules_MatchesC verifies that a whole module update from
// Go, generation copy, upsert, every worker's contexts, publication and
// the teardown of the replaced generation included, leaves the storage as
// cp_config_update_modules does.
func Test_UpdateModules_MatchesC(t *testing.T) {
	for _, name := range []string{"fresh", "a"} {
		t.Run("module "+name, func(t *testing.T) {
			memory := newPublishedFixture(t)
			instance := memory.Instance()
			memory.CompareWithC(t,
				func() {
					config, err := cblackhole.NewModuleConfig(memory.cAgent, name)
					require.NoError(t, err)
					require.NoError(t, memory.cAgent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
				},
				func() {
					module, err := cpconfig.NewBlackhole(memory.owner, name)
					require.NoError(t, err)
					require.NoError(t, cpconfig.UpdateModules(instance, []*layout.CpModule{module},
						cpconfig.WithDelivery(nativetest.Deliver),
					))
				},
			)
		})
	}
}

// Benchmark_UpdateModules_Blackhole measures building one blackhole
// configuration and publishing it until every worker runs it, through
// the cgo control plane and through the native one, both delivering the
// workers' contexts on the publishing thread as the C publish does for an
// in-process dataplane; the native publish is also measured with the
// contexts delivered by an assigner thread, as a real dataplane's are.
func Benchmark_UpdateModules_Blackhole(b *testing.B) {
	b.Run("cgo", func(b *testing.B) {
		memory := newPublishedFixture(b)
		for b.Loop() {
			config, err := cblackhole.NewModuleConfig(memory.cAgent, "a")
			require.NoError(b, err)
			require.NoError(b, memory.cAgent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
		}
	})
	b.Run("native", func(b *testing.B) {
		memory := newPublishedFixture(b)
		for b.Loop() {
			module, err := cpconfig.NewBlackhole(memory.owner, "a")
			require.NoError(b, err)
			require.NoError(b, cpconfig.UpdateModules(memory.Instance(), []*layout.CpModule{module},
				cpconfig.WithDelivery(nativetest.Deliver),
			))
		}
	})
	b.Run("native with an assigner thread", func(b *testing.B) {
		memory := newPublishedFixture(b)
		memory.RunAssigner(b)
		for b.Loop() {
			module, err := cpconfig.NewBlackhole(memory.owner, "a")
			require.NoError(b, err)
			require.NoError(b, cpconfig.UpdateModules(memory.Instance(), []*layout.CpModule{module}))
		}
	})
}
