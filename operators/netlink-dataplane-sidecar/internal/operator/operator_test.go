package operator_test

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
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

// Test_NewOperator_CleansPartialDialFailure verifies that a later gateway dial
// failure closes the earlier connection and kernel handle before returning.
func Test_NewOperator_CleansPartialDialFailure(t *testing.T) {
	handle := &fakeNetlinkHandle{}
	firstConnection := &fakeGatewayConnection{}
	dialErr := errors.New("second dial failed")
	dialCount := 0

	runnable, err := sidecaroperator.NewOperator(
		twoGatewayConfig(),
		sidecaroperator.WithNetplanLoader(emptyNetplan),
		sidecaroperator.WithNetlinkHandleFactory(func() (
			sidecaroperator.NetlinkHandle,
			error,
		) {
			return handle, nil
		}),
		sidecaroperator.WithGatewayDialer(func(
			commonoperator.GatewayConfig,
		) (sidecaroperator.GatewayConnection, error) {
			dialCount++
			if dialCount == 1 {
				return firstConnection, nil
			}
			return nil, dialErr
		}),
	)

	require.Nil(t, runnable)
	require.ErrorIs(t, err, dialErr)
	require.True(t, firstConnection.Closed)
	require.True(t, handle.Closed)
}

// Test_NewOperator_HandleFactoryError verifies that a real socket-open failure
// returns its error without closing a typed-nil handle or panicking.
func Test_NewOperator_HandleFactoryError(t *testing.T) {
	if os.Getenv("YANET_TEST_HANDLE_FAILURE") == "1" {
		require.NoError(t, unix.Setrlimit(unix.RLIMIT_NOFILE, &unix.Rlimit{}))
		runnable, err := sidecaroperator.NewOperator(twoGatewayConfig(),
			sidecaroperator.WithNetplanLoader(emptyNetplan),
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
		sidecaroperator.WithNetplanLoader(emptyNetplan),
		sidecaroperator.WithNetlinkHandleFactory(func() (sidecaroperator.NetlinkHandle, error) {
			return handle, nil
		}),
	)
	require.Nil(t, runnable)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.True(t, handle.Closed)
}

// Test_NewOperator_ConfiguresSharedSocketTimeout verifies that every shared
// kernel socket is bounded before any gateway dependency is constructed.
func Test_NewOperator_ConfiguresSharedSocketTimeout(t *testing.T) {
	handle := &fakeNetlinkHandle{}
	runnable, err := sidecaroperator.NewOperator(
		twoGatewayConfig(),
		sidecaroperator.WithNetplanLoader(emptyNetplan),
		sidecaroperator.WithNetlinkHandleFactory(func() (sidecaroperator.NetlinkHandle, error) {
			return handle, nil
		}),
		sidecaroperator.WithGatewayDialer(func(
			commonoperator.GatewayConfig,
		) (sidecaroperator.GatewayConnection, error) {
			require.Equal(t, 5*time.Second, handle.SocketTimeout)
			require.Equal(t, 1, handle.SocketTimeoutCalls)
			return &fakeGatewayConnection{}, nil
		}),
	)
	require.NoError(t, err)
	require.NoError(t, runnable.Close())
}

// Test_NewOperator_ClosesHandleOnSocketTimeoutFailure verifies that failure
// to bound kernel I/O releases the handle before gateway construction.
func Test_NewOperator_ClosesHandleOnSocketTimeoutFailure(t *testing.T) {
	timeoutErr := errors.New("socket timeout configuration failed")
	handle := &fakeNetlinkHandle{SocketTimeoutErr: timeoutErr}
	runnable, err := sidecaroperator.NewOperator(
		twoGatewayConfig(),
		sidecaroperator.WithNetplanLoader(emptyNetplan),
		sidecaroperator.WithNetlinkHandleFactory(func() (sidecaroperator.NetlinkHandle, error) {
			return handle, nil
		}),
		sidecaroperator.WithGatewayDialer(func(
			commonoperator.GatewayConfig,
		) (sidecaroperator.GatewayConnection, error) {
			t.Fatal("gateway dialed after socket timeout configuration failed")
			return nil, nil
		}),
	)

	require.Nil(t, runnable)
	require.ErrorIs(t, err, timeoutErr)
	require.True(t, handle.Closed)
}

// Test_NewOperator_RejectsInvalidConfigBeforeCreatingHandle verifies that bad
// endpoints and scheduling values cannot allocate kernel resources.
func Test_NewOperator_RejectsInvalidConfigBeforeCreatingHandle(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*sidecaroperator.Config)
	}{
		{
			name: "malformed server endpoint",
			mutate: func(cfg *sidecaroperator.Config) {
				cfg.Server.Endpoint = xcfg.MustNonEmptyString("::1:8080")
			},
		},
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
				sidecaroperator.WithNetlinkHandleFactory(func() (
					sidecaroperator.NetlinkHandle,
					error,
				) {
					handleCreated = true
					return &fakeNetlinkHandle{}, nil
				}),
			)

			require.Error(t, err)
			require.Nil(t, runnable)
			require.False(t, handleCreated)
		})
	}
}

// Test_Operator_CloseReleasesResources verifies that normal shutdown closes
// both gateway connections and the shared kernel handle.
func Test_Operator_CloseReleasesResources(t *testing.T) {
	handle := &fakeNetlinkHandle{}
	connections := []*fakeGatewayConnection{{}, {}}
	dialCount := 0
	runnable, err := sidecaroperator.NewOperator(
		twoGatewayConfig(),
		sidecaroperator.WithNetplanLoader(emptyNetplan),
		sidecaroperator.WithNetlinkHandleFactory(func() (
			sidecaroperator.NetlinkHandle,
			error,
		) {
			return handle, nil
		}),
		sidecaroperator.WithGatewayDialer(func(
			commonoperator.GatewayConfig,
		) (sidecaroperator.GatewayConnection, error) {
			connection := connections[dialCount]
			dialCount++
			return connection, nil
		}),
	)
	require.NoError(t, err)

	require.NoError(t, runnable.Close())
	require.True(t, connections[0].Closed)
	require.True(t, connections[1].Closed)
	require.True(t, handle.Closed)
}

// emptyNetplan provides an interface-free configuration for resource tests.
func emptyNetplan(string) (sidecaroperator.State, error) {
	return sidecaroperator.State{}, nil
}

// twoGatewayConfig returns independent gateway connections with valid defaults.
func twoGatewayConfig() *sidecaroperator.Config {
	cfg := sidecaroperator.DefaultConfig()
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
// change recurring restoration or cause a second load.
func Test_Operator_LoadsNetplanOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "netplan.yaml")
	require.NoError(t, os.WriteFile(path, []byte("network: {version: 2}"), 0o600))
	config := twoGatewayConfig()
	config.NetplanPath = xcfg.MustNonEmptyString(path)
	config.Reconcile.Interval = xcfg.MustNonZero(5 * time.Millisecond)
	config.Reconcile.InitialBackoff = xcfg.MustNonZero(time.Millisecond)
	config.Reconcile.MaxBackoff = xcfg.MustNonZero(5 * time.Millisecond)
	handle := &fakeNetlinkHandle{}
	var loads atomic.Int64
	runnable, err := sidecaroperator.NewOperator(config,
		sidecaroperator.WithNetplanLoader(func(path string) (netplan.State, error) {
			loads.Add(1)
			return netplan.ParseFile(path)
		}),
		sidecaroperator.WithNetlinkHandleFactory(func() (sidecaroperator.NetlinkHandle, error) { return handle, nil }),
		sidecaroperator.WithGatewayDialer(func(commonoperator.GatewayConfig) (sidecaroperator.GatewayConnection, error) {
			return &fakeGatewayConnection{}, nil
		}),
		sidecaroperator.WithNeighbourSubscriber(func(updates chan<- vnetlink.NeighUpdate, done <-chan struct{}, options vnetlink.NeighSubscribeOptions) error {
			go func() { <-done; close(updates) }()
			return nil
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
}

// Test_NewOperator_RejectsNetplanBeforeResources verifies that startup loading
// and validation finish before any kernel handle or transport is allocated.
func Test_NewOperator_RejectsNetplanBeforeResources(t *testing.T) {
	state := netplan.State{Links: []netplan.Link{{Name: "eth0"}}}
	runnable, err := sidecaroperator.NewOperator(twoGatewayConfig(),
		sidecaroperator.WithNetplanLoader(func(string) (netplan.State, error) { return state, nil }),
		sidecaroperator.WithNetlinkHandleFactory(func() (sidecaroperator.NetlinkHandle, error) {
			t.Fatal("kernel handle opened for invalid desired state")
			return nil, nil
		}),
	)
	require.Nil(t, runnable)
	require.Error(t, err)
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
		sidecaroperator.WithNetplanLoader(emptyNetplan),
		sidecaroperator.WithNetlinkHandleFactory(func() (sidecaroperator.NetlinkHandle, error) { return &fakeNetlinkHandle{}, nil }),
		sidecaroperator.WithGatewayDialer(func(commonoperator.GatewayConfig) (sidecaroperator.GatewayConnection, error) {
			return &fakeGatewayConnection{}, nil
		}),
		sidecaroperator.WithNeighbourSubscriber(func(updates chan<- vnetlink.NeighUpdate, done <-chan struct{}, options vnetlink.NeighSubscribeOptions) error {
			go func() { <-done; close(updates) }()
			return nil
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
