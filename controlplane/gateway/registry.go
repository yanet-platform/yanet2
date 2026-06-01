package gateway

import (
	"errors"
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

// Conn is a backend connection tracked by the registry.
//
// Target identifies the backend endpoint for change detection; Close releases
// the connection.
type Conn interface {
	Target() string
	Close() error
}

// BackendRegistry is a registry of backends for Gateway API.
type BackendRegistry struct {
	mu       sync.RWMutex
	backends map[string]BackendEntry
}

// BackendEntry holds metadata about a single registered backend.
type BackendEntry struct {
	service    string
	backend    proxy.Backend
	conn       Conn
	lastSeenAt time.Time
}

// Service returns the service name of the entry.
func (m *BackendEntry) Service() string {
	return m.service
}

// Endpoint returns the endpoint of the entry.
func (m *BackendEntry) Endpoint() string {
	return m.conn.Target()
}

// LastSeenAt returns the time the entry was last registered.
func (m *BackendEntry) LastSeenAt() time.Time {
	return m.lastSeenAt
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
	backend := entry.backend
	return backend, ok
}

// RegisterBackend registers a backend connection for the given service.
//
// On a same-target re-registration the new conn is closed and the existing
// entry is retained. On an endpoint change the previous conn is closed and the
// new entry replaces it. The obsolete connection is closed after the lock is
// released.
func (m *BackendRegistry) RegisterBackend(
	service string,
	backend proxy.Backend,
	conn Conn,
) RegistrationStatus {
	status, obsolete := m.registerBackend(service, backend, conn)
	if obsolete != nil {
		_ = obsolete.Close()
	}
	return status
}

func (m *BackendRegistry) registerBackend(
	service string,
	backend proxy.Backend,
	conn Conn,
) (RegistrationStatus, Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	existing, ok := m.backends[service]
	switch {
	case ok && existing.conn.Target() == conn.Target():
		existing.lastSeenAt = now
		m.backends[service] = existing
		return RegistrationRenewed, conn
	case ok:
		m.backends[service] = BackendEntry{service: service, backend: backend, conn: conn, lastSeenAt: now}
		return RegistrationUpdated, existing.conn
	default:
		m.backends[service] = BackendEntry{service: service, backend: backend, conn: conn, lastSeenAt: now}
		return RegistrationRegistered, nil
	}
}

// Close closes all tracked backend connections and clears the registry.
func (m *BackendRegistry) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var err error
	for _, entry := range m.backends {
		err = errors.Join(err, entry.conn.Close())
	}
	m.backends = map[string]BackendEntry{}
	return err
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
