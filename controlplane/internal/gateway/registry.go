package gateway

import (
	"sync"

	"github.com/siderolabs/grpc-proxy/proxy"
)

// BackendRegistry is a registry of backends for Gateway API.
type BackendRegistry struct {
	mu       sync.RWMutex
	backends map[string]proxy.Backend
}

// NewBackendRegistry creates a new BackendRegistry.
func NewBackendRegistry() *BackendRegistry {
	return &BackendRegistry{
		backends: map[string]proxy.Backend{},
	}
}

// GetBackend returns a backend for the given service.
//
// Service parameter must be in gRPC format, such as "routepb.RouteService".
func (r *BackendRegistry) GetBackend(service string) (proxy.Backend, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	backend, ok := r.backends[service]
	return backend, ok
}

// RegisterBackend registers a backend for the given service.
func (r *BackendRegistry) RegisterBackend(service string, backend proxy.Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.backends[service] = backend
}
