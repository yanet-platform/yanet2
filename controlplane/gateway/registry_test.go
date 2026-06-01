package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/siderolabs/grpc-proxy/proxy"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

// fakeBackend is a test double for the Backend interface.
//
// Close is idempotent via sync.Once, mirroring the real backend, so tests that
// close it more than once still see closeCount == 1.
type fakeBackend struct {
	endpoint   string
	closed     bool
	closeCount int
	closeOnce  sync.Once
}

func (m *fakeBackend) Endpoint() string { return m.endpoint }
func (m *fakeBackend) String() string   { return m.endpoint }

func (m *fakeBackend) GetConnection(ctx context.Context, _ string) (context.Context, *grpc.ClientConn, error) {
	return ctx, nil, nil
}

func (m *fakeBackend) AppendInfo(_ bool, resp []byte) ([]byte, error) { return resp, nil }
func (m *fakeBackend) BuildError(bool, error) ([]byte, error)         { return nil, nil }

func (m *fakeBackend) Close() error {
	m.closeOnce.Do(func() {
		m.closed = true
		m.closeCount++
	})
	return nil
}

// Ensure fakeBackend satisfies both interfaces at compile time.
var _ proxy.Backend = (*fakeBackend)(nil)
var _ Backend = (*fakeBackend)(nil)

func TestBackendRegistry_FirstRegistration(t *testing.T) {
	reg := NewBackendRegistry()
	b := &fakeBackend{endpoint: "127.0.0.1:9000"}

	status := reg.RegisterBackend("svc.Foo", b)

	require.Equal(t, RegistrationRegistered, status)
	require.False(t, b.closed)

	entry, ok := reg.backends["svc.Foo"]
	require.True(t, ok)
	require.Equal(t, "127.0.0.1:9000", entry.Endpoint())
}

func TestBackendRegistry_SameEndpointRenews(t *testing.T) {
	reg := NewBackendRegistry()
	original := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", original)

	// Re-register with a different object but the same endpoint.
	second := &fakeBackend{endpoint: "127.0.0.1:9000"}

	status := reg.RegisterBackend("svc.Foo", second)

	require.Equal(t, RegistrationRenewed, status)

	// The redundant new backend must be closed.
	require.True(t, second.closed, "redundant backend should be closed")

	// The original backend must still be open and retained.
	require.False(t, original.closed, "original backend must remain open")

	entry, ok := reg.backends["svc.Foo"]
	require.True(t, ok)
	require.Equal(t, original, entry.backend)
}

func TestBackendRegistry_DifferentEndpointUpdates(t *testing.T) {
	reg := NewBackendRegistry()
	prev := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", prev)

	next := &fakeBackend{endpoint: "127.0.0.1:9001"}

	status := reg.RegisterBackend("svc.Foo", next)

	require.Equal(t, RegistrationUpdated, status)

	// The previous backend must be closed.
	require.True(t, prev.closed, "previous backend should be closed")

	// The new backend must be open.
	require.False(t, next.closed, "new backend must remain open")

	entry, ok := reg.backends["svc.Foo"]
	require.True(t, ok)
	require.Equal(t, next, entry.backend)
	require.Equal(t, "127.0.0.1:9001", entry.Endpoint())
}

func TestBackendRegistry_Close(t *testing.T) {
	reg := NewBackendRegistry()
	bA := &fakeBackend{endpoint: "127.0.0.1:9000"}
	bB := &fakeBackend{endpoint: "127.0.0.1:9001"}

	reg.RegisterBackend("svc.A", bA)
	reg.RegisterBackend("svc.B", bB)

	err := reg.Close()
	require.NoError(t, err)

	require.True(t, bA.closed, "bA should be closed")
	require.True(t, bB.closed, "bB should be closed")

	// Registry must be empty after Close.
	require.Empty(t, reg.backends)
}

func TestBackendRegistry_CloseSharedBackendOnce(t *testing.T) {
	reg := NewBackendRegistry()
	shared := &fakeBackend{endpoint: "127.0.0.1:9000"}

	// Register the same backend instance under two different service names,
	// simulating the shared loopback backend used by built-in services.
	reg.RegisterBackend("svc.A", shared)
	reg.RegisterBackend("svc.B", shared)

	err := reg.Close()
	require.NoError(t, err)

	// Close is called once per registry entry (two), but the backend's own
	// idempotency (sync.Once) ensures the underlying work runs exactly once.
	require.Equal(t, 1, shared.closeCount, "shared backend must be closed exactly once")

	// Registry must be empty after Close.
	require.Empty(t, reg.backends)
}

func TestBackendRegistry_Renew(t *testing.T) {
	reg := NewBackendRegistry()
	b := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", b)

	before := reg.backends["svc.Foo"].lastSeenAt

	// Sleep briefly so lastSeenAt can advance.
	time.Sleep(time.Millisecond)

	ok := reg.Renew("svc.Foo", "127.0.0.1:9000")
	require.True(t, ok, "Renew should return true for matching endpoint")
	require.True(t, reg.backends["svc.Foo"].lastSeenAt.After(before), "lastSeenAt should advance")

	// Wrong endpoint: Renew must return false.
	ok = reg.Renew("svc.Foo", "127.0.0.1:9999")
	require.False(t, ok, "Renew should return false for different endpoint")

	// Absent service: Renew must return false.
	ok = reg.Renew("svc.Missing", "127.0.0.1:9000")
	require.False(t, ok, "Renew should return false for absent service")
}

func TestBackend_CloseIdempotent(t *testing.T) {
	b, err := dialBackend("passthrough:x", insecure.NewCredentials())
	require.NoError(t, err)

	require.NoError(t, b.Close(), "first Close must succeed")
	require.NoError(t, b.Close(), "second Close must be a no-op")
	require.Equal(t, connectivity.Shutdown, b.conn.GetState(), "conn must be in Shutdown state")
}

func TestBackendRegistry_ConcurrentRace(t *testing.T) {
	reg := NewBackendRegistry()

	const goroutines = 20
	done := make(chan struct{})
	for range goroutines {
		go func() {
			defer func() { done <- struct{}{} }()
			b := &fakeBackend{endpoint: "127.0.0.1:9000"}
			reg.RegisterBackend("svc.Race", b)
			reg.Renew("svc.Race", "127.0.0.1:9000")
			reg.GetBackend("svc.Race")
			reg.ListBackends()
		}()
	}
	for range goroutines {
		<-done
	}

	_ = reg.Close()
}
