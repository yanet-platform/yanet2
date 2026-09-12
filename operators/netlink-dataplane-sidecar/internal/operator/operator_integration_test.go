package operator_test

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	routepb "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
)

// pipelineGateway records dataplane requests without programming the host.
type pipelineGateway struct {
	routepb.UnimplementedRouteServiceServer
	ynpb.UnimplementedFunctionServiceServer
	ynpb.UnimplementedGatewayServer
	mu     sync.Mutex
	latest *routepb.UpdateFIBRequest
	writes uint64
}

// nativeRestartHandle injects one transient kernel dump failure for retry.
type nativeRestartHandle struct {
	sidecaroperator.NetlinkHandle
	FailNext atomic.Bool
	Failures atomic.Int64
}

func (m *nativeRestartHandle) LinkList() ([]vnetlink.Link, error) {
	if m.FailNext.Swap(false) {
		m.Failures.Add(1)
		return nil, vnetlink.ErrDumpInterrupted
	}
	return m.NetlinkHandle.LinkList()
}

// startupFileConfig supplies equivalent topology through either file adapter.
func startupFileConfig(source, address string, mtu int) []byte {
	header := "source: native\nnative:\n"
	if source == "netplan" {
		header = "network:\n  version: 2\n"
	}
	data := fmt.Appendf([]byte(header), `  ethernets:
    kni9: {mtu: 1500, link-local: []}
    lo: {mtu: %d, addresses: [%q], accept-ra: false}
  vlans:
    vlan9: {id: 100, link: kni9, link-local: []}
  dummy-devices:
    dummy0: {mtu: %d, addresses: [%q], link-local: [], dhcp4: false, dhcp6: false}
`, mtu, address, mtu, address)
	if source == "native" {
		data = append(data, []byte("gateways: [{name: recording, endpoint: '127.0.0.1:9'}]\n")...)
	}
	return data
}

// newKernelTAP supplies a dataplane-owned Ethernet while retaining carrier.
func newKernelTAP(t *testing.T, name string) vnetlink.Link {
	t.Helper()
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
	return link
}

// nativeKernelMatches observes configured values without retaining kernel links.
func nativeKernelMatches(address string, mtu int) bool {
	for _, name := range []string{"lo", "dummy0"} {
		link, err := vnetlink.LinkByName(name)
		if err != nil || link.Attrs().MTU != mtu || link.Attrs().Flags&net.FlagUp == 0 {
			return false
		}
		addresses, err := vnetlink.AddrList(link, vnetlink.FAMILY_V4)
		if err != nil {
			return false
		}
		found := false
		for _, observed := range addresses {
			found = found || observed.IPNet != nil && observed.IPNet.String() == address
		}
		if !found {
			return false
		}
	}
	return true
}

// nativeKernelHasAddress detects unwanted replacement values on either link.
func nativeKernelHasAddress(address string) bool {
	for _, name := range []string{"lo", "dummy0"} {
		link, err := vnetlink.LinkByName(name)
		if err != nil {
			continue
		}
		addresses, err := vnetlink.AddrList(link, vnetlink.FAMILY_V4)
		if err != nil {
			continue
		}
		for _, observed := range addresses {
			if observed.IPNet != nil && observed.IPNet.String() == address {
				return true
			}
		}
	}
	return false
}

// driftNativeLinks removes initial addresses and changes MTU after bootstrap.
func driftNativeLinks(t *testing.T, prefix string) {
	t.Helper()
	address, err := vnetlink.ParseAddr(prefix)
	require.NoError(t, err)
	for _, name := range []string{"lo", "dummy0"} {
		link, err := vnetlink.LinkByName(name)
		require.NoError(t, err)
		require.NoError(t, vnetlink.AddrDel(link, address))
		require.NoError(t, vnetlink.LinkSetMTU(link, 2000))
	}
}

// Test_Operator_ConfigFileRestart verifies that neither drift nor startup file
// changes rerun completed setup, while periodic neighbour publication continues.
//
// A separate process reads the replacement only after the original exits. Each
// child owns a disposable namespace and uses the normal startup config loader.
func Test_Operator_ConfigFileRestart(t *testing.T) {
	if os.Getenv("YANET_NETNS_TESTS") != "1" {
		t.Skip("requires disposable namespaces and YANET_NETNS_TESTS=1")
	}
	const original, replacement = "192.0.2.10/32", "192.0.2.20/32"
	phase := os.Getenv("YANET_RESTART_PHASE")
	if phase == "" {
		binary, err := os.Executable()
		require.NoError(t, err)
		for _, source := range []string{"native", "netplan"} {
			t.Run(source, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "startup.yaml")
				require.NoError(t, os.WriteFile(path, startupFileConfig(source, original, 1500), 0o600))
				for _, phase := range []string{"original", "replacement", "invalid", "missing"} {
					if phase == "invalid" {
						require.NoError(t, os.WriteFile(path, []byte("invalid: ["), 0o600))
					}
					if phase == "missing" {
						require.NoError(t, os.Remove(path))
					}
					command := exec.CommandContext(t.Context(), binary, "-test.run", "^Test_Operator_ConfigFileRestart$", "-test.timeout", "20s")
					command.Env = append(os.Environ(), "YANET_RESTART_PHASE="+phase, "YANET_RESTART_PATH="+path, "YANET_RESTART_SOURCE="+source)
					command.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNET}
					output, err := command.CombinedOutput()
					require.NoError(t, err, "%s", output)
				}
			})
		}
		return
	}
	path, source := os.Getenv("YANET_RESTART_PATH"), os.Getenv("YANET_RESTART_SOURCE")
	config := sidecaroperator.DefaultConfig()
	var err error
	if source == "native" {
		config, err = xcfg.LoadConfig[sidecaroperator.Config](path, xcfg.WithKnownFields())
	} else {
		config.Source, config.NetplanPath = source, &path
	}
	if err != nil {
		require.Contains(t, []string{"invalid", "missing"}, phase, "%v", err)
		if phase == "missing" {
			require.ErrorIs(t, err, os.ErrNotExist)
		}
		return
	}
	config.Gateways = []commonoperator.GatewayConfig{{Name: "recording", Endpoint: xcfg.MustNonEmptyString("127.0.0.1:9")}}
	config.Reconcile.Interval = xcfg.MustNonZero(30 * time.Millisecond)
	config.Reconcile.InitialBackoff = xcfg.MustNonZero(10 * time.Millisecond)
	config.Reconcile.MaxBackoff = xcfg.MustNonZero(20 * time.Millisecond)
	loopback, err := vnetlink.LinkByName("lo")
	require.NoError(t, err)
	require.NoError(t, vnetlink.LinkSetUp(loopback))
	handle, err := netreconcile.NewHandle()
	require.NoError(t, err)
	backend := &nativeRestartHandle{NetlinkHandle: handle}
	connection := &recordingConnection{}
	core, logs := observer.New(zap.InfoLevel)
	options := append(testRuntime(backend, connection), sidecaroperator.WithLog(zap.New(core)))
	sidecar, err := sidecaroperator.NewOperator(config, options...)
	if phase == "invalid" || phase == "missing" {
		handle.Close()
		require.Error(t, err)
		if phase == "missing" {
			require.ErrorIs(t, err, os.ErrNotExist)
		}
		return
	}
	require.NoError(t, err)
	_, err = vnetlink.LinkByName("dummy0")
	require.Error(t, err, "a restart must use a fresh namespace")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- sidecar.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.ErrorIs(t, <-stopped, context.Canceled)
		require.NoError(t, sidecar.Close())
	})
	expected, mtu := original, 1500
	if phase == "replacement" {
		expected, mtu = replacement, 3000
	}
	require.Eventually(t, func() bool {
		return nativeKernelMatches(expected, mtu) && connection.Publications.Load() > 0
	}, 5*time.Second, 10*time.Millisecond)
	require.Zero(t, logs.FilterMessage("configured startup interfaces").Len())
	newKernelTAP(t, "kni9")
	require.Eventually(t, func() bool {
		return logs.FilterMessage("configured startup interfaces").Len() == 1
	}, 5*time.Second, 10*time.Millisecond)
	vlan, err := vnetlink.LinkByName("vlan9")
	require.NoError(t, err)
	require.NotZero(t, vlan.Attrs().Flags&net.FlagUp)
	if phase == "original" {
		driftNativeLinks(t, original)
		dummy, err := vnetlink.LinkByName("dummy0")
		require.NoError(t, err)
		require.NoError(t, vnetlink.LinkDel(dummy))
		require.NoError(t, vnetlink.LinkAdd(&vnetlink.Dummy{LinkAttrs: vnetlink.LinkAttrs{Name: "dummy0", MTU: 2000}}))
		for _, mutation := range []string{"write", "rename", "invalid", "delete"} {
			switch mutation {
			case "write":
				require.NoError(t, os.WriteFile(path, startupFileConfig(source, replacement, 3000), 0o600))
			case "rename":
				require.NoError(t, os.WriteFile(path+".new", startupFileConfig(source, replacement, 3000), 0o600))
				require.NoError(t, os.Rename(path+".new", path))
			case "invalid":
				require.NoError(t, os.WriteFile(path, []byte("native: ["), 0o600))
			case "delete":
				require.NoError(t, os.Remove(path))
			}
			previous := connection.Publications.Load()
			failures := backend.Failures.Load()
			backend.FailNext.Store(true)
			require.Eventually(t, func() bool {
				return backend.Failures.Load() > failures && connection.Publications.Load() >= previous+3
			}, 5*time.Second, 10*time.Millisecond, mutation)
			require.False(t, nativeKernelHasAddress(original), mutation)
			require.False(t, nativeKernelHasAddress(replacement), mutation)
			for _, name := range []string{"lo", "dummy0"} {
				link, err := vnetlink.LinkByName(name)
				require.NoError(t, err)
				require.Equal(t, 2000, link.Attrs().MTU)
			}
		}
		// Leave the replacement for a new process, never reload this instance.
		require.NoError(t, os.WriteFile(path, startupFileConfig(source, replacement, 3000), 0o600))
	} else {
		require.False(t, nativeKernelHasAddress(original))
	}
	require.Equal(t, 1, logs.FilterMessage("configured startup interfaces").Len())
}

func (m *pipelineGateway) UpdateFIB(ctx context.Context, request *routepb.UpdateFIBRequest) (*routepb.UpdateFIBResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latest = request
	m.writes++
	return &routepb.UpdateFIBResponse{}, nil
}

// Snapshot captures the last immutable dataplane request and RPC count together.
func (m *pipelineGateway) Snapshot() (*routepb.UpdateFIBRequest, uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.latest, m.writes
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

// Test_Operator_NeighbourPipeline verifies last-good FIB preservation on invalid
// or stale kernel input, recovery, and withdrawal on valid empty snapshots.
//
// Both sources publish through the sidecar and a route-operator subprocess.
// This opt-in test must run in a disposable network namespace. FeedRIB input is
// synthetic here; actual BIRD exporter provenance is a separate deployment gate.
func Test_Operator_NeighbourPipeline(t *testing.T) {
	if os.Getenv("YANET_NETNS_TESTS") != "1" {
		t.Skip("requires disposable network namespace and YANET_NETNS_TESTS=1")
	}
	for _, source := range []string{"netplan", "native"} {
		t.Run(source, func(t *testing.T) { runNeighbourPipeline(t, source) })
	}
}

// runNeighbourPipeline exercises complete and invalid dumps for both sources.
func runNeighbourPipeline(t *testing.T, source string) {
	t.Helper()
	binary := os.Getenv("YANET_ROUTE_OPERATOR_BINARY")
	require.NotEmpty(t, binary, "provide the built route-operator subprocess")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
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
	neighbours := operatorpb.NewNeighbourServiceClient(connection)
	readiness := operatorpb.NewReadinessServiceClient(connection)
	neighbourScope := &readinesspb.ReadyRequest{Scopes: []string{"neighbours"}}
	require.Eventually(t, func() bool {
		_, err := routes.ListConfigs(ctx, &operatorpb.ListConfigsRequest{})
		return err == nil
	}, 5*time.Second, 20*time.Millisecond)
	coldFIB, writes := gateway.Snapshot()
	require.Nil(t, coldFIB, "cold remote input must preserve prior dataplane state")
	require.Zero(t, writes)
	path := filepath.Join(t.TempDir(), "netplan.yaml")
	const topology = `ethernets:
  kni0: {mtu: 1500, link-local: [], addresses: ["fe80::f1/64"]}
  kni1: {mtu: 1500, link-local: [], addresses: ["fe80::1f1/64"]}
  lo: {addresses: ["192.0.2.10/32"]}
dummy-devices:
  dummy0: {addresses: ["192.0.2.11/32"]}
vlans:
  vlan0: {id: 100, link: kni0, link-local: []}`
	data := "  " + strings.ReplaceAll(topology, "\n", "\n  ") + "\n"
	sidecarConfig := sidecaroperator.DefaultConfig()
	sidecarConfig.Source = source
	sidecarConfig.Gateways = []commonoperator.GatewayConfig{
		{Name: "unavailable", Endpoint: xcfg.MustNonEmptyString("127.0.0.1:9")},
		{Name: "direct", Endpoint: xcfg.MustNonEmptyString(endpoint)},
	}
	if source == "netplan" {
		require.NoError(t, os.WriteFile(path, []byte("network:\n  version: 2\n"+data), 0o600))
		sidecarConfig.NetplanPath = &path
	} else {
		require.NoError(t, xcfg.Decode([]byte("source: native\nnative:\n"+data), sidecarConfig))
	}
	t.Cleanup(func() {
		if link, err := vnetlink.LinkByName("dummy0"); err == nil {
			_ = vnetlink.LinkDel(link)
		}
		if link, err := vnetlink.LinkByName("lo"); err == nil {
			address, err := vnetlink.ParseAddr("192.0.2.10/32")
			if err == nil {
				_ = vnetlink.AddrDel(link, address)
			}
		}
	})
	sidecarConfig.NeighbourPublishTimeout = 200 * time.Millisecond
	sidecarConfig.LinkMap = map[string]string{"kni0": "logical0", "kni1": "logical1"}
	sidecarConfig.Reconcile.Interval = xcfg.MustNonZero(20 * time.Millisecond)
	sidecarConfig.Reconcile.InitialBackoff = xcfg.MustNonZero(10 * time.Millisecond)
	sidecarConfig.Reconcile.MaxBackoff = xcfg.MustNonZero(20 * time.Millisecond)
	core, logs := observer.New(zap.WarnLevel)
	sidecar, err := sidecaroperator.NewOperator(sidecarConfig, sidecaroperator.WithLog(zap.New(core)))
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
	kernelNeighbours := []vnetlink.Neigh{}
	for idx := range 2 {
		name := fmt.Sprintf("kni%d", idx)
		link := newKernelTAP(t, name)
		neighbour := vnetlink.Neigh{LinkIndex: link.Attrs().Index, IP: net.ParseIP(fmt.Sprintf("fe80::%d", idx+1)), HardwareAddr: net.HardwareAddr{2, 0, 0, 0, 0, byte(idx + 1)}, State: vnetlink.NUD_PERMANENT}
		require.NoError(t, vnetlink.NeighSet(&neighbour))
		kernelNeighbours = append(kernelNeighbours, neighbour)
	}
	stream, err := routes.FeedRIB(ctx)
	require.NoError(t, err)
	for idx, neighbour := range kernelNeighbours {
		prefix, err := commonpb.NewIPPrefixFromPrefix(netip.MustParsePrefix(fmt.Sprintf("2001:db8:%d::/64", idx+1)))
		require.NoError(t, err)
		require.NoError(t, stream.Send(&operatorpb.Update{Name: "route0", Route: &operatorpb.Route{
			Prefix: prefix, NextHop: commonpb.NewIPAddressFromAddr(netip.MustParseAddr(fmt.Sprintf("fe80::%d", idx+1))),
			Peer: commonpb.NewIPAddressFromAddr(netip.MustParseAddr("2001:db8::2")), Ifindex: uint32(neighbour.LinkIndex),
			Source: operatorpb.RouteSourceID_ROUTE_SOURCE_ID_BIRD,
		}}))
	}
	_, err = stream.CloseAndRecv()
	require.NoError(t, err)
	sentinelPrefix := netip.MustParsePrefix("198.51.100.0/24")
	sentinel := vnetlink.Route{LinkIndex: kernelNeighbours[0].LinkIndex, Dst: &net.IPNet{IP: sentinelPrefix.Addr().AsSlice(), Mask: net.CIDRMask(24, 32)}, Table: 222, Protocol: 99}
	var initialFIB *routepb.UpdateFIBRequest
	require.Eventually(t, func() bool {
		initialFIB, writes = gateway.Snapshot()
		return writes > 0 && len(initialFIB.GetEntries()) == 2
	}, 5*time.Second, 20*time.Millisecond)
	require.Equal(t, "route0", initialFIB.GetModuleName())
	require.NoError(t, vnetlink.RouteAdd(&sentinel))
	var firstHop *routepb.FIBNexthop
	for idx := range 2 {
		addressRange, err := commonpb.NewIPRange(netip.MustParseAddr(fmt.Sprintf("2001:db8:%d::", idx+1)), netip.MustParseAddr(fmt.Sprintf("2001:db8:%d:0:ffff:ffff:ffff:ffff", idx+1)))
		require.NoError(t, err)
		found := false
		for _, entry := range initialFIB.GetEntries() {
			if !proto.Equal(entry.GetRange(), addressRange) {
				continue
			}
			found = true
			require.Len(t, entry.GetNexthops(), 1)
			hop := entry.GetNexthops()[0]
			require.Equal(t, fmt.Sprintf("logical%d", idx), hop.GetDevice())
			require.Equal(t, uint64(0x020000000001+idx), hop.GetDstMac().GetAddr())
			if idx == 0 {
				firstHop = hop
			}
		}
		require.True(t, found)
	}
	if source == "netplan" {
		require.NoError(t, os.Remove(path))
	}
	initialNeighbours, err := neighbours.List(ctx, &operatorpb.ListNeighboursRequest{Table: sidecarConfig.NeighbourTable})
	require.NoError(t, err)
	require.Len(t, initialNeighbours.GetNeighbours(), 2)
	ready, err := readiness.Ready(ctx, neighbourScope)
	require.NoError(t, err)
	require.Len(t, ready.GetScopes(), 1)
	require.Equal(t, readinesspb.State_STATE_READY, ready.GetScopes()[0].GetState())

	// Duplicate unicast identity rejects the entire dump, including its heartbeat.
	duplicate := kernelNeighbours[0]
	duplicate.LinkIndex = kernelNeighbours[1].LinkIndex
	require.NoError(t, vnetlink.NeighSet(&duplicate))
	require.Eventually(t, func() bool {
		return logs.Filter(func(entry observer.LoggedEntry) bool {
			return strings.Contains(fmt.Sprint(entry.ContextMap()["error"]), "duplicate next hop fe80::1")
		}).Len() > 0
	}, 5*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		response, err := readiness.Ready(ctx, neighbourScope)
		if err != nil || len(response.GetScopes()) != 1 {
			return false
		}
		scope := response.GetScopes()[0]
		return scope.GetState() == readinesspb.State_STATE_NOT_READY &&
			len(scope.GetReasons()) == 1 && scope.GetReasons()[0].GetCode() == "STALE"
	}, 5*time.Second, 20*time.Millisecond, "invalid dumps must not refresh the 1s input age")
	lastGoodNeighbours, err := neighbours.List(ctx, &operatorpb.ListNeighboursRequest{Table: sidecarConfig.NeighbourTable})
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(initialNeighbours, lastGoodNeighbours,
		protocmp.Transform(), protocmp.SortRepeatedFields(initialNeighbours, "neighbours"),
	))
	lastGoodFIB, staleWrites := gateway.Snapshot()
	require.Empty(t, cmp.Diff(initialFIB, lastGoodFIB,
		protocmp.Transform(), protocmp.SortRepeatedFields(initialFIB, "entries"),
	))

	// A real RIB mutation must not authorize a FIB write while input is stale.
	addedPrefix, err := commonpb.NewIPPrefixFromPrefix(netip.MustParsePrefix("2001:db8:3::/64"))
	require.NoError(t, err)
	addedHop := commonpb.NewIPAddressFromAddr(netip.MustParseAddr("fe80::1"))
	_, err = routes.InsertRoute(ctx, &operatorpb.InsertRouteRequest{
		Name: "route0", Prefix: addedPrefix, NexthopAddrs: []*commonpb.IPAddress{addedHop},
		SourceId: operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC, DoFlush: true,
	})
	require.NoError(t, err)
	lookup, err := routes.LookupRoute(ctx, &operatorpb.LookupRouteRequest{
		Name: "route0", IpAddr: commonpb.NewIPAddressFromAddr(netip.MustParseAddr("2001:db8:3::1")),
	})
	require.NoError(t, err)
	require.True(t, proto.Equal(addedPrefix, lookup.GetPrefix()))
	require.Len(t, lookup.GetRoutes(), 1)
	require.True(t, proto.Equal(addedHop, lookup.GetRoutes()[0].GetNextHop()))
	require.Equal(t, operatorpb.RouteSourceID_ROUTE_SOURCE_ID_STATIC, lookup.GetRoutes()[0].GetSource())
	require.Never(t, func() bool {
		current, writes := gateway.Snapshot()
		return writes != staleWrites || !proto.Equal(lastGoodFIB, current)
	}, 300*time.Millisecond, 20*time.Millisecond, "stale input must preserve the last-good FIB without new RPCs")

	// Recovery of identical neighbour content must apply the buffered RIB change.
	require.NoError(t, vnetlink.NeighDel(&duplicate))
	require.Eventually(t, func() bool {
		response, err := readiness.Ready(ctx, neighbourScope)
		return err == nil && len(response.GetScopes()) == 1 && response.GetScopes()[0].GetState() == readinesspb.State_STATE_READY
	}, 5*time.Second, 20*time.Millisecond)
	recoveredNeighbours, err := neighbours.List(ctx, &operatorpb.ListNeighboursRequest{Table: sidecarConfig.NeighbourTable})
	require.NoError(t, err)
	require.Empty(t, cmp.Diff(initialNeighbours, recoveredNeighbours,
		protocmp.Transform(), protocmp.SortRepeatedFields(initialNeighbours, "neighbours"),
	))
	var recoveredFIB *routepb.UpdateFIBRequest
	require.Eventually(t, func() bool {
		recoveredFIB, writes = gateway.Snapshot()
		return writes > staleWrites && len(recoveredFIB.GetEntries()) == 3
	}, 5*time.Second, 20*time.Millisecond)
	addedRange, err := commonpb.NewIPRange(netip.MustParseAddr("2001:db8:3::"), netip.MustParseAddr("2001:db8:3:0:ffff:ffff:ffff:ffff"))
	require.NoError(t, err)
	expectedEntries := append([]*routepb.FIBEntry{}, initialFIB.GetEntries()...)
	expectedEntries = append(expectedEntries, &routepb.FIBEntry{Range: addedRange, Nexthops: []*routepb.FIBNexthop{firstHop}})
	expectedFIB := &routepb.UpdateFIBRequest{ModuleName: "route0", Entries: expectedEntries}
	require.Empty(t, cmp.Diff(expectedFIB, recoveredFIB,
		protocmp.Transform(), protocmp.SortRepeatedFields(expectedFIB, "entries"),
	))

	// A committed empty table is fresh input, not a failed dump or FIB apply ACK.
	_, beforeEmptyWrites := gateway.Snapshot()
	for idx := range kernelNeighbours {
		require.NoError(t, vnetlink.NeighDel(&kernelNeighbours[idx]))
	}
	require.Eventually(t, func() bool {
		response, err := neighbours.List(ctx, &operatorpb.ListNeighboursRequest{Table: sidecarConfig.NeighbourTable})
		return err == nil && len(response.GetNeighbours()) == 0
	}, 5*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		current, writes := gateway.Snapshot()
		return writes > beforeEmptyWrites && current.GetModuleName() == "route0" && len(current.GetEntries()) == 0
	}, 5*time.Second, 20*time.Millisecond)
	ready, err = readiness.Ready(ctx, neighbourScope)
	require.NoError(t, err)
	require.Len(t, ready.GetScopes(), 1)
	require.Equal(t, readinesspb.State_STATE_READY, ready.GetScopes()[0].GetState())
	for idx := range kernelNeighbours {
		require.NoError(t, vnetlink.NeighSet(&kernelNeighbours[idx]))
	}
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
