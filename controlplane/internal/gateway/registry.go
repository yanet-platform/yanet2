package gateway

import (
	"sort"
	"sync"
	"time"

	"github.com/siderolabs/grpc-proxy/proxy"
)

// BackendRegistry is a registry of backends for Gateway API.
type BackendRegistry struct {
	mu       sync.RWMutex
	backends map[string]BackendEntry
}

type BackendEntry struct {
	service    string
	backend    proxy.Backend
	endpoint   string
	lastSeenAt time.Time
}

func (m *BackendEntry) Service() string {
	return m.service
}

func (m *BackendEntry) Endpoint() string {
	return m.endpoint
}

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

// RegisterBackend registers a backend for the given service.
func (m *BackendRegistry) RegisterBackend(
	service string,
	backend proxy.Backend,
	endpoint string,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.backends[service] = BackendEntry{
		service:    service,
		backend:    backend,
		endpoint:   endpoint,
		lastSeenAt: time.Now().UTC(),
	}
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
