package l3b_test

import (
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	"github.com/yanet-platform/yanet2/modules/l3b/bindings/go/cl3b"
)

const (
	l3bCPSize  = 64 * datasize.MB
	l3bDPSize  = 4 * datasize.MB
	l3bMemSize = 16 * datasize.MB
)

// setupL3bHarness builds the harness with the l3b and forward modules loaded,
// attaches a control-plane agent, and publishes an empty l3b module config.
//
// The forward module is loaded so a catch-all sink can route pass-through
// packets to egress. Returns the harness and the agent. Cleanup is wired via
// t.Cleanup in LIFO order. Pipeline wiring must follow after calling this
// function.
func setupL3bHarness(
	t *testing.T,
	deviceName string,
	configName string,
) (*dataplaneut.Harness, *ffi.Agent) {
	t.Helper()

	cfg := dataplaneut.Config{
		CPMemory:      uint64(l3bCPSize),
		DPMemory:      uint64(l3bDPSize),
		WorkerCount:   1,
		Devices:       []string{deviceName},
		Modules:       []string{"l3b", "forward"},
		DevicesToLoad: []string{"plain"},
	}
	h, err := dataplaneut.NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("l3b-test", 0, l3bMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	mod, err := cl3b.NewModuleConfig(agent, configName)
	require.NoError(t, err)
	t.Cleanup(mod.Free)

	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{mod.AsFFIModule()}))

	return h, agent
}

// catchAllForwardRules returns forward rules with ModeOut that route every
// packet to the given device's output stage.
func catchAllForwardRules(device string) []cforward.ForwardRule {
	return []cforward.ForwardRule{
		{
			Target:  device,
			Mode:    cforward.ModeOut,
			Counter: "sink4",
			Src4s:   filter.IPNets{filter.UnspecifiedIPv4},
			Dst4s:   filter.IPNets{filter.UnspecifiedIPv4},
		},
		{
			Target:  device,
			Mode:    cforward.ModeOut,
			Counter: "sink6",
			Src6s:   filter.IPNets{filter.UnspecifiedIPv6},
			Dst6s:   filter.IPNets{filter.UnspecifiedIPv6},
		},
	}
}

// wirePipeline wires a chain[l3b:configName -> forward:sink] -> pipeline ->
// plain device topology. The forward sink routes pass-through packets to the
// device output; packets dropped by l3b never reach it.
func wirePipeline(
	t *testing.T,
	agent *ffi.Agent,
	deviceName, configName string,
) {
	t.Helper()

	sinkName := configName + "-sink"
	sinkBackend := forward.NewBackend(agent)
	sinkHandle, err := sinkBackend.UpdateModule(sinkName, catchAllForwardRules(deviceName))
	require.NoError(t, err)
	t.Cleanup(sinkHandle.Free)

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: configName,
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name: configName + "_chain",
				Modules: []ffi.ChainModuleConfig{
					{Type: "l3b", Name: configName},
					{Type: "forward", Name: sinkName},
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

// TestL3b_ForwardsNonTcpUdpPackets verifies that packets which are neither
// TCP nor UDP (here ICMPv4) pass through the module unchanged: they are not
// eligible for load balancing and reach the output via the sink.
func TestL3b_ForwardsNonTcpUdpPackets(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
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
	require.Empty(t, result.Drop, "non-TCP/UDP packets must not be dropped")
	require.Len(t, result.Output, packetCount, "non-TCP/UDP packets must be forwarded")
}

// TestL3b_DropsTcpWithoutVirtualService verifies that TCP packets are dropped
// when no virtual service is configured: with no matching virtual service the
// scheduler has nowhere to send the packet.
func TestL3b_DropsTcpWithoutVirtualService(t *testing.T) {
	h, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.ParseIP("10.0.0.1"),
		DstIP:    net.ParseIP("192.168.1.1"),
	}
	tcp := layers.TCP{
		SrcPort: 12345,
		DstPort: 80,
		Seq:     1,
		Window:  1024,
	}
	tcp.SetNetworkLayerForChecksum(&ip4)

	packetCount := 3
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &tcp)
	packets := make([]gopacket.Packet, 0, packetCount)
	for range packetCount {
		packets = append(packets, pkt)
	}

	result, err := h.HandlePackets(packets...)
	require.NoError(t, err)
	require.Empty(t, result.Output, "TCP packets must not be forwarded without a virtual service")
	require.Len(t, result.Drop, packetCount, "TCP packets must be dropped without a virtual service")
}
