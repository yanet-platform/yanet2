package gateway

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/siderolabs/grpc-proxy/proxy"
	"github.com/yanet-platform/yanet2/controlplane/ynpb"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

// RegisterModule registers all services provided by the module
// and returns a listener where the module's services are registered.
func RegisterModule(
	ctx context.Context,
	gatewayEndpoint string,
	listener net.Listener,
	serviceNames []string,
	log *zap.SugaredLogger,
) error {
	log = log.With("name", "gateway")

	gatewayConn, err := grpc.NewClient(
		gatewayEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize gateway gRPC client: %w", err)
	}

	client := ynpb.NewGatewayClient(gatewayConn)

	wg, ctx := errgroup.WithContext(ctx)
	for _, serviceName := range serviceNames {
		serviceName := serviceName
		req := &ynpb.RegisterRequest{
			Name:     serviceName,
			Endpoint: listener.Addr().String(),
		}

		wg.Go(func() error {
			for {
				if _, err := client.Register(ctx, req); err == nil {
					log.Infof("successfully registered %q in the Gateway API", serviceName)
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				log.Warnf("failed to register %q in the Gateway API: %v", serviceName, err)
				// TODO: exponential backoff should fit better here.
				time.Sleep(1 * time.Second)
			}
		})
	}

	return wg.Wait()
}
