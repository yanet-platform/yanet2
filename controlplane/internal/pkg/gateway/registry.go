package gateway

import (
	"sync"

	"github.com/siderolabs/grpc-proxy/proxy"
)

type BackendRegistry struct {
	mu       sync.RWMutex
	backends map[string]proxy.Backend
}

func NewBackendRegistry() *BackendRegistry {
	return &BackendRegistry{
		backends: map[string]proxy.Backend{},
	}
}

func (r *BackendRegistry) GetBackend(service string) (proxy.Backend, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	backend, ok := r.backends[service]
	return backend, ok
}

func (r *BackendRegistry) RegisterBackend(service string, backend proxy.Backend) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.backends[service] = backend
}
