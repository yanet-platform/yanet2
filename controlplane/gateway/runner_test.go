package gateway_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

type fakeService struct{}

func (m *fakeService) Name() string {
	return "fake-service"
}

func (m *fakeService) Endpoint() string {
	return "127.0.0.1:0"
}

func (m *fakeService) ServicesNames() []string {
	return []string{"fake.Service"}
}

func (m *fakeService) RegisterService(_ *grpc.Server) {}

type fakeConnection struct {
	net.Conn

	listener *connectionTrackingListener
	once     sync.Once
}

func (m *fakeConnection) Close() error {
	m.once.Do(func() {
		m.listener.unregister(m)
	})

	return m.Conn.Close()
}

type connectionTrackingListener struct {
	net.Listener

	mu     sync.Mutex
	seen   int
	active map[net.Conn]struct{}
}

func newConnectionTrackingListener(listener net.Listener) *connectionTrackingListener {
	return &connectionTrackingListener{
		Listener: listener,
		active:   map[net.Conn]struct{}{},
	}
}

func (m *connectionTrackingListener) Accept() (net.Conn, error) {
	conn, err := m.Listener.Accept()
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.seen++
	tracked := &fakeConnection{
		Conn:     conn,
		listener: m,
	}
	m.active[tracked] = struct{}{}
	m.mu.Unlock()

	return tracked, nil
}

func (m *connectionTrackingListener) unregister(conn net.Conn) {
	m.mu.Lock()
	delete(m.active, conn)
	m.mu.Unlock()
}

func (m *connectionTrackingListener) AcceptedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seen
}

func (m *connectionTrackingListener) ActiveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active)
}

func TestServiceRunner_registerClosesGatewayClientConnection(t *testing.T) {
	t.Parallel()

	gatewayListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	trackingListener := newConnectionTrackingListener(gatewayListener)
	backendRegistry := gateway.NewBackendRegistry()
	gatewayService := gateway.NewGatewayService(backendRegistry)

	gatewayServer := grpc.NewServer()
	ynpb.RegisterGatewayServer(gatewayServer, gatewayService)

	var wg errgroup.Group
	wg.Go(func() error {
		return gatewayServer.Serve(trackingListener)
	})
	t.Cleanup(func() {
		gatewayServer.Stop()
		_ = wg.Wait()
	})

	serviceRunner := gateway.NewServiceRunner(&fakeService{}, trackingListener.Addr().String(), nil)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var runnerGroup errgroup.Group
	runnerGroup.Go(func() error { return serviceRunner.Run(ctx) })

	select {
	case <-serviceRunner.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("service runner did not become ready")
	}

	require.Eventually(t, func() bool {
		return trackingListener.AcceptedCount() > 0
	}, 2*time.Second, 25*time.Millisecond, "registration connection was not accepted")

	require.Eventually(t, func() bool {
		return trackingListener.ActiveCount() == 0
	}, 2*time.Second, 25*time.Millisecond, "registration client connections were not closed")

	cancel()
	require.NoError(t, runnerGroup.Wait())
}
