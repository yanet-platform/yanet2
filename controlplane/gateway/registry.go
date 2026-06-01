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

// GetBackend returns the proxy.Backend for this entry.
func (m *BackendEntry) GetBackend() proxy.Backend {
	return m.backend
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
// This close-on-displace path applies only to 1:1 out-of-process module
// backends.
// The shared loopback backend is registered once under distinct keys and is
// never displaced, so it is only closed by Close().
func (m *BackendRegistry) RegisterBackend(service string, b Backend) RegistrationStatus {
	status, evicted := m.registerBackend(service, b)
	if evicted != nil {
		_ = evicted.Close()
	}

	return status
}

func (m *BackendRegistry) registerBackend(service string, b Backend) (RegistrationStatus, Backend) {
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.backends[service]
	switch {
	case ok && existing.backend.Endpoint() == b.Endpoint():
		existing.lastSeenAt = now
		m.backends[service] = existing
		return RegistrationRenewed, b
	case ok:
		m.backends[service] = BackendEntry{service: service, backend: b, lastSeenAt: now}
		return RegistrationUpdated, existing.backend
	default:
		m.backends[service] = BackendEntry{service: service, backend: b, lastSeenAt: now}
		return RegistrationRegistered, nil
	}
}

// Renew refreshes the last-seen time when service is already registered at
// endpoint and reports whether it did.
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
		err = errors.Join(err, entry.backend.Close())
	}

	return err
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
