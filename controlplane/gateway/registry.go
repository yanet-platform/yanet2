package gateway

import (
	"errors"
	"io"
	"sort"
	"sync"
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

// GetBackend returns the proxy.Backend for this entry.
func (m *BackendEntry) GetBackend() proxy.Backend {
	return m.backend
}

// Close closes the entry's underlying backend.
//
// Only the owning registry closes an entry. An entry handed out by
// ListBackends must not be closed by its receiver.
func (m *BackendEntry) Close() error {
	return m.backend.Close()
}

// BackendRegistry is a registry of backends for Gateway API.
type BackendRegistry struct {
	mu       sync.RWMutex
	backends map[string]BackendEntry
}

// NewBackendRegistry creates a new BackendRegistry.
func NewBackendRegistry() *BackendRegistry {
	return &BackendRegistry{
		backends: map[string]BackendEntry{},
	}
}

// GetBackend returns a backend for the given service.
//
// Service parameter must be in gRPC format, such as "routepb.RouteService".
func (m *BackendRegistry) GetBackend(service string) (proxy.Backend, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.backends[service]
	return entry.backend, ok
}

// RegisterBackend stores or replaces the backend for service and reports how
// the registry changed.
//
// The displaced backend (the previous one on an endpoint change, or the
// redundant new one on an unchanged-endpoint re-registration) is closed after
// the lock is released.
//
// This close-on-displace path applies only to 1:1 external module backends.
// The shared loopback backend is registered once under distinct keys and is
// never displaced, so it is only closed by Close().
func (m *BackendRegistry) RegisterBackend(service string, b Backend, kind BackendKind) RegistrationStatus {
	status, evicted := m.registerBackend(service, b, kind)
	if evicted != nil {
		_ = evicted.Close()
	}

	return status
}

func (m *BackendRegistry) registerBackend(service string, b Backend, kind BackendKind) (RegistrationStatus, Backend) {
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.backends[service]
	switch {
	case ok && existing.backend.Endpoint() == b.Endpoint():
		existing.kind = kind
		existing.lastSeenAt = now
		m.backends[service] = existing
		return RegistrationRenewed, b
	case ok:
		m.backends[service] = BackendEntry{service: service, backend: b, kind: kind, lastSeenAt: now}
		return RegistrationUpdated, existing.backend
	default:
		m.backends[service] = BackendEntry{service: service, backend: b, kind: kind, lastSeenAt: now}
		return RegistrationRegistered, nil
	}
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
func (m *BackendRegistry) EvictStale(before time.Time) []BackendEntry {
	evicted := m.evictStale(before)
	for _, entry := range evicted {
		_ = entry.Close()
	}

	return evicted
}

func (m *BackendRegistry) evictStale(before time.Time) []BackendEntry {
	m.mu.Lock()
	defer m.mu.Unlock()

	var evicted []BackendEntry
	for service, entry := range m.backends {
		if entry.Kind() == BackendKindExternal && entry.LastSeenAt().Before(before) {
			evicted = append(evicted, entry)
			delete(m.backends, service)
		}
	}

	return evicted
}

// takeBackends atomically returns the registered backends and clears the
// registry.
func (m *BackendRegistry) takeBackends() map[string]BackendEntry {
	m.mu.Lock()
	defer m.mu.Unlock()

	backends := m.backends
	m.backends = map[string]BackendEntry{}
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
