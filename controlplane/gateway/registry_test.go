package gateway

import (
	"testing"

	"github.com/siderolabs/grpc-proxy/proxy"
	"github.com/stretchr/testify/require"
)

// fakeConn is a test double for the Conn interface.
type fakeConn struct {
	target string
	closed bool
}

func (m *fakeConn) Target() string { return m.target }
func (m *fakeConn) Close() error {
	m.closed = true
	return nil
}

// noopBackend is a proxy.Backend that does nothing, used where the backend
// value is stored by the registry but never invoked.
var noopBackend proxy.Backend

func TestBackendRegistry_FirstRegistration(t *testing.T) {
	reg := NewBackendRegistry()
	conn := &fakeConn{target: "127.0.0.1:9000"}

	status := reg.RegisterBackend("svc.Foo", noopBackend, conn)

	require.Equal(t, RegistrationRegistered, status)
	require.False(t, conn.closed)

	entry, ok := reg.backends["svc.Foo"]
	require.True(t, ok)
	require.Equal(t, "127.0.0.1:9000", entry.Endpoint())
}

func TestBackendRegistry_SameTargetRenews(t *testing.T) {
	reg := NewBackendRegistry()
	original := &fakeConn{target: "127.0.0.1:9000"}

	reg.RegisterBackend("svc.Foo", noopBackend, original)

	// Re-register with a different conn object but the same target.
	second := &fakeConn{target: "127.0.0.1:9000"}

	status := reg.RegisterBackend("svc.Foo", noopBackend, second)

	require.Equal(t, RegistrationRenewed, status)

	// The redundant new conn must be closed.
	require.True(t, second.closed, "redundant conn should be closed")

	// The original conn must still be open and retained.
	require.False(t, original.closed, "original conn must remain open")

	entry, ok := reg.backends["svc.Foo"]
	require.True(t, ok)
	// Stored entry still points at the original conn.
	require.Equal(t, original, entry.conn)
}

func TestBackendRegistry_DifferentTargetUpdates(t *testing.T) {
	reg := NewBackendRegistry()
	prev := &fakeConn{target: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", noopBackend, prev)

	next := &fakeConn{target: "127.0.0.1:9001"}

	status := reg.RegisterBackend("svc.Foo", noopBackend, next)

	require.Equal(t, RegistrationUpdated, status)

	// The previous conn must be closed.
	require.True(t, prev.closed, "previous conn should be closed")

	// The new conn must be open.
	require.False(t, next.closed, "new conn must remain open")

	entry, ok := reg.backends["svc.Foo"]
	require.True(t, ok)
	require.Equal(t, next, entry.conn)
	require.Equal(t, "127.0.0.1:9001", entry.Endpoint())
}

func TestBackendRegistry_Close(t *testing.T) {
	reg := NewBackendRegistry()
	connA := &fakeConn{target: "127.0.0.1:9000"}
	connB := &fakeConn{target: "127.0.0.1:9001"}

	reg.RegisterBackend("svc.A", noopBackend, connA)
	reg.RegisterBackend("svc.B", noopBackend, connB)

	err := reg.Close()
	require.NoError(t, err)

	require.True(t, connA.closed, "connA should be closed")
	require.True(t, connB.closed, "connB should be closed")

	// Registry must be empty after Close.
	require.Empty(t, reg.backends)
}

func TestBackendRegistry_ConcurrentRace(t *testing.T) {
	reg := NewBackendRegistry()

	const goroutines = 20
	done := make(chan struct{})
	for idx := 0; idx < goroutines; idx++ {
		go func() {
			defer func() { done <- struct{}{} }()
			conn := &fakeConn{target: "127.0.0.1:9000"}
			reg.RegisterBackend("svc.Race", noopBackend, conn)
			reg.GetBackend("svc.Race")
			reg.ListBackends()
		}()
	}
	for idx := 0; idx < goroutines; idx++ {
		<-done
	}

	_ = reg.Close()
}
