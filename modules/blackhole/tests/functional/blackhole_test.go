package blackhole_test

import (
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

const (
	blackholeCPSize  = 16 * datasize.MB
	blackholeDPSize  = 4 * datasize.MB
	blackholeMemSize = 2 * datasize.MB
)

// setupBlackholeHarness builds the harness, attaches a control-plane agent,
// and publishes a blackhole module config into shared memory.
//
// Returns the harness, the agent, and the module config handle. Cleanup is
// wired via t.Cleanup in LIFO order. Pipeline wiring must follow after
// calling this function.
func setupBlackholeHarness(
	t *testing.T,
	deviceName string,
	configName string,
) (*dataplaneut.Harness, *ffi.Agent, *cblackhole.ModuleConfig) {
	t.Helper()

	cfg := dataplaneut.Config{
		CPMemory:      uint64(blackholeCPSize),
		DPMemory:      uint64(blackholeDPSize),
		WorkerCount:   1,
		Devices:       []string{deviceName},
		Modules:       []string{"blackhole"},
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("bh-test", 0, blackholeMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	mod, err := cblackhole.NewModuleConfig(agent, configName)
	require.NoError(t, err)
	t.Cleanup(mod.Free)

	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{mod.AsFFIModule()}))

	return h, agent, mod
}

// wirePipeline wires a chain[blackhole:configName] -> pipeline -> plain device
// topology so packets injected on deviceName pass through the blackhole module.
func wirePipeline(
	t *testing.T,
	agent *ffi.Agent,
	deviceName, configName string,
) {
	t.Helper()

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: configName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: configName + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "blackhole", Name: configName},
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
		Name:   deviceName,
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}}))
}

// TestBlackhole_DropsAllPackets verifies that the blackhole module drops every
// injected packet regardless of its content.
func TestBlackhole_DropsAllPackets(t *testing.T) {
	h, agent, _ := setupBlackholeHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP("10.0.0.1"),
		DstIP:    net.ParseIP("192.168.1.1"),
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}

	packetCount := 3
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	packets := make([]gopacket.Packet, 0, packetCount)
	for range packetCount {
		packets = append(packets, pkt)
	}

	result, err := h.HandlePackets(packets...)
	require.NoError(t, err)
	require.Empty(t, result.Output, "blackhole must not forward any packets")
	require.Len(t, result.Drop, packetCount, "blackhole must drop all injected packets")
	for idx := range packetCount {
		require.Equal(t, pkt.Data(), result.Drop[idx].RawData,
			"dropped packet %d must be identical to the injected packet", idx)
	}
}
