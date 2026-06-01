package gateway

import (
	"sort"
	"sync"
	"time"

	"github.com/siderolabs/grpc-proxy/proxy"
	"google.golang.org/grpc"
)

// RegistrationStatus describes how a Register call changed the registry.
type RegistrationStatus int

const (
	RegistrationRegistered RegistrationStatus = iota + 1
	RegistrationRenewed
	RegistrationUpdated
)

// BackendRegistry is a registry of backends for Gateway API.
type BackendRegistry struct {
	mu       sync.RWMutex
	backends map[string]BackendEntry
}

// BackendEntry holds metadata about a single registered backend.
type BackendEntry struct {
	service    string
	backend    proxy.Backend
	conn       *grpc.ClientConn
	endpoint   string
	lastSeenAt time.Time
}

// Service returns the service name of the entry.
func (m *BackendEntry) Service() string {
	return m.service
}

// Endpoint returns the endpoint of the entry.
func (m *BackendEntry) Endpoint() string {
	return m.endpoint
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

// RegisterBackend registers or refreshes the backend for the given service.
//
// dial is invoked, under the registry lock, only when a connection must be
// established: for a newly registered service or an endpoint change. On an
// unchanged endpoint the existing connection is reused and only the last-seen
// time is refreshed; when the endpoint changes the previous connection is
// closed.
func (m *BackendRegistry) RegisterBackend(
	service string,
	endpoint string,
	dial func() (proxy.Backend, *grpc.ClientConn, error),
) (RegistrationStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	existing, ok := m.backends[service]
	if ok && existing.endpoint == endpoint {
		existing.lastSeenAt = now
		m.backends[service] = existing
		return RegistrationRenewed, nil
	}

	backend, conn, err := dial()
	if err != nil {
		return 0, err
	}

	status := RegistrationRegistered
	if ok {
		status = RegistrationUpdated
		if existing.conn != nil {
			_ = existing.conn.Close()
		}
	}

	m.backends[service] = BackendEntry{
		service:    service,
		backend:    backend,
		conn:       conn,
		endpoint:   endpoint,
		lastSeenAt: now,
	}
	return status, nil
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
