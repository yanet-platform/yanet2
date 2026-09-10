package operator_test

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	routepb "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// pipelineGateway records dataplane requests without programming the host.
type pipelineGateway struct {
	routepb.UnimplementedRouteServiceServer
	ynpb.UnimplementedFunctionServiceServer
	ynpb.UnimplementedGatewayServer
	mu     sync.Mutex
	latest *routepb.UpdateFIBRequest
}

// Test_Operator_NetplanNamespaceRestart verifies that file replacement cannot
// alter a running instance, but a new process in a new namespace consumes it.
func Test_Operator_NetplanNamespaceRestart(t *testing.T) {
	if os.Getenv("YANET_NETNS_TESTS") != "1" {
		t.Skip("requires disposable namespaces and YANET_NETNS_TESTS=1")
	}
	plan := func(address string) []byte {
		return fmt.Appendf(nil, "network: {version: 2, ethernets: {lo: {addresses: [%q]}}, dummy-devices: {dummy0: {addresses: [%q]}}}", address, address)
	}
	const original = "192.0.2.10/32"
	const replacement = "192.0.2.20/32"
	expected := os.Getenv("YANET_NETPLAN_RESTART_ADDRESS")
	if expected == "" {
		path := filepath.Join(t.TempDir(), "netplan.yaml")
		require.NoError(t, os.WriteFile(path, plan(original), 0o600))
		binary, err := os.Executable()
		require.NoError(t, err)
		for _, address := range []string{original, replacement} {
			command := exec.CommandContext(t.Context(), binary, "-test.run", "^Test_Operator_NetplanNamespaceRestart$", "-test.timeout", "15s")
			command.Env = append(os.Environ(), "YANET_NETPLAN_RESTART_ADDRESS="+address, "YANET_NETPLAN_RESTART_PATH="+path)
			command.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNET}
			output, err := command.CombinedOutput()
			require.NoError(t, err, "%s", output)
		}
		return
	}
	_, err := vnetlink.LinkByName("dummy0")
	require.Error(t, err, "each child must start in a fresh namespace")
	loopback, err := vnetlink.LinkByName("lo")
	require.NoError(t, err)
	require.NoError(t, vnetlink.LinkSetUp(loopback))
	path := os.Getenv("YANET_NETPLAN_RESTART_PATH")
	config := sidecaroperator.DefaultConfig()
	config.NetplanPath = xcfg.MustNonEmptyString(path)
	config.Gateways = []commonoperator.GatewayConfig{{Name: "unavailable", Endpoint: xcfg.MustNonEmptyString("127.0.0.1:9")}}
	config.NeighbourPublishTimeout = 50 * time.Millisecond
	config.Reconcile.Interval = xcfg.MustNonZero(20 * time.Millisecond)
	config.Reconcile.InitialBackoff = xcfg.MustNonZero(10 * time.Millisecond)
	config.Reconcile.MaxBackoff = xcfg.MustNonZero(20 * time.Millisecond)
	sidecar, err := sidecaroperator.NewOperator(config)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- sidecar.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-stopped
		require.NoError(t, sidecar.Close())
	})
	contains := func(name, address string) bool {
		link, err := vnetlink.LinkByName(name)
		if err != nil {
			return false
		}
		addresses, err := vnetlink.AddrList(link, vnetlink.FAMILY_V4)
		if err != nil {
			return false
		}
		for _, observed := range addresses {
			if observed.IPNet.String() == address {
				return true
			}
		}
		return false
	}
	require.Eventually(t, func() bool {
		return contains("lo", expected) && contains("dummy0", expected)
	}, 5*time.Second, 20*time.Millisecond)
	if expected == original {
		require.NoError(t, os.WriteFile(path, plan(replacement), 0o600))
		address, err := vnetlink.ParseAddr(original)
		require.NoError(t, err)
		for _, name := range []string{"lo", "dummy0"} {
			link, err := vnetlink.LinkByName(name)
			require.NoError(t, err)
			require.NoError(t, vnetlink.AddrDel(link, address))
		}
		require.Eventually(t, func() bool {
			return contains("lo", original) && contains("dummy0", original)
		}, 5*time.Second, 20*time.Millisecond)
		require.Never(t, func() bool {
			return contains("lo", replacement) || contains("dummy0", replacement)
		}, 200*time.Millisecond, 20*time.Millisecond)
	} else {
		require.False(t, contains("lo", original))
		require.False(t, contains("dummy0", original))
	}
}

func (m *pipelineGateway) UpdateFIB(ctx context.Context, request *routepb.UpdateFIBRequest) (*routepb.UpdateFIBResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latest = request
	return &routepb.UpdateFIBResponse{}, nil
}

// Snapshot captures the last immutable dataplane request.
func (m *pipelineGateway) Snapshot() *routepb.UpdateFIBRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.latest
}

func (m *pipelineGateway) Get(ctx context.Context, request *ynpb.GetFunctionRequest) (*ynpb.GetFunctionResponse, error) {
	return nil, status.Error(codes.NotFound, "no prior function")
}
func (m *pipelineGateway) Update(ctx context.Context, request *ynpb.UpdateFunctionRequest) (*ynpb.UpdateFunctionResponse, error) {
	return &ynpb.UpdateFunctionResponse{}, nil
}
func (m *pipelineGateway) Register(ctx context.Context, request *ynpb.RegisterRequest) (*ynpb.RegisterResponse, error) {
	return &ynpb.RegisterResponse{}, nil
}

// Test_Operator_NeighbourPipeline verifies that real kernel observations drive
// scoped FIB entries through the sidecar and a route-operator subprocess.
//
// This opt-in test must run in a disposable network namespace. FeedRIB input is
// synthetic here; actual BIRD exporter provenance is a separate deployment gate.
func Test_Operator_NeighbourPipeline(t *testing.T) {
	if os.Getenv("YANET_NETNS_TESTS") != "1" {
		t.Skip("requires disposable network namespace and YANET_NETNS_TESTS=1")
	}
	binary := os.Getenv("YANET_ROUTE_OPERATOR_BINARY")
	require.NotEmpty(t, binary, "provide the built route-operator subprocess")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	gateway := &pipelineGateway{}
	server := grpc.NewServer()
	routepb.RegisterRouteServiceServer(server, gateway)
	ynpb.RegisterFunctionServiceServer(server, gateway)
	ynpb.RegisterGatewayServer(server, gateway)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	endpoint := reserved.Addr().String()
	require.NoError(t, reserved.Close())
	configPath := filepath.Join(t.TempDir(), "route.yaml")
	config := fmt.Sprintf(`logging: {level: error}
server: {endpoint: %q}
gateways: [{name: recording, endpoint: %q}]
gateway_devices: {recording: [logical0, logical1, vlan0]}
netlink_monitor: {disabled: true}
readiness: {expect_bird: false, remote_neighbour_table: netlink-dataplane-default, remote_neighbour_max_age: 1s}
reconcile: {interval: 20ms, initial_backoff: 10ms, max_backoff: 20ms}
`, endpoint, listener.Addr().String())
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))
	processContext, stopProcess := context.WithCancel(ctx)
	process := exec.CommandContext(processContext, binary, "-c", configPath)
	process.Stderr = os.Stderr
	require.NoError(t, process.Start())
	t.Cleanup(func() { stopProcess(); _ = process.Wait() })
	connection, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	routes := operatorpb.NewRouteServiceClient(connection)
	require.Eventually(t, func() bool {
		_, err := routes.ListConfigs(ctx, &operatorpb.ListConfigsRequest{})
		return err == nil
	}, 5*time.Second, 20*time.Millisecond)
	require.Nil(t, gateway.Snapshot(), "cold remote input must preserve prior dataplane state")
	path := filepath.Join(t.TempDir(), "netplan.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`network:
  version: 2
  ethernets:
    kni0: {mtu: 1500, link-local: [], addresses: ["fe80::f1/64"]}
    kni1: {mtu: 1500, link-local: [], addresses: ["fe80::1f1/64"]}
    lo: {addresses: ["192.0.2.10/32"]}
  dummy-devices:
    dummy0: {addresses: ["192.0.2.11/32"]}
  vlans:
    vlan0: {id: 100, link: kni0, link-local: []}
`), 0o600))
	sidecarConfig := sidecaroperator.DefaultConfig()
	sidecarConfig.NetplanPath = xcfg.MustNonEmptyString(path)
	sidecarConfig.Gateways = []commonoperator.GatewayConfig{
		{Name: "unavailable", Endpoint: xcfg.MustNonEmptyString("127.0.0.1:9")},
		{Name: "direct", Endpoint: xcfg.MustNonEmptyString(endpoint)},
	}
	sidecarConfig.NeighbourPublishTimeout = 200 * time.Millisecond
	sidecarConfig.LinkMap = map[string]string{"kni0": "logical0", "kni1": "logical1"}
	sidecarConfig.Reconcile.Interval = xcfg.MustNonZero(20 * time.Millisecond)
	sidecarConfig.Reconcile.InitialBackoff = xcfg.MustNonZero(10 * time.Millisecond)
	sidecarConfig.Reconcile.MaxBackoff = xcfg.MustNonZero(20 * time.Millisecond)
	sidecar, err := sidecaroperator.NewOperator(sidecarConfig, sidecaroperator.WithLog(zaptest.NewLogger(t)))
	require.NoError(t, err)
	sidecarContext, stopSidecar := context.WithCancel(ctx)
	stopped := make(chan error, 1)
	go func() { stopped <- sidecar.Run(sidecarContext) }()
	sidecarStopped := false
	t.Cleanup(func() {
		if !sidecarStopped {
			stopSidecar()
			<-stopped
		}
		require.NoError(t, sidecar.Close())
	})
	indices := []uint32{}
	for idx := range 2 {
		name := fmt.Sprintf("kni%d", idx)
		pair := &vnetlink.Tuntap{LinkAttrs: vnetlink.LinkAttrs{Name: name}, Mode: vnetlink.TUNTAP_MODE_TAP, Queues: 1}
		require.NoError(t, vnetlink.LinkAdd(pair))
		t.Cleanup(func() {
			_ = vnetlink.LinkDel(pair)
			for _, descriptor := range pair.Fds {
				_ = descriptor.Close()
			}
		})
		link, err := vnetlink.LinkByName(name)
		require.NoError(t, err)
		indices = append(indices, uint32(link.Attrs().Index))
		require.NoError(t, vnetlink.NeighSet(&vnetlink.Neigh{LinkIndex: link.Attrs().Index, IP: net.ParseIP("fe80::1"), HardwareAddr: net.HardwareAddr{2, 0, 0, 0, 0, byte(idx + 1)}, State: vnetlink.NUD_PERMANENT}))
	}
	stream, err := routes.FeedRIB(ctx)
	require.NoError(t, err)
	for idx, index := range indices {
		prefix, err := commonpb.NewIPPrefixFromPrefix(netip.MustParsePrefix(fmt.Sprintf("2001:db8:%d::/64", idx+1)))
		require.NoError(t, err)
		require.NoError(t, stream.Send(&operatorpb.Update{Name: "route0", Route: &operatorpb.Route{
			Prefix: prefix, NextHop: commonpb.NewIPAddressFromAddr(netip.MustParseAddr("fe80::1")),
			Peer: commonpb.NewIPAddressFromAddr(netip.MustParseAddr("2001:db8::2")), Ifindex: index,
			Source: operatorpb.RouteSourceID_ROUTE_SOURCE_ID_BIRD,
		}}))
	}
	_, err = stream.CloseAndRecv()
	require.NoError(t, err)
	sentinelPrefix := netip.MustParsePrefix("198.51.100.0/24")
	sentinel := vnetlink.Route{LinkIndex: int(indices[0]), Dst: &net.IPNet{IP: sentinelPrefix.Addr().AsSlice(), Mask: net.CIDRMask(24, 32)}, Table: 222, Protocol: 99}
	require.Eventually(t, func() bool { return len(gateway.Snapshot().GetEntries()) == 2 }, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, vnetlink.RouteAdd(&sentinel))
	for idx := range 2 {
		addressRange, err := commonpb.NewIPRange(netip.MustParseAddr(fmt.Sprintf("2001:db8:%d::", idx+1)), netip.MustParseAddr(fmt.Sprintf("2001:db8:%d:0:ffff:ffff:ffff:ffff", idx+1)))
		require.NoError(t, err)
		found := false
		for _, entry := range gateway.Snapshot().GetEntries() {
			if !proto.Equal(entry.GetRange(), addressRange) {
				continue
			}
			found = true
			require.Len(t, entry.GetNexthops(), 1)
			hop := entry.GetNexthops()[0]
			require.Equal(t, fmt.Sprintf("logical%d", idx), hop.GetDevice())
			require.Equal(t, uint64(0x020000000001+idx), hop.GetDstMac().GetAddr())
		}
		require.True(t, found)
	}
	require.NoError(t, os.Remove(path))
	neighbours := operatorpb.NewNeighbourServiceClient(connection)
	retained, err := vnetlink.RouteListFiltered(vnetlink.FAMILY_V4, &vnetlink.Route{Table: 222}, vnetlink.RT_FILTER_TABLE)
	require.NoError(t, err)
	require.Len(t, retained, 1)
	require.Equal(t, sentinel.Protocol, retained[0].Protocol)
	require.Equal(t, sentinel.Dst.String(), retained[0].Dst.String())
	stopProcess()
	_ = process.Wait()
	processContext, stopProcess = context.WithCancel(ctx)
	defer stopProcess()
	process = exec.CommandContext(processContext, binary, "-c", configPath)
	process.Stderr = os.Stderr
	require.NoError(t, process.Start())
	require.Eventually(t, func() bool {
		response, err := neighbours.List(ctx, &operatorpb.ListNeighboursRequest{Table: sidecarConfig.NeighbourTable})
		return err == nil && len(response.GetNeighbours()) >= 2
	}, 5*time.Second, 20*time.Millisecond)
	stopSidecar()
	<-stopped
	sidecarStopped = true
}
