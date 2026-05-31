package gateway

import (
	"net"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
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
	backendRegistry := NewBackendRegistry()
	gatewayService := NewGatewayService(backendRegistry, zap.NewNop())

	gatewayServer := grpc.NewServer()
	ynpb.RegisterGatewayServer(gatewayServer, gatewayService)
	t.Cleanup(gatewayServer.Stop)

	go func() {
		_ = gatewayServer.Serve(trackingListener)
	}()

	serviceRunner := NewServiceRunner(&fakeService{}, trackingListener.Addr().String(), nil, zap.NewNop())

	backendAddrListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, backendAddrListener.Close())
	})

	err = serviceRunner.register(t.Context(), backendAddrListener.Addr())
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return trackingListener.AcceptedCount() > 0
	}, 2*time.Second, 25*time.Millisecond, "registration connection was not accepted")

	require.Eventually(t, func() bool {
		return trackingListener.ActiveCount() == 0
	}, 2*time.Second, 25*time.Millisecond, "registration client connections were not closed")
}
