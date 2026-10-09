package example_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	"github.com/yanet-platform/yanet2/sdk/example-rs/bindings/go/cexamplers"
)

const (
	exampleCPSize  = 16 * datasize.MB
	exampleDPSize  = 4 * datasize.MB
	exampleMemSize = 2 * datasize.MB
)

// pluginDir is the example module's own meson build directory, resolved
// relative to this test's package directory. The dataplane harness scans
// it for libexample_dp.so and prefers the plugin's entry point over the
// statically linked built-ins.
func pluginDir(t *testing.T) string {
	t.Helper()

	dir := filepath.Join("..", "..", "build")
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf(
			"plugin directory %q is missing; run `make sdk-example` (or meson) in sdk/example-rs first: %v",
			dir, err,
		)
	}
	return dir
}

// setupExampleHarness builds the harness with the example module loaded
// from the plugin directory, attaches a control-plane agent, and publishes
// an example module config into shared memory.
func setupExampleHarness(
	t *testing.T,
	deviceName string,
	configName string,
) (*dataplaneut.Harness, *ffi.Agent) {
	t.Helper()

	cfg := dataplaneut.Config{
		CPMemory:      uint64(exampleCPSize),
		DPMemory:      uint64(exampleDPSize),
		WorkerCount:   1,
		Devices:       []string{deviceName},
		Modules:       []string{"example_rs"},
		DevicesToLoad: []string{"plain"},
		PluginDir:     pluginDir(t),
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("example-test", 0, exampleMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	mod, err := cexamplers.NewModuleConfig(agent, configName)
	require.NoError(t, err)
	t.Cleanup(func() { _ = mod.Free() })

	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{mod.AsFFIModule()}))

	return h, agent
}

// wirePipeline wires a chain[example:configName] -> pipeline -> plain device
// topology so packets injected on deviceName pass through the example module.
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
					{Type: "example_rs", Name: configName},
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
	_, err := plain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:   deviceName,
		Input:  []ffi.DevicePipelineConfig{{Name: configName, Weight: 1}},
		Output: []ffi.DevicePipelineConfig{{Name: "dummy", Weight: 1}},
	}})
	require.NoError(t, err)
}

// TestExampleRs_DropsAllPackets loads the example module's plugin into the
// in-process dataplane and verifies it drops every injected packet, proving
// the out-of-tree plugin path end to end.
func TestExampleRs_DropsAllPackets(t *testing.T) {
	h, agent := setupExampleHarness(t, "port0", "test")
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
	require.Empty(t, result.Output, "example must not forward any packets")
	require.Len(t, result.Drop, packetCount, "example must drop all injected packets")
	for idx := range packetCount {
		require.Equal(t, pkt.Data(), result.Drop[idx].RawData,
			"dropped packet %d must be identical to the injected packet", idx)
	}
}
