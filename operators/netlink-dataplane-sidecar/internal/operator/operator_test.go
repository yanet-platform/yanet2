package operator_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/desired"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/native"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

type fakeNetlinkHandle struct {
	Closed             bool
	SocketTimeout      time.Duration
	SocketTimeoutCalls int
	SocketTimeoutErr   error
	ListCalls          atomic.Int64
}

func (m *fakeNetlinkHandle) LinkList() ([]vnetlink.Link, error) {
	m.ListCalls.Add(1)
	return nil, nil
}

func (m *fakeNetlinkHandle) LinkByName(string) (vnetlink.Link, error) {
	return nil, nil
}

func (m *fakeNetlinkHandle) LinkAdd(vnetlink.Link) error {
	return nil
}

func (m *fakeNetlinkHandle) LinkSetMTU(vnetlink.Link, int) error {
	return nil
}

func (m *fakeNetlinkHandle) LinkSetUp(vnetlink.Link) error {
	return nil
}

func (m *fakeNetlinkHandle) AddrList(vnetlink.Link, int) ([]vnetlink.Addr, error) {
	return nil, nil
}

func (m *fakeNetlinkHandle) AddrReplace(vnetlink.Link, *vnetlink.Addr) error {
	return nil
}

func (m *fakeNetlinkHandle) AddrDel(vnetlink.Link, *vnetlink.Addr) error {
	return nil
}

func (m *fakeNetlinkHandle) WalkNeighbours(ctx context.Context, visit func(vnetlink.Neigh) error) error {
	return ctx.Err()
}

func (m *fakeNetlinkHandle) Close() {
	m.Closed = true
}

func (m *fakeNetlinkHandle) SetSocketTimeout(timeout time.Duration) error {
	m.SocketTimeout = timeout
	m.SocketTimeoutCalls++
	return m.SocketTimeoutErr
}

type fakeGatewayConnection struct {
	Closed bool
}

func (m *fakeGatewayConnection) Invoke(
	context.Context,
	string,
	any,
	any,
	...grpc.CallOption,
) error {
	return nil
}

func (m *fakeGatewayConnection) NewStream(
	context.Context,
	*grpc.StreamDesc,
	string,
	...grpc.CallOption,
) (grpc.ClientStream, error) {
	return nil, errors.New("transport unavailable")
}

func (m *fakeGatewayConnection) Close() error {
	m.Closed = true
	return nil
}

// Test_NewOperator_RuntimeResources verifies that socket timeouts precede
// dialing and all transferred resources are closed on failure or shutdown.
func Test_NewOperator_RuntimeResources(t *testing.T) {
	injected := errors.New("runtime construction failed")
	for _, tc := range []struct {
		name           string
		socketError    error
		failSecondDial bool
		connections    int
	}{
		{name: "normal shutdown", connections: 2},
		{name: "socket timeout failure", socketError: injected},
		{name: "second dial failure", failSecondDial: true, connections: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handle := &fakeNetlinkHandle{SocketTimeoutErr: tc.socketError}
			connections := []*fakeGatewayConnection{}
			options := append(testRuntime(handle, nil),
				sidecaroperator.WithConfigSourceFactory(emptyConfigSource),
				sidecaroperator.WithGatewayDialer(func(commonoperator.GatewayConfig) (sidecaroperator.GatewayConnection, error) {
					require.Equal(t, 5*time.Second, handle.SocketTimeout)
					require.Equal(t, 1, handle.SocketTimeoutCalls)
					if tc.failSecondDial && len(connections) == 1 {
						return nil, injected
					}
					connection := &fakeGatewayConnection{}
					connections = append(connections, connection)
					return connection, nil
				}),
			)
			runnable, err := sidecaroperator.NewOperator(twoGatewayConfig(), options...)
			if tc.socketError != nil || tc.failSecondDial {
				require.Nil(t, runnable)
				require.ErrorIs(t, err, injected)
			} else {
				require.NoError(t, err)
				require.NoError(t, runnable.Close())
				require.NoError(t, runnable.Close())
			}
			require.Len(t, connections, tc.connections)
			for _, connection := range connections {
				require.True(t, connection.Closed)
			}
			require.True(t, handle.Closed)
		})
	}
}

// Test_NewOperator_HandleFactoryError verifies that a real socket-open failure
// returns its error without closing a typed-nil handle or panicking.
func Test_NewOperator_HandleFactoryError(t *testing.T) {
	if os.Getenv("YANET_TEST_HANDLE_FAILURE") == "1" {
		require.NoError(t, unix.Setrlimit(unix.RLIMIT_NOFILE, &unix.Rlimit{}))
		runnable, err := sidecaroperator.NewOperator(twoGatewayConfig(),
			sidecaroperator.WithConfigSourceFactory(emptyConfigSource),
		)
		require.Nil(t, runnable)
		require.ErrorIs(t, err, unix.EMFILE)
		return
	}
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^Test_NewOperator_HandleFactoryError$")
	command.Env = append(os.Environ(), "YANET_TEST_HANDLE_FAILURE=1")
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
}

// Test_NewOperator_TLSFailureCleanup verifies that a failed real credential
// load closes earlier resources and returns its cause without a typed-nil panic.
func Test_NewOperator_TLSFailureCleanup(t *testing.T) {
	config := twoGatewayConfig()
	config.Gateways[1].TLS = &xgrpc.ClientTLSConfig{
		CAFile: filepath.Join(t.TempDir(), "missing-ca.pem"),
	}
	handle := &fakeNetlinkHandle{}
	runnable, err := sidecaroperator.NewOperator(config,
		sidecaroperator.WithConfigSourceFactory(emptyConfigSource),
		sidecaroperator.WithNetlinkHandleFactory(func() (sidecaroperator.NetlinkHandle, error) {
			return handle, nil
		}),
	)
	require.Nil(t, runnable)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.True(t, handle.Closed)
}

// Test_NewOperator_RejectsInvalidConfigBeforeCreatingHandle verifies that bad
// scheduling values fail validation before allocating kernel resources.
func Test_NewOperator_RejectsInvalidConfigBeforeCreatingHandle(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*sidecaroperator.Config)
	}{
		{
			name: "negative register interval",
			mutate: func(config *sidecaroperator.Config) {
				config.Register.Interval = xcfg.MustNonZero(-time.Second)
			},
		},
		{
			name: "zero reconcile maximum backoff",
			mutate: func(config *sidecaroperator.Config) {
				config.Reconcile.MaxBackoff = xcfg.NonZero[time.Duration]{}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := twoGatewayConfig()
			test.mutate(cfg)
			handleCreated := false

			runnable, err := sidecaroperator.NewOperator(
				cfg,
				sidecaroperator.WithConfigSourceFactory(emptyConfigSource),
				sidecaroperator.WithNetlinkHandleFactory(func() (
					sidecaroperator.NetlinkHandle,
					error,
				) {
					handleCreated = true
					return &fakeNetlinkHandle{}, nil
				}),
			)

			require.ErrorContains(t, err, "invalid config")
			require.Nil(t, runnable)
			require.False(t, handleCreated)
		})
	}
}

// configSourceFunc controls the one startup load independently of its factory.
type configSourceFunc func() (desired.State, error)

func (m configSourceFunc) Load() (desired.State, error) { return m() }

// emptyConfigSource provides an interface-free configuration for resource tests.
func emptyConfigSource(*sidecaroperator.Config) (desired.Source, error) {
	return configSourceFunc(func() (desired.State, error) { return desired.State{}, nil }), nil
}

// twoGatewayConfig returns independent gateway connections with valid defaults.
func twoGatewayConfig() *sidecaroperator.Config {
	cfg := sidecaroperator.DefaultConfig()
	cfg.Source = "netplan"
	cfg.Gateways = []commonoperator.GatewayConfig{
		{
			Name:     "numa0",
			Endpoint: xcfg.MustNonEmptyString(net.JoinHostPort("::1", "8080")),
		},
		{
			Name:     "numa1",
			Endpoint: xcfg.MustNonEmptyString(net.JoinHostPort("::1", "8081")),
		},
	}
	return cfg
}

// Test_Operator_LoadsNetplanOnce verifies that replacing the startup file cannot
// change the managed topology or cause a second load.
func Test_Operator_LoadsNetplanOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netplan.yaml")
	require.NoError(t, os.WriteFile(path, []byte("network: {version: 2}"), 0o600))
	config := twoGatewayConfig()
	config.NetplanPath = &path
	config.Reconcile.Interval = xcfg.MustNonZero(5 * time.Millisecond)
	config.Reconcile.InitialBackoff = xcfg.MustNonZero(time.Millisecond)
	config.Reconcile.MaxBackoff = xcfg.MustNonZero(5 * time.Millisecond)
	handle := &fakeNetlinkHandle{}
	var loads atomic.Int64
	var factories atomic.Int64
	runnable, err := sidecaroperator.NewOperator(config,
		sidecaroperator.WithConfigSourceFactory(func(config *sidecaroperator.Config) (desired.Source, error) {
			factories.Add(1)
			adapter := &netplan.Source{Path: *config.NetplanPath}
			return configSourceFunc(func() (desired.State, error) {
				loads.Add(1)
				return adapter.Load()
			}), nil
		}),
		sidecaroperator.WithNetlinkHandleFactory(func() (sidecaroperator.NetlinkHandle, error) { return handle, nil }),
		sidecaroperator.WithGatewayDialer(func(commonoperator.GatewayConfig) (sidecaroperator.GatewayConnection, error) {
			return &fakeGatewayConnection{}, nil
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runnable.Close()) })
	require.NoError(t, os.WriteFile(path, []byte("network: {version: 2, ethernets: {kni0: {}}}"), 0o600))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- runnable.Run(ctx) }()
	require.Eventually(t, func() bool { return handle.ListCalls.Load() >= 6 }, time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
	require.Equal(t, int64(1), loads.Load())
	require.Equal(t, int64(1), factories.Load())
}

// registrationGateway captures operational registration independently of the
// outbound neighbour transport used by the sidecar.
type registrationGateway struct {
	ynpb.UnimplementedGatewayServer
	Registrations chan *ynpb.BackendDesc
}

func (m *registrationGateway) Register(ctx context.Context, request *ynpb.RegisterRequest) (*ynpb.RegisterResponse, error) {
	select {
	case m.Registrations <- request.GetBackend():
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &ynpb.RegisterResponse{}, nil
}

// Test_Operator_RegistersOnlyMetrics verifies that the operational server
// exposes shared metrics while neighbour transport failures remain independent.
func Test_Operator_RegistersOnlyMetrics(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	gateway := &registrationGateway{Registrations: make(chan *ynpb.BackendDesc, 16)}
	server := grpc.NewServer()
	ynpb.RegisterGatewayServer(server, gateway)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	config := twoGatewayConfig()
	config.Gateways = config.Gateways[:1]
	config.Gateways[0].Endpoint = xcfg.MustNonEmptyString(listener.Addr().String())
	config.Register.Interval = xcfg.MustNonZero(10 * time.Millisecond)
	runnable, err := sidecaroperator.NewOperator(config,
		sidecaroperator.WithConfigSourceFactory(emptyConfigSource),
		sidecaroperator.WithNetlinkHandleFactory(func() (sidecaroperator.NetlinkHandle, error) { return &fakeNetlinkHandle{}, nil }),
		sidecaroperator.WithGatewayDialer(func(commonoperator.GatewayConfig) (sidecaroperator.GatewayConnection, error) {
			return &fakeGatewayConnection{}, nil
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runnable.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- runnable.Run(ctx) }()
	for range 3 {
		select {
		case registration := <-gateway.Registrations:
			require.Equal(t, commonoperator.MetricsServiceName("netlink-dataplane-sidecar"), registration.GetName())
			connection, err := grpc.NewClient(registration.GetEndpoint(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			response := &commonpb.GetMetricsResponse{}
			err = connection.Invoke(ctx, "/"+registration.GetName()+"/GetMetrics", &commonpb.GetMetricsRequest{}, response)
			require.NoError(t, connection.Close())
			require.NoError(t, err)
			require.NotEmpty(t, response.GetMetrics())
		case <-ctx.Done():
			t.Fatal("operational registration did not complete")
		}
	}
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
}

// Test_NewOperator_ConfigSourceFailures verifies that factory, load and model
// failures cannot allocate a kernel handle or dial a gateway.
func Test_NewOperator_ConfigSourceFailures(t *testing.T) {
	injected := errors.New("injected startup error")
	for _, tc := range []struct {
		name    string
		factory sidecaroperator.ConfigSourceFactory
		cause   error
	}{
		{name: "missing factory"},
		{name: "factory error", cause: injected, factory: func(*sidecaroperator.Config) (desired.Source, error) { return nil, injected }},
		{name: "nil source", factory: func(*sidecaroperator.Config) (desired.Source, error) { return nil, nil }},
		{name: "load error", cause: injected, factory: func(*sidecaroperator.Config) (desired.Source, error) {
			return configSourceFunc(func() (desired.State, error) { return desired.State{}, injected }), nil
		}},
		{name: "invalid model", factory: func(*sidecaroperator.Config) (desired.Source, error) {
			return configSourceFunc(func() (desired.State, error) { return desired.State{Links: []desired.Link{{Name: "eth0"}}}, nil }), nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runnable, err := sidecaroperator.NewOperator(twoGatewayConfig(),
				sidecaroperator.WithConfigSourceFactory(tc.factory),
				sidecaroperator.WithNetlinkHandleFactory(func() (sidecaroperator.NetlinkHandle, error) {
					t.Fatal("kernel handle opened for invalid startup input")
					return nil, nil
				}),
				sidecaroperator.WithGatewayDialer(func(commonoperator.GatewayConfig) (sidecaroperator.GatewayConnection, error) {
					t.Fatal("gateway dialed for invalid startup input")
					return nil, nil
				}),
			)
			require.Nil(t, runnable)
			require.Error(t, err)
			if tc.cause != nil {
				require.ErrorIs(t, err, tc.cause)
			}
		})
	}
	config := twoGatewayConfig()
	config.Source = "unknown"
	runnable, err := sidecaroperator.NewOperator(config,
		sidecaroperator.WithConfigSourceFactory(func(*sidecaroperator.Config) (desired.Source, error) {
			t.Fatal("factory invoked for an unknown source")
			return nil, nil
		}),
	)
	require.Nil(t, runnable)
	require.ErrorContains(t, err, "source")
}

// Test_NewOperator_RealConfigSources verifies equivalent adapters and the
// default factory's selection without reading a Netplan file in native mode.
func Test_NewOperator_RealConfigSources(t *testing.T) {
	data, err := os.ReadFile("../native/testdata/native.yaml")
	require.NoError(t, err)
	config := twoGatewayConfig()
	require.NoError(t, xcfg.Decode([]byte("source: native\nnative:\n  "+strings.ReplaceAll(strings.TrimSpace(string(data)), "\n", "\n  ")+"\n"), config))
	var nativeSource desired.Source = &native.Source{Config: *config.Native}
	nativeState, err := nativeSource.Load()
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "netplan.yaml")
	netplanData := "network:\n  version: 2\n  " + strings.ReplaceAll(strings.TrimSpace(string(data)), "\n", "\n  ") + "\n"
	require.NoError(t, os.WriteFile(path, []byte(netplanData), 0o600))
	var netplanSource desired.Source = &netplan.Source{Path: path}
	netplanState, err := netplanSource.Load()
	require.NoError(t, err)
	require.Equal(t, nativeState, netplanState)
	for _, source := range []string{"netplan", "native"} {
		t.Run(source, func(t *testing.T) {
			selected := *config
			selected.Source = source
			if source == "netplan" {
				selected.Native = nil
				selected.NetplanPath = &path
			} else {
				require.NoError(t, os.Remove(path))
				require.Nil(t, selected.NetplanPath)
			}
			runnable, err := sidecaroperator.NewOperator(&selected,
				sidecaroperator.WithNetlinkHandleFactory(func() (sidecaroperator.NetlinkHandle, error) { return &fakeNetlinkHandle{}, nil }),
				sidecaroperator.WithGatewayDialer(func(commonoperator.GatewayConfig) (sidecaroperator.GatewayConnection, error) {
					return &fakeGatewayConnection{}, nil
				}),
			)
			require.NoError(t, err)
			require.NoError(t, runnable.Close())
		})
	}
}

// Test_NewOperator_DefaultNetplanPath verifies that only the Netplan adapter
// resolves an omitted path, without mutating the decoded config.
func Test_NewOperator_DefaultNetplanPath(t *testing.T) {
	if _, err := os.Stat(sidecaroperator.DefaultNetplanPath); !errors.Is(err, os.ErrNotExist) {
		t.Skip("default Netplan file exists; cannot assert a missing-file error")
	}
	config := twoGatewayConfig()
	runnable, err := sidecaroperator.NewOperator(config,
		sidecaroperator.WithNetlinkHandleFactory(func() (sidecaroperator.NetlinkHandle, error) {
			t.Fatal("kernel handle opened after missing startup file")
			return nil, nil
		}),
	)
	require.Nil(t, runnable)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, sidecaroperator.DefaultNetplanPath)
	require.Nil(t, config.NetplanPath)
}

// Test_NewOperator_RejectsNativeBeforeResources verifies real sidecar decoding
// and native loading reject schema and semantic failures before runtime I/O.
func Test_NewOperator_RejectsNativeBeforeResources(t *testing.T) {
	for _, input := range []string{
		"{ethernets: {kni0: {routes: []}}}",
		"{ethernets: {kni0: {routing-policy: []}}}",
		"{ethernets: {kni0: {mtu: 1500, mtu: 9000}}}",
		"{ethernets: {kni0: {dhcp4: 'false'}}}",
		"{ethernets: {kni0: {dhcp6: null}}}",
		"{ethernets: {kni0: {dhcp4: true}}}",
		"{ethernets: {kni0: {dhcp6: 0}}}",
		"{ethernets: {eth0: {}}}",
		"{ethernets: {kni0: {addresses: [invalid]}}}",
		"{ethernets: {kni0: {mtu: 1200}}}",
		"{vlans: {v0: {id: 0, link: kni0}}}",
		"{ethernets: {kni0: {link: ''}}}",
		"{ethernets: {kni0: null}}",
		"{ethernets: null}",
		"{network: {version: 2}}",
	} {
		t.Run(input, func(t *testing.T) {
			config := twoGatewayConfig()
			err := xcfg.Decode([]byte("source: native\nnative: "+input), config)
			if err == nil {
				var runnable *commonoperator.Operator[sidecaroperator.State]
				runnable, err = sidecaroperator.NewOperator(config,
					sidecaroperator.WithNetlinkHandleFactory(func() (sidecaroperator.NetlinkHandle, error) {
						t.Fatal("kernel handle opened for invalid native input")
						return nil, nil
					}),
					sidecaroperator.WithGatewayDialer(func(commonoperator.GatewayConfig) (sidecaroperator.GatewayConnection, error) {
						t.Fatal("gateway dialed for invalid native input")
						return nil, nil
					}),
				)
				require.Nil(t, runnable)
			}
			require.Error(t, err)
		})
	}
}

// pollingHandle fails setup before sysctl I/O but exposes healthy neighbours.
type pollingHandle struct {
	fakeNetlinkHandle
	SetupCalls atomic.Int64
	NextHop    atomic.Uint32
	Failure    error
}

func (m *pollingHandle) LinkList() ([]vnetlink.Link, error) {
	m.ListCalls.Add(1)
	link, err := m.LinkByName("kni0")
	return []vnetlink.Link{link}, err
}

func (m *pollingHandle) LinkByName(name string) (vnetlink.Link, error) {
	return &vnetlink.Device{LinkAttrs: vnetlink.LinkAttrs{
		Name: name, Index: 1, MTU: 1500, HardwareAddr: net.HardwareAddr{2, 0, 0, 0, 0, 1},
	}}, nil
}

func (m *pollingHandle) LinkSetMTU(vnetlink.Link, int) error {
	m.SetupCalls.Add(1)
	return m.Failure
}

func (m *pollingHandle) WalkNeighbours(ctx context.Context, visit func(vnetlink.Neigh) error) error {
	return visit(vnetlink.Neigh{LinkIndex: 1, IP: net.IPv4(192, 0, 2, byte(m.NextHop.Load()+1)), HardwareAddr: net.HardwareAddr{2, 0, 0, 0, 0, 2}, State: vnetlink.NUD_REACHABLE})
}

// recordingConnection retains immutable publication requests across workers.
type recordingConnection struct {
	fakeGatewayConnection
	Publications atomic.Int64
	Requests     chan *operatorpb.ReplaceNeighboursRequest
	Stop         atomic.Bool
	Cancel       context.CancelFunc
}

func (m *recordingConnection) Invoke(ctx context.Context, method string, request, response any, options ...grpc.CallOption) error {
	m.Publications.Add(1)
	select {
	case m.Requests <- request.(*operatorpb.ReplaceNeighboursRequest):
	default:
	}
	if m.Stop.Load() {
		m.Cancel()
	}
	return nil
}

// testRuntime supplies controlled kernel and gateway I/O without a source stub.
func testRuntime(handle sidecaroperator.NetlinkHandle, connection sidecaroperator.GatewayConnection) []sidecaroperator.Option {
	return []sidecaroperator.Option{
		sidecaroperator.WithNeighbourSubscriber(testSubscriber(nil)),
		sidecaroperator.WithNetlinkHandleFactory(func() (sidecaroperator.NetlinkHandle, error) { return handle, nil }),
		sidecaroperator.WithGatewayDialer(func(commonoperator.GatewayConfig) (sidecaroperator.GatewayConnection, error) { return connection, nil }),
	}
}

// Test_Operator_IndependentBootstrap verifies that missing links and setup
// failures neither cancel the watcher nor suppress healthy observed neighbours.
func Test_Operator_IndependentBootstrap(t *testing.T) {
	config := twoGatewayConfig()
	config.Reconcile.Interval = xcfg.MustNonZero(time.Hour)
	config.Reconcile.InitialBackoff = xcfg.MustNonZero(time.Millisecond)
	config.Reconcile.MaxBackoff = xcfg.MustNonZero(5 * time.Millisecond)
	state := sourceFixture(t)
	state.Links[0].MTU = 9000
	state.Links = append(state.Links, desired.Link{Name: "kni1"}, desired.Link{Name: "vlan0", Kind: desired.LinkKindVLAN, Parent: "kni1", VLANID: 100})
	var factories, loads atomic.Int64
	handle := &pollingHandle{Failure: errors.New("MTU setup failed")}
	connection := &recordingConnection{Requests: make(chan *operatorpb.ReplaceNeighboursRequest, 16)}
	events := make(chan vnetlink.NeighUpdate, 1)
	options := append(testRuntime(handle, connection), sidecaroperator.WithNeighbourSubscriber(testSubscriber(events)), sidecaroperator.WithConfigSourceFactory(func(*sidecaroperator.Config) (desired.Source, error) {
		factories.Add(1)
		return configSourceFunc(func() (desired.State, error) {
			loads.Add(1)
			return state, nil
		}), nil
	}))
	runnable, err := sidecaroperator.NewOperator(config, options...)
	require.NoError(t, err)
	state.Links[0].Name = "eth0"
	state.Links[0].Addresses[0] = netip.Prefix{}
	*state.Links[0].AcceptRA = true
	config.Source = "invalid after startup"
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- runnable.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-stopped; require.NoError(t, runnable.Close()) })
	for idx := range 3 {
		select {
		case request := <-connection.Requests:
			require.Len(t, request.GetEntries(), 1)
			require.Equal(t, "kni0", request.GetEntries()[0].GetDevice())
			address, err := request.GetEntries()[0].GetNextHop().ToAddr()
			require.NoError(t, err)
			if idx < 2 {
				require.Equal(t, netip.MustParseAddr("192.0.2.1"), address)
			} else {
				require.Equal(t, netip.MustParseAddr("192.0.2.2"), address)
			}
			if idx == 1 {
				handle.NextHop.Store(1)
				events <- vnetlink.NeighUpdate{Type: unix.RTM_NEWNEIGH}
			}
		case <-ctx.Done():
			t.Fatal("pending setup blocked neighbour publication")
		}
	}
	require.Eventually(t, func() bool { return handle.SetupCalls.Load() >= 2 }, time.Second, time.Millisecond)
	require.Equal(t, int64(1), factories.Load())
	require.Equal(t, int64(1), loads.Load())
}

// Test_Operator_BootstrapStops verifies that a completed worker does not cancel
// the operator or rerun setup on subsequent neighbour timer ticks.
func Test_Operator_BootstrapStops(t *testing.T) {
	config := twoGatewayConfig()
	config.Reconcile.Interval = xcfg.MustNonZero(5 * time.Millisecond)
	handle := &fakeNetlinkHandle{}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	connection := &recordingConnection{Cancel: cancel}
	core, logs := observer.New(zap.InfoLevel)
	options := append(testRuntime(handle, connection), sidecaroperator.WithConfigSourceFactory(emptyConfigSource), sidecaroperator.WithLog(zap.New(core)))
	runnable, err := sidecaroperator.NewOperator(config, options...)
	require.NoError(t, err)
	stopped := make(chan error, 1)
	go func() { stopped <- runnable.Run(ctx) }()
	finished := false
	t.Cleanup(func() {
		cancel()
		if !finished {
			<-stopped
		}
		require.NoError(t, runnable.Close())
	})
	require.Eventually(t, func() bool {
		return connection.Publications.Load() >= 3 && logs.FilterMessage("configured startup interfaces").Len() == 1
	}, time.Second, time.Millisecond)
	connection.Stop.Store(true)
	err = <-stopped
	finished = true
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 2+2*connection.Publications.Load(), handle.ListCalls.Load(), "two bootstrap reads plus two per poll")
}
