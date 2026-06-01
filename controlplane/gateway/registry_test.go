package gateway

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/siderolabs/grpc-proxy/proxy"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	testSvc = "test.Service"
	testEp1 = "127.0.0.1:1234"
	testEp2 = "127.0.0.1:5678"
)

// newTestConn returns a real *grpc.ClientConn suitable for use as a tracked
// prior connection in registry tests. grpc.NewClient performs no I/O until
// the first RPC, so no real server is needed.
func newTestConn(t *testing.T) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient("passthrough:fake",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	return conn
}

// simpleDial returns a dial function that succeeds, counts calls, and returns
// a proxy.SingleBackend with a nil conn (no I/O needed for these tests).
func simpleDial(calls *int32) func() (proxy.Backend, *grpc.ClientConn, error) {
	return func() (proxy.Backend, *grpc.ClientConn, error) {
		atomic.AddInt32(calls, 1)
		return &proxy.SingleBackend{}, nil, nil
	}
}

func TestBackendRegistry_RegisterBackend_firstRegistration(t *testing.T) {
	t.Parallel()

	var calls int32
	reg := NewBackendRegistry()

	status, err := reg.RegisterBackend(testSvc, testEp1, simpleDial(&calls))
	require.NoError(t, err)
	require.Equal(t, RegistrationRegistered, status)
	require.EqualValues(t, 1, atomic.LoadInt32(&calls), "dial must be called exactly once on first registration")
}

func TestBackendRegistry_RegisterBackend_sameEndpointRenewal(t *testing.T) {
	t.Parallel()

	var calls int32
	reg := NewBackendRegistry()

	_, err := reg.RegisterBackend(testSvc, testEp1, simpleDial(&calls))
	require.NoError(t, err)

	// Re-register at the same endpoint; dial must not be called again.
	status, err := reg.RegisterBackend(testSvc, testEp1, func() (proxy.Backend, *grpc.ClientConn, error) {
		t.Fatal("dial must not be called on same-endpoint renewal")
		return nil, nil, nil
	})
	require.NoError(t, err)
	require.Equal(t, RegistrationRenewed, status)
	require.EqualValues(t, 1, atomic.LoadInt32(&calls), "dial count must not increase on renewal")
}

func TestBackendRegistry_RegisterBackend_endpointChange(t *testing.T) {
	t.Parallel()

	reg := NewBackendRegistry()

	// Prime with ep1, injecting a real conn we can inspect after the update.
	prior := newTestConn(t)
	_, err := reg.RegisterBackend(testSvc, testEp1, func() (proxy.Backend, *grpc.ClientConn, error) {
		return &proxy.SingleBackend{}, prior, nil
	})
	require.NoError(t, err)
	require.NotEqual(t, connectivity.Shutdown, prior.GetState(), "conn must be open before endpoint change")

	// Update to ep2; registry must close the prior conn.
	var calls int32
	status, err := reg.RegisterBackend(testSvc, testEp2, simpleDial(&calls))
	require.NoError(t, err)
	require.Equal(t, RegistrationUpdated, status)
	require.EqualValues(t, 1, atomic.LoadInt32(&calls))
	require.Equal(t, connectivity.Shutdown, prior.GetState(), "prior conn must be closed after endpoint change")
}

func TestBackendRegistry_RegisterBackend_dialFailure(t *testing.T) {
	t.Parallel()

	reg := NewBackendRegistry()
	_, err := reg.RegisterBackend(testSvc, testEp1, func() (proxy.Backend, *grpc.ClientConn, error) {
		return nil, nil, errors.New("dial failed")
	})
	require.Error(t, err)

	_, ok := reg.GetBackend(testSvc)
	require.False(t, ok, "failed dial must not leave an entry in the registry")
}
