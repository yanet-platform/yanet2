package gateway

import (
	"errors"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/siderolabs/grpc-proxy/proxy"
)

// RegistrationStatus describes how a Register call changed the registry.
type RegistrationStatus int

const (
	RegistrationRegistered RegistrationStatus = iota + 1
	RegistrationRenewed
	RegistrationUpdated
)

// BackendKind classifies how a backend is hosted relative to the gateway.
type BackendKind int

const (
	BackendKindBuiltin BackendKind = iota + 1
	BackendKindInProcess
	BackendKindExternal
)

// String returns the human-readable name of the backend kind.
func (m BackendKind) String() string {
	switch m {
	case BackendKindBuiltin:
		return "builtin"
	case BackendKindInProcess:
		return "in_process"
	case BackendKindExternal:
		return "external"
	default:
		return "unspecified"
	}
}

// Backend is a routable, closeable upstream connection tracked by the registry.
type Backend interface {
	proxy.Backend
	Endpoint() string
	io.Closer
}

// BackendEntry holds metadata about a single registered backend.
type BackendEntry struct {
	service    string
	backend    Backend
	kind       BackendKind
	lastSeenAt time.Time
}

// Service returns the service name of the entry.
func (m *BackendEntry) Service() string {
	return m.service
}

// Endpoint returns the endpoint of the entry.
func (m *BackendEntry) Endpoint() string {
	return m.backend.Endpoint()
}

// LastSeenAt returns the time the entry was last registered.
func (m *BackendEntry) LastSeenAt() time.Time {
	return m.lastSeenAt
}

// Kind returns the hosting classification of the entry.
func (m *BackendEntry) Kind() BackendKind {
	return m.kind
}

// GetBackend returns the Backend for this entry.
func (m *BackendEntry) GetBackend() Backend {
	return m.backend
}

// Close closes the entry's underlying backend.
//
// Only the owning registry closes an entry. An entry handed out by
// ListBackends must not be closed by its receiver.
func (m *BackendEntry) Close() error {
	return m.backend.Close()
}

// backendRef is the shared, atomically-updated refcount for one distinct
// backend connection.
//
// Its count is the number of registry entries currently resolving to
// backend plus the number of leases handed out for it by GetBackend. The
// backend is closed the instant the count reaches zero, by whichever
// decrement gets it there.
type backendRef struct {
	backend Backend
	count   atomic.Int64
}

// GetBackend returns the backend this ref counts references to.
func (m *backendRef) GetBackend() Backend {
	return m.backend
}

// Retain records one more reference to the backend.
func (m *backendRef) Retain() {
	m.count.Add(1)
}

// Release drops one reference to the backend and reports whether that was
// the last one.
func (m *backendRef) Release() bool {
	return m.count.Add(-1) == 0
}

// BackendRegistry is a registry of backends for Gateway API.
type BackendRegistry struct {
	mu       sync.RWMutex
	backends map[string]BackendEntry
	refs     map[Backend]*backendRef
}

// NewBackendRegistry creates a new BackendRegistry.
func NewBackendRegistry() *BackendRegistry {
	return &BackendRegistry{
		backends: map[string]BackendEntry{},
		refs:     map[Backend]*backendRef{},
	}
}

// GetBackend returns a leased backend for the given service, plus a release
// function the caller must call exactly once when it is done using the
// backend.
//
// Service parameter must be in gRPC format, such as "routepb.RouteService".
// The lease keeps the backend's connection open even if EvictStale or a
// displacing RegisterBackend removes it from the registry while the lease
// is outstanding: the connection is only closed once every lease on it has
// been released and no registry entry resolves to it any more. Release is
// idempotent and safe to call after the entry backing the lease has been
// evicted or displaced.
func (m *BackendRegistry) GetBackend(service string) (proxy.Backend, func(), bool) {
	m.mu.RLock()
	entry, ok := m.backends[service]
	if !ok {
		m.mu.RUnlock()
		return nil, func() {}, false
	}

	ref := m.refs[entry.backend]
	ref.Retain()
	m.mu.RUnlock()

	return entry.GetBackend(), m.lease(ref), true
}

// HasBackend reports whether a backend is currently registered for service,
// without leasing it.
//
// Service parameter must be in gRPC format, such as "routepb.RouteService".
func (m *BackendRegistry) HasBackend(service string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.backends[service]
	return ok
}

// lease returns an idempotent release function for ref.
//
// Wrapping the decrement in sync.Once means a caller that releases more
// than once, deliberately or by accident, still drops the refcount by
// exactly one.
func (m *BackendRegistry) lease(ref *backendRef) func() {
	var once sync.Once
	return func() {
		once.Do(func() { m.releaseRef(ref) })
	}
}

// releaseRef drops one reference from ref and closes its backend, outside
// the registry lock, if that was the last reference of any kind.
func (m *BackendRegistry) releaseRef(ref *backendRef) {
	if !ref.Release() {
		return
	}

	m.mu.Lock()
	if m.refs[ref.GetBackend()] == ref {
		delete(m.refs, ref.GetBackend())
	}
	m.mu.Unlock()

	_ = ref.GetBackend().Close()
}

// RegisterBackend stores or replaces the backend for service and reports how
// the registry changed.
//
// The displaced backend (the previous one on an endpoint change, or the
// redundant new one on an unchanged-endpoint re-registration) is closed
// after the lock is released, but only once no service name in the registry
// still resolves to it and no outstanding GetBackend lease still holds it:
// displacing one of the names a backend is registered under leaves it open
// for as long as another name still points at it, and a call already
// proxying through it leaves it open until that call finishes. This matters
// because one dialed connection, such as the gateway's own loopback
// backend, is commonly registered under several service names at once.
func (m *BackendRegistry) RegisterBackend(service string, b Backend, kind BackendKind) RegistrationStatus {
	status, evicted := m.registerBackend(service, b, kind)
	if evicted != nil {
		_ = evicted.Close()
	}

	return status
}

// registerBackend performs the registration under the write lock and
// reports the backend the caller must close afterwards, if any.
//
// The renewed branch's displaced value is b itself when the registry holds
// no reference to it yet, since such a b is a freshly dialed connection
// discarded before ever being stored in m.backends. When m.refs already
// tracks b, such as a backend shared across several service names, the
// displaced value is nil instead: b is a connection this registration never
// retained, so closing it would tear down every other entry still
// resolving to it.
func (m *BackendRegistry) registerBackend(service string, b Backend, kind BackendKind) (RegistrationStatus, Backend) {
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.backends[service]
	switch {
	case ok && existing.backend.Endpoint() == b.Endpoint():
		existing.lastSeenAt = now
		m.backends[service] = existing
		if _, tracked := m.refs[b]; tracked {
			return RegistrationRenewed, nil
		}
		return RegistrationRenewed, b
	case ok:
		m.backends[service] = BackendEntry{service: service, backend: b, kind: kind, lastSeenAt: now}
		m.retainLocked(b)
		return RegistrationUpdated, m.releaseLocked(existing.backend)
	default:
		m.backends[service] = BackendEntry{service: service, backend: b, kind: kind, lastSeenAt: now}
		m.retainLocked(b)
		return RegistrationRegistered, nil
	}
}

// retainLocked records a new registry-entry reference to b, creating its
// refcount if no entry or lease points at it yet.
//
// Must be called with m.mu held for writing.
func (m *BackendRegistry) retainLocked(b Backend) {
	ref, ok := m.refs[b]
	if !ok {
		ref = &backendRef{backend: b}
		m.refs[b] = ref
	}
	ref.Retain()
}

// releaseLocked drops one registry-entry reference from b and returns b if
// that was its last reference of any kind, or nil if a lease or another
// entry still holds it.
//
// Must be called with m.mu held for writing. A non-nil result must be
// closed by the caller after the lock is released.
func (m *BackendRegistry) releaseLocked(b Backend) Backend {
	ref, ok := m.refs[b]
	if !ok {
		// Every backend stored in m.backends was retained via
		// retainLocked, so a missing ref here is a bookkeeping bug
		// rather than a state this registry can reach.
		return nil
	}

	if !ref.Release() {
		return nil
	}

	delete(m.refs, b)
	return b
}

// Renew refreshes the last-seen timestamp when service is already registered
// at endpoint, and reports whether it did.
//
// An entry's kind is fixed at registration and never changes here. The
// sweeper acts only on BackendKindExternal, so letting a caller relabel an
// existing entry would let it either arm eviction against a backend the
// gateway shares across its own services, or exempt itself from eviction
// entirely. A same-name registration at a different endpoint falls through
// to registerBackend's same-name branch instead, which is out of scope here.
//
// A false result means the caller must dial a new backend and call
// RegisterBackend (new service or changed endpoint).
func (m *BackendRegistry) Renew(service, endpoint string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.backends[service]
	if ok && entry.backend.Endpoint() == endpoint {
		entry.lastSeenAt = time.Now().UTC()
		m.backends[service] = entry
		return true
	}

	return false
}

// Close closes all backend connections without holding the registry lock.
//
// A backend may be registered under several service names (the shared loopback
// connection).
//
// backend.Close is idempotent, so closing every entry is safe.
func (m *BackendRegistry) Close() error {
	var err error
	for _, entry := range m.takeBackends() {
		err = errors.Join(err, entry.Close())
	}

	return err
}

// EvictStale removes external backends not refreshed since before and
// returns the entries it removed.
//
// Only BackendKindExternal entries are eligible. That is the sole kind that
// heartbeats — builtin and in-process entries are registered once and never
// renewed, so a stale lastSeenAt on them reflects gateway startup, not a dead
// process. It is also the sole kind with a 1:1 connection: builtin and
// in-process entries share one loopback backend across several service keys,
// and closing it here would tear down every other service hosted on that
// backend along with the one that looked stale.
//
// An evicted backend is closed outside the registry lock, and only once
// nothing else still holds it: a GetBackend lease taken before eviction
// keeps the connection open until that call releases it, so a concurrent
// caller already proxying through the backend never sees it close under it.
func (m *BackendRegistry) EvictStale(before time.Time) []BackendEntry {
	evicted, closable := m.evictStale(before)
	for _, b := range closable {
		_ = b.Close()
	}

	return evicted
}

func (m *BackendRegistry) evictStale(before time.Time) ([]BackendEntry, []Backend) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var evicted []BackendEntry
	var closable []Backend
	for service, entry := range m.backends {
		if entry.Kind() == BackendKindExternal && entry.LastSeenAt().Before(before) {
			evicted = append(evicted, entry)
			delete(m.backends, service)
			if b := m.releaseLocked(entry.GetBackend()); b != nil {
				closable = append(closable, b)
			}
		}
	}

	return evicted, closable
}

// takeBackends atomically returns the registered backends and clears the
// registry, including its refcount bookkeeping.
func (m *BackendRegistry) takeBackends() map[string]BackendEntry {
	m.mu.Lock()
	defer m.mu.Unlock()

	backends := m.backends
	m.backends = map[string]BackendEntry{}
	m.refs = map[Backend]*backendRef{}
	return backends
}

// ListBackends returns metadata for all currently registered backends.
func (m *BackendRegistry) ListBackends() []BackendEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	services := make([]BackendEntry, 0, len(m.backends))
	for name, entry := range m.backends {
		entry.service = name
		services = append(services, entry)
	}

	sort.Slice(services, func(i int, j int) bool {
		return services[i].Service() < services[j].Service()
	})

	return services
}
