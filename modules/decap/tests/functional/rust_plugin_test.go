//go:build yanet_rust_cp

package decap_test

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/decap/bindings/go/cdecap"
	decap "github.com/yanet-platform/yanet2/modules/decap/controlplane"
	decappb "github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
)

// ipipPacket is a VLAN-tagged IPv4-in-IPv4 echo request to outerDst, and
// the same packet with the outer header removed.
func ipipPacket(t *testing.T, outerDst string) (sent, decapsulated []byte) {
	t.Helper()
	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeDot1Q,
	}
	vlan := layers.Dot1Q{VLANIdentifier: 100, Type: layers.EthernetTypeIPv4}
	outer := layers.IPv4{Version: 4, Id: 1, TTL: 64, Protocol: layers.IPProtocolIPv4, SrcIP: net.IPv4zero, DstIP: net.ParseIP(outerDst)}
	inner := layers.IPv4{Version: 4, Id: 1, TTL: 64, Protocol: layers.IPProtocolICMPv4, SrcIP: net.IPv4zero, DstIP: net.ParseIP("1.1.0.9")}
	icmp := layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)}
	return xpacket.LayersToPacket(t, &eth, &vlan, &outer, &inner, &icmp).Data(),
		xpacket.LayersToPacket(t, &eth, &vlan, &inner, &icmp).Data()
}

// Test_RustPlugin_DrivenThroughDecapService verifies the control path the
// decap CLI uses, end to end: an UpdateConfig request over gRPC reaches the
// decap service, the Rust api builds and validates the configuration, and
// the Rust dataplane module decapsulates matching packets.
func Test_RustPlugin_DrivenThroughDecapService(t *testing.T) {
	h, agent, backend := setupDecapHarness(t)

	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	decappb.RegisterDecapServiceServer(server, decap.NewDecapService(backend))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient(
		"passthrough:///decap",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := decappb.NewDecapServiceClient(conn)

	prefixes4, err := commonpb.NewIPv4PrefixesFromPrefixes([]netip.Prefix{netip.MustParsePrefix("4.5.6.7/32")})
	require.NoError(t, err)
	prefixes6, err := commonpb.NewIPv6PrefixesFromPrefixes([]netip.Prefix{netip.MustParsePrefix("1:2:3:4::abcd/128")})
	require.NoError(t, err)
	_, err = client.UpdateConfig(t.Context(), &decappb.UpdateConfigRequest{
		Name:      "decap_lab",
		Prefixes4: prefixes4,
		Prefixes6: prefixes6,
	})
	require.NoError(t, err)
	wireDecapPipeline(t, agent, "decap_lab")

	shown, err := client.ShowConfig(t.Context(), &decappb.ShowConfigRequest{Name: "decap_lab"})
	require.NoError(t, err)
	require.Len(t, shown.GetPrefixes4(), 1)

	sent, expected := ipipPacket(t, "4.5.6.7")
	result, err := h.HandlePackets(xpacket.ParseEtherPacket(sent))
	require.NoError(t, err)
	require.Len(t, result.Output, 1)
	require.Equal(t, expected, result.Output[0].RawData)

	missed, _ := ipipPacket(t, "4.5.6.8")
	result, err = h.HandlePackets(xpacket.ParseEtherPacket(missed))
	require.NoError(t, err)
	require.Len(t, result.Output, 1)
	require.Equal(t, missed, result.Output[0].RawData)
}

// Test_RustPlugin_RefusesConfigFromCApi verifies that the C api cannot
// create a configuration for the Rust module: the module declares a
// non-zero configuration layout and the C module init, which names layout
// zero, refuses with a layout mismatch before anything reaches the
// dataplane.
func Test_RustPlugin_RefusesConfigFromCApi(t *testing.T) {
	_, agent, _ := setupDecapHarness(t)

	mod, err := cdecap.NewModuleConfig(agent, "decap_c")
	require.Nil(t, mod)
	require.ErrorContains(t, err, "module 'decap' config layout mismatch: control plane 0x0000000000000000, dataplane 0x")
}

// Test_BuiltinModule_RefusesConfigFromRustApi verifies the converse: with
// the built-in C module loaded (layout zero), the Rust api's layout is
// refused, so the C module never reads a configuration of the Rust layout.
func Test_BuiltinModule_RefusesConfigFromRustApi(t *testing.T) {
	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(decapCPSize),
		DPMemory:      uint64(decapDPSize),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		Modules:       []string{"decap"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)
	agent, err := h.SharedMemory().AgentAttach("decap-builtin", 0, decapMemSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	handle, err := decap.NewBackend(agent).UpdateModule("decap0", []netip.Prefix{netip.MustParsePrefix("4.5.6.7/32")})
	require.Nil(t, handle)
	require.ErrorContains(t, err, "config layout mismatch: control plane 0x")
	require.ErrorContains(t, err, "dataplane 0x0000000000000000")
}
