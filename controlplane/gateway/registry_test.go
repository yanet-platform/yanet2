package gateway_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/siderolabs/grpc-proxy/proxy"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	"github.com/yanet-platform/yanet2/controlplane/gateway"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// fakeBackend is a test double for the Backend interface.
//
// Close is idempotent via sync.Once, mirroring the real backend, so tests
// that close it more than once still see CloseCount() == 1. closed and
// closeCount are guarded by mu, since Closed and CloseCount are polled from
// a goroutine other than the one that calls Close (require.Eventually runs
// its condition on its own goroutine).
type fakeBackend struct {
	endpoint   string
	mu         sync.Mutex
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
		m.mu.Lock()
		defer m.mu.Unlock()
		m.closed = true
		m.closeCount++
	})
	return nil
}

// Closed reports whether Close has run.
func (m *fakeBackend) Closed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// CloseCount returns how many times the underlying close work has run.
func (m *fakeBackend) CloseCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeCount
}

// Ensure fakeBackend satisfies both interfaces at compile time.
var _ proxy.Backend = (*fakeBackend)(nil)
var _ gateway.Backend = (*fakeBackend)(nil)

func getBackendEntry(t *testing.T, registry *gateway.BackendRegistry, service string) *gateway.BackendEntry {
	t.Helper()

	for _, entry := range registry.ListBackends() {
		if entry.Service() == service {
			return &entry
		}
	}

	t.Fatalf("backend %q is not registered", service)
	return &gateway.BackendEntry{}
}

func TestBackendRegistry_FirstRegistration(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	b := &fakeBackend{endpoint: "127.0.0.1:9000"}

	status := reg.RegisterBackend("svc.Foo", b, gateway.BackendKindExternal)

	require.Equal(t, gateway.RegistrationRegistered, status)
	require.False(t, b.Closed())

	entry := getBackendEntry(t, reg, "svc.Foo")
	require.Equal(t, "127.0.0.1:9000", entry.Endpoint())
}

func TestBackendRegistry_SameEndpointRenews(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	original := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", original, gateway.BackendKindExternal)

	// Re-register with a different object but the same endpoint and a claimed
	// kind change.
	second := &fakeBackend{endpoint: "127.0.0.1:9000"}

	status := reg.RegisterBackend("svc.Foo", second, gateway.BackendKindInProcess)

	require.Equal(t, gateway.RegistrationRenewed, status)

	// The redundant new backend must be closed.
	require.True(t, second.Closed(), "redundant backend should be closed")

	// The original backend must still be open and retained.
	require.False(t, original.Closed(), "original backend must remain open")

	entry := getBackendEntry(t, reg, "svc.Foo")
	require.Equal(t, original, entry.GetBackend())

	// The kind claim must have no effect: it is fixed at registration.
	require.Equal(t, gateway.BackendKindExternal, entry.Kind())
}

// TestBackendRegistry_DifferentEndpointUpdates verifies that a registration
// at a changed endpoint replaces the entry and closes the now-unreferenced
// previous backend exactly once.
func TestBackendRegistry_DifferentEndpointUpdates(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	prev := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", prev, gateway.BackendKindExternal)

	next := &fakeBackend{endpoint: "127.0.0.1:9001"}

	status := reg.RegisterBackend("svc.Foo", next, gateway.BackendKindExternal)

	require.Equal(t, gateway.RegistrationUpdated, status)

	// The previous backend must be closed exactly once.
	require.Equal(t, 1, prev.CloseCount(), "previous backend should be closed exactly once")

	// The new backend must be open.
	require.False(t, next.Closed(), "new backend must remain open")

	entry := getBackendEntry(t, reg, "svc.Foo")
	require.Equal(t, next, entry.GetBackend())
	require.Equal(t, "127.0.0.1:9001", entry.Endpoint())
}

// TestBackendRegistry_FreshNameSucceedsForEveryKind verifies that
// registering a name with no existing entry succeeds regardless of the
// backend kind.
//
// This guards the invariant that admitting a not-yet-known name never
// depends on which kind is claimed for it.
func TestBackendRegistry_FreshNameSucceedsForEveryKind(t *testing.T) {
	kinds := []gateway.BackendKind{gateway.BackendKindBuiltin, gateway.BackendKindInProcess, gateway.BackendKindExternal}

	for _, kind := range kinds {
		t.Run(kind.String(), func(t *testing.T) {
			reg := gateway.NewBackendRegistry()
			b := &fakeBackend{endpoint: "127.0.0.1:9000"}

			regStatus := reg.RegisterBackend("svc.Foo", b, kind)

			require.Equal(t, gateway.RegistrationRegistered, regStatus)
			require.False(t, b.Closed())
		})
	}
}

// TestBackendRegistry_DisplacingOneOfSeveralSharedNamesKeepsBackendOpen
// verifies that displacing one of several service names sharing a backend
// leaves that backend open and every other name still resolving to it.
//
// This is the regression guard for the shared loopback hazard: the gateway
// dials one connection and registers it under many service names, so
// closing it out from under the names that still use it would take down
// every one of those services, not just the displaced name.
func TestBackendRegistry_DisplacingOneOfSeveralSharedNamesKeepsBackendOpen(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	shared := &fakeBackend{endpoint: "127.0.0.1:8080"}
	reg.RegisterBackend("svc.A", shared, gateway.BackendKindBuiltin)
	reg.RegisterBackend("svc.B", shared, gateway.BackendKindBuiltin)

	other := &fakeBackend{endpoint: "127.0.0.1:9999"}
	status := reg.RegisterBackend("svc.A", other, gateway.BackendKindExternal)

	require.Equal(t, gateway.RegistrationUpdated, status)
	require.False(t, shared.Closed(), "the shared backend must not be closed")

	entryA := getBackendEntry(t, reg, "svc.A")
	require.Equal(t, other, entryA.GetBackend())

	entryB := getBackendEntry(t, reg, "svc.B")
	require.Equal(t, shared, entryB.GetBackend())
}

// TestBackendRegistry_DisplacingLastSharedNameClosesBackend verifies that
// displacing the last service name still referencing a backend does close
// it, exactly once, so the fix does not simply leak every displaced
// connection.
func TestBackendRegistry_DisplacingLastSharedNameClosesBackend(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	shared := &fakeBackend{endpoint: "127.0.0.1:8080"}
	reg.RegisterBackend("svc.A", shared, gateway.BackendKindBuiltin)
	reg.RegisterBackend("svc.B", shared, gateway.BackendKindBuiltin)

	otherA := &fakeBackend{endpoint: "127.0.0.1:9998"}
	reg.RegisterBackend("svc.A", otherA, gateway.BackendKindExternal)
	require.False(t, shared.Closed(), "the shared backend must remain open while svc.B references it")

	otherB := &fakeBackend{endpoint: "127.0.0.1:9999"}
	status := reg.RegisterBackend("svc.B", otherB, gateway.BackendKindExternal)

	require.Equal(t, gateway.RegistrationUpdated, status)
	require.Equal(t, 1, shared.CloseCount(), "the last-referenced backend must be closed exactly once")

	entryB := getBackendEntry(t, reg, "svc.B")
	require.Equal(t, otherB, entryB.GetBackend())
}

// TestBackendRegistry_RenewingWithASharedBackendKeepsItOpen verifies that
// renewing a service name with a backend object the registry already tracks
// under another service name leaves that backend open.
//
// This is the regression guard for the shared loopback hazard on the
// renewal path: renewing svc.A with the very backend object already
// registered under both svc.A and svc.B must not close it out from under
// svc.B, even though the renewal branch treats the passed-in backend as
// redundant.
func TestBackendRegistry_RenewingWithASharedBackendKeepsItOpen(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	shared := &fakeBackend{endpoint: "127.0.0.1:8080"}
	reg.RegisterBackend("svc.A", shared, gateway.BackendKindBuiltin)
	reg.RegisterBackend("svc.B", shared, gateway.BackendKindBuiltin)

	status := reg.RegisterBackend("svc.A", shared, gateway.BackendKindBuiltin)

	require.Equal(t, gateway.RegistrationRenewed, status)
	require.False(t, shared.Closed(), "the shared backend must not be closed")

	entryA := getBackendEntry(t, reg, "svc.A")
	require.Equal(t, shared, entryA.GetBackend())

	entryB := getBackendEntry(t, reg, "svc.B")
	require.Equal(t, shared, entryB.GetBackend())
}

func TestBackendRegistry_Close(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	bA := &fakeBackend{endpoint: "127.0.0.1:9000"}
	bB := &fakeBackend{endpoint: "127.0.0.1:9001"}

	reg.RegisterBackend("svc.A", bA, gateway.BackendKindExternal)
	reg.RegisterBackend("svc.B", bB, gateway.BackendKindExternal)

	err := reg.Close()
	require.NoError(t, err)

	require.True(t, bA.Closed(), "bA should be closed")
	require.True(t, bB.Closed(), "bB should be closed")

	// Registry must be empty after Close.
	require.Empty(t, reg.ListBackends())
}

func TestBackendRegistry_CloseSharedBackendOnce(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	shared := &fakeBackend{endpoint: "127.0.0.1:9000"}

	// Register the same backend instance under two different service names,
	// simulating the shared loopback backend used by built-in services.
	reg.RegisterBackend("svc.A", shared, gateway.BackendKindBuiltin)
	reg.RegisterBackend("svc.B", shared, gateway.BackendKindBuiltin)

	err := reg.Close()
	require.NoError(t, err)

	// Close is called once per registry entry (two), but the backend's own
	// idempotency (sync.Once) ensures the underlying work runs exactly once.
	require.Equal(t, 1, shared.CloseCount(), "shared backend must be closed exactly once")

	// Registry must be empty after Close.
	require.Empty(t, reg.ListBackends())
}

func TestBackendRegistry_Renew(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	b := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", b, gateway.BackendKindExternal)

	before := getBackendEntry(t, reg, "svc.Foo").LastSeenAt()

	// Sleep briefly so lastSeenAt can advance.
	time.Sleep(time.Millisecond)

	ok := reg.Renew("svc.Foo", "127.0.0.1:9000")
	require.True(t, ok, "Renew should return true for matching endpoint")
	require.True(t, getBackendEntry(t, reg, "svc.Foo").LastSeenAt().After(before), "lastSeenAt should advance")

	// Wrong endpoint: Renew must return false.
	ok = reg.Renew("svc.Foo", "127.0.0.1:9999")
	require.False(t, ok, "Renew should return false for different endpoint")

	// Absent service: Renew must return false.
	ok = reg.Renew("svc.Missing", "127.0.0.1:9000")
	require.False(t, ok, "Renew should return false for absent service")
}

func TestBackendRegistry_LeaseReleaseIsIdempotent(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	b := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", b, gateway.BackendKindExternal)

	_, release, ok := reg.GetBackend("svc.Foo")
	require.True(t, ok)

	release()
	release()
	require.False(t, b.Closed(), "registry entry must keep backend open after lease release")
	require.NoError(t, reg.Close())
	require.Equal(t, 1, b.CloseCount(), "releasing a lease twice must close once")
}

func TestBackend_CloseIdempotent(t *testing.T) {
	registry := gateway.NewBackendRegistry()
	service := gateway.NewGatewayService(registry)
	_, err := service.Register(t.Context(), &ynpb.RegisterRequest{
		Backend: &ynpb.BackendDesc{
			Name:     "svc.Foo",
			Endpoint: "passthrough:x",
		},
	})
	require.NoError(t, err)

	backend := getBackendEntry(t, registry, "svc.Foo").GetBackend()
	_, conn, err := backend.GetConnection(t.Context(), "")
	require.NoError(t, err)
	require.NoError(t, backend.Close(), "first Close must succeed")
	require.NoError(t, backend.Close(), "second Close must be a no-op")
	require.Equal(t, connectivity.Shutdown, conn.GetState(), "conn must be in Shutdown state")
}

func TestGateway_CloseIsIdempotent(t *testing.T) {
	listener := NewTestListener(t)
	gw, err := gateway.NewGateway(gateway.DefaultConfig(), gateway.WithListener(listener))
	require.NoError(t, err)

	require.NoError(t, gw.Close())
	require.NoError(t, gw.Close())
}

func TestBackendRegistry_ConcurrentRace(t *testing.T) {
	reg := gateway.NewBackendRegistry()

	const goroutines = 20
	done := make(chan struct{})
	for range goroutines {
		go func() {
			defer func() { done <- struct{}{} }()
			b := &fakeBackend{endpoint: "127.0.0.1:9000"}
			reg.RegisterBackend("svc.Race", b, gateway.BackendKindExternal)
			reg.Renew("svc.Race", "127.0.0.1:9000")
			if _, release, ok := reg.GetBackend("svc.Race"); ok {
				release()
			}
			reg.ListBackends()
		}()
	}
	for range goroutines {
		<-done
	}

	_ = reg.Close()
}

func TestBackendRegistry_KindIsPreserved(t *testing.T) {
	reg := gateway.NewBackendRegistry()

	builtin := &fakeBackend{endpoint: "127.0.0.1:9000"}
	inProc := &fakeBackend{endpoint: "127.0.0.1:9001"}
	ext := &fakeBackend{endpoint: "127.0.0.1:9002"}

	reg.RegisterBackend("svc.Builtin", builtin, gateway.BackendKindBuiltin)
	reg.RegisterBackend("svc.InProcess", inProc, gateway.BackendKindInProcess)
	reg.RegisterBackend("svc.External", ext, gateway.BackendKindExternal)

	entries := reg.ListBackends()
	kinds := map[string]gateway.BackendKind{}
	for _, e := range entries {
		kinds[e.Service()] = e.Kind()
	}

	require.Equal(t, gateway.BackendKindBuiltin, kinds["svc.Builtin"])
	require.Equal(t, gateway.BackendKindInProcess, kinds["svc.InProcess"])
	require.Equal(t, gateway.BackendKindExternal, kinds["svc.External"])
}

// TestBackendRegistry_RenewKindIsImmutable verifies that Renew never changes
// an entry's kind, regardless of what a caller claims, while always
// reporting success.
func TestBackendRegistry_RenewKindIsImmutable(t *testing.T) {
	cases := []struct {
		name    string
		initial gateway.BackendKind
		claimed gateway.BackendKind
	}{
		{"same kind claimed", gateway.BackendKindExternal, gateway.BackendKindExternal},
		{"external claims in-process", gateway.BackendKindExternal, gateway.BackendKindInProcess},
		{"in-process claims external", gateway.BackendKindInProcess, gateway.BackendKindExternal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := gateway.NewBackendRegistry()
			b := &fakeBackend{endpoint: "127.0.0.1:9000"}
			reg.RegisterBackend("svc.Foo", b, tc.initial)

			ok := reg.Renew("svc.Foo", "127.0.0.1:9000")
			require.True(t, ok, "Renew must report success even though the kind claim has no effect")

			entries := reg.ListBackends()
			require.Len(t, entries, 1)
			require.Equal(t, tc.initial, entries[0].Kind(), "kind must stay as it was at registration")
		})
	}
}

// TestBackendRegistry_EvictStaleRemovesExternal verifies that a stale
// external backend is removed from the registry and closed exactly once.
func TestBackendRegistry_EvictStaleRemovesExternal(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	b := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", b, gateway.BackendKindExternal)

	evicted := reg.EvictStale(time.Now().UTC().Add(time.Hour))

	require.Len(t, evicted, 1)
	require.Equal(t, "svc.Foo", evicted[0].Service())
	require.Equal(t, 1, b.CloseCount(), "evicted backend must be closed exactly once")

	require.Empty(t, reg.ListBackends(), "evicted entry must be removed from the registry")
}

// TestBackendRegistry_EvictStaleSparesNonExternal verifies that stale
// builtin and in-process entries are never evicted or closed.
//
// This is the regression guard for the constraint that only
// gateway.BackendKindExternal entries heartbeat. Builtin and in-process
// entries may share the gateway's loopback backend with other live services.
func TestBackendRegistry_EvictStaleSparesNonExternal(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	builtin := &fakeBackend{endpoint: "127.0.0.1:9000"}
	inProc := &fakeBackend{endpoint: "127.0.0.1:9001"}
	reg.RegisterBackend("svc.Builtin", builtin, gateway.BackendKindBuiltin)
	reg.RegisterBackend("svc.InProcess", inProc, gateway.BackendKindInProcess)

	evicted := reg.EvictStale(time.Now().UTC().Add(time.Hour))

	require.Empty(t, evicted)
	require.False(t, builtin.Closed(), "builtin backend must not be closed")
	require.False(t, inProc.Closed(), "in-process backend must not be closed")

	require.Equal(t, 2, len(reg.ListBackends()), "non-external entries must remain registered")
}

// TestBackendRegistry_EvictStaleSparesSharedBackend verifies that evicting a
// stale external entry leaves a shared builtin backend, registered under
// several service keys, open and its keys intact.
func TestBackendRegistry_EvictStaleSparesSharedBackend(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	shared := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.A", shared, gateway.BackendKindBuiltin)
	reg.RegisterBackend("svc.B", shared, gateway.BackendKindBuiltin)

	ext := &fakeBackend{endpoint: "127.0.0.1:9001"}
	reg.RegisterBackend("svc.External", ext, gateway.BackendKindExternal)

	evicted := reg.EvictStale(time.Now().UTC().Add(time.Hour))

	require.Len(t, evicted, 1)
	require.Equal(t, "svc.External", evicted[0].Service())
	require.True(t, ext.Closed(), "external backend must be closed")
	require.Equal(t, 0, shared.CloseCount(), "shared backend must remain open")

	require.Equal(t, 2, len(reg.ListBackends()), "shared builtin services must remain registered")
}

// TestBackendRegistry_LeaseSurvivesConcurrentEviction verifies that a
// backend obtained via GetBackend is not closed by a concurrent EvictStale
// while the lease is still held, and is closed once that lease is the last
// one released.
//
// This is the direct regression guard for the eviction race: GetBackend
// used to hand out a raw backend with no ownership guarantee, so a sweep
// that ran between lookup and use could close the connection out from
// under an in-flight call.
func TestBackendRegistry_LeaseSurvivesConcurrentEviction(t *testing.T) {
	t.Parallel()

	reg := gateway.NewBackendRegistry()
	b := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", b, gateway.BackendKindExternal)

	backend, release, ok := reg.GetBackend("svc.Foo")
	require.True(t, ok)
	require.NotNil(t, backend)

	evictedCh := make(chan []gateway.BackendEntry, 1)
	go func() {
		evictedCh <- reg.EvictStale(time.Now().UTC().Add(time.Hour))
	}()

	var evicted []gateway.BackendEntry
	select {
	case evicted = <-evictedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("EvictStale did not return")
	}
	require.Len(t, evicted, 1)

	require.False(t, b.Closed(), "backend must stay open while the lease obtained before eviction is still held")

	release()

	require.True(t, b.Closed(), "backend must be closed once its last outstanding lease is released")
}

// TestBackendRegistry_ReRegisterAfterEvictionLeavesNewBackendUsable verifies
// that releasing a lease held on a backend evicted and superseded by a
// fresh registration under the same service name closes only the old
// backend, never the new one.
//
// This guards the evict-then-re-register cycle: a lease taken before
// eviction outlives the entry it was obtained from, so its eventual
// release must not reach past that entry into whatever now occupies the
// same service name.
func TestBackendRegistry_ReRegisterAfterEvictionLeavesNewBackendUsable(t *testing.T) {
	t.Parallel()

	reg := gateway.NewBackendRegistry()
	old := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", old, gateway.BackendKindExternal)

	// Simulate a call that obtained the backend just before the sweeper
	// runs.
	_, release, ok := reg.GetBackend("svc.Foo")
	require.True(t, ok)

	evicted := reg.EvictStale(time.Now().UTC().Add(time.Hour))
	require.Len(t, evicted, 1)
	require.False(t, old.Closed(), "old backend must stay open while its lease is outstanding")

	// A fresh registration under the same service name, using a distinct
	// connection, lands before the old lease is released.
	next := &fakeBackend{endpoint: "127.0.0.1:9001"}
	status := reg.RegisterBackend("svc.Foo", next, gateway.BackendKindExternal)
	require.Equal(t, gateway.RegistrationRegistered, status)

	nextBackend, nextRelease, ok := reg.GetBackend("svc.Foo")
	require.True(t, ok)
	require.Same(t, next, nextBackend)

	// Releasing the stale lease must close only the now-unreferenced old
	// backend, never the newly registered one sharing its service name.
	release()
	require.True(t, old.Closed(), "old backend must close once its last lease is released")
	require.False(t, next.Closed(), "re-registered backend must not be closed by the old lease's release")

	nextRelease()
	require.False(t, next.Closed(), "releasing the only lease on a still-registered backend must not close it")
}

// TestBackendRegistry_RenewSavesFromEviction verifies that a Renew after
// registration refreshes lastSeenAt so the entry survives a sweep whose
// cutoff would otherwise have evicted it.
func TestBackendRegistry_RenewSavesFromEviction(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	b := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", b, gateway.BackendKindExternal)

	before := getBackendEntry(t, reg, "svc.Foo").LastSeenAt()
	// Sleep briefly so Renew's timestamp is distinguishable from registration.
	time.Sleep(time.Millisecond)

	ok := reg.Renew("svc.Foo", "127.0.0.1:9000")
	require.True(t, ok)
	renewed := getBackendEntry(t, reg, "svc.Foo").LastSeenAt()
	require.True(t, renewed.After(before), "Renew should advance lastSeenAt")

	cutoff := before.Add(renewed.Sub(before) / 2)
	require.True(t, cutoff.After(before))
	require.True(t, cutoff.Before(renewed))

	evicted := reg.EvictStale(cutoff)

	require.Empty(t, evicted)
	require.False(t, b.Closed(), "renewed backend must not be closed")

	require.Len(t, reg.ListBackends(), 1, "renewed entry must remain registered")
}

// TestBackendRegistry_EvictStaleCutoffOlderThanAllEntriesRemovesNothing
// verifies that a cutoff older than every entry's lastSeenAt evicts nothing.
func TestBackendRegistry_EvictStaleCutoffOlderThanAllEntriesRemovesNothing(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	b := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", b, gateway.BackendKindExternal)

	evicted := reg.EvictStale(time.Now().UTC().Add(-time.Hour))

	require.Empty(t, evicted)
	require.False(t, b.Closed())

	require.Len(t, reg.ListBackends(), 1, "entry must remain registered")
}

// TestBackendRegistry_RenewCannotArmSweeperAgainstSharedBackend verifies
// that renewing a builtin entry cannot relabel it as external.
//
// This is the regression guard for the loopback hazard: a Register RPC that
// names a builtin service's own endpoint must not relabel that entry, or
// the sweeper would later evict and close a backend shared with every other
// in-process service.
func TestBackendRegistry_RenewCannotArmSweeperAgainstSharedBackend(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	shared := &fakeBackend{endpoint: "127.0.0.1:8080"}
	reg.RegisterBackend("controlplane.ynpb.v1.Gateway", shared, gateway.BackendKindBuiltin)

	ok := reg.Renew("controlplane.ynpb.v1.Gateway", "127.0.0.1:8080")
	require.True(t, ok)

	after := time.Now().UTC()
	evicted := reg.EvictStale(after)

	require.Empty(t, evicted, "a builtin entry must never be evicted, even after a spoofed renewal")
	require.False(t, shared.Closed(), "shared backend must not be closed")

	entry := getBackendEntry(t, reg, "controlplane.ynpb.v1.Gateway")
	require.Equal(t, gateway.BackendKindBuiltin, entry.Kind(), "entry must keep its original kind")
}

// TestBackendRegistry_RenewCannotExemptExternalFromEviction verifies that
// renewing an external entry cannot relabel it out of the sweeper's reach.
//
// This is the regression guard for the reverse hazard: if a renewal could
// demote an external entry, a separate-process backend would become
// permanently non-evictable the moment it stopped heartbeating.
func TestBackendRegistry_RenewCannotExemptExternalFromEviction(t *testing.T) {
	reg := gateway.NewBackendRegistry()
	b := &fakeBackend{endpoint: "127.0.0.1:9000"}
	reg.RegisterBackend("svc.Foo", b, gateway.BackendKindExternal)

	ok := reg.Renew("svc.Foo", "127.0.0.1:9000")
	require.True(t, ok)

	cutoff := getBackendEntry(t, reg, "svc.Foo").LastSeenAt().Add(time.Nanosecond)
	evicted := reg.EvictStale(cutoff)

	require.Len(t, evicted, 1)
	require.Equal(t, "svc.Foo", evicted[0].Service())
	require.True(t, b.Closed(), "entry must not have become immortal after a renewal")
}

// TestGatewayService_RegisterKindResolution verifies that the Register RPC
// maps in_process=true to gateway.BackendKindInProcess and in_process=false
// (or unset) to gateway.BackendKindExternal.
func TestGatewayService_RegisterKindResolution(t *testing.T) {
	cases := []struct {
		name      string
		inProcess bool
		wantKind  gateway.BackendKind
	}{
		{"external when unset", false, gateway.BackendKindExternal},
		{"in-process when set", true, gateway.BackendKindInProcess},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := gateway.NewBackendRegistry()
			svc := gateway.NewGatewayService(reg)

			_, err := svc.Register(t.Context(), &ynpb.RegisterRequest{
				Backend: &ynpb.BackendDesc{
					Name:     "svc.Test",
					Endpoint: "passthrough:test-endpoint",
				},
				InProcess: tc.inProcess,
			})
			require.NoError(t, err)

			entry := getBackendEntry(t, reg, "svc.Test")
			require.Equal(t, tc.wantKind, entry.Kind())

			_ = reg.Close()
		})
	}
}
