package yncp_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	testshm "github.com/yanet-platform/yanet2/common/go/testutils/shm"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
	"github.com/yanet-platform/yanet2/controlplane/yncp"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	l3b "github.com/yanet-platform/yanet2/modules/l3b/controlplane"
	l3bpb "github.com/yanet-platform/yanet2/modules/l3b/controlplane/l3bpb/v1"
)

// newL3BDirectorConfig returns an empty, ready runtime with only L3B configured.
func newL3BDirectorConfig(t *testing.T) *yncp.Config {
	t.Helper()
	config := yncp.DefaultConfig()
	config.MemoryPath = testshm.NewStorage(t)
	config.Gateway.InstanceID = xcfg.NewRequired(uint32(0))
	config.Gateway.Server.Endpoint = "127.0.0.1:0"
	config.Gateway.Server.HTTPEndpoint = ""
	module := l3b.DefaultConfig()
	module.InstanceID = xcfg.NewRequired(uint32(0))
	module.MemoryPath = xcfg.MustNonEmptyString(config.MemoryPath)
	config.Modules.L3B = xcfg.NewOptional(*module)
	return config
}

// Test_Director_L3BNontrafficAdmission verifies that the production constructor
// admits one L3B backend and joins shutdown before releasing shared memory.
func Test_Director_L3BNontrafficAdmission(t *testing.T) {
	for _, tc := range []string{"empty read-only runtime", "canceled before startup", "occupied listener"} {
		t.Run(tc, func(t *testing.T) {
			config := newL3BDirectorConfig(t)
			if tc == "occupied listener" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, listener.Close()) })
				config.Gateway.Server.Endpoint = listener.Addr().String()
			}
			ready := make(chan struct{}, 1)
			core, logs := observer.New(zap.InfoLevel)
			log := zap.New(core, zap.Hooks(func(entry zapcore.Entry) error {
				if entry.Message == "all built-in modules ready" {
					select {
					case ready <- struct{}{}:
					default:
					}
				}
				return nil
			}))
			director, err := yncp.NewDirector(config, yncp.WithLog(log))
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			if tc == "canceled before startup" {
				cancel()
			}
			var group errgroup.Group
			joined := make(chan struct{})
			group.Go(func() error {
				defer close(joined)
				return director.Run(ctx)
			})
			t.Cleanup(func() {
				cancel()
				select {
				case <-joined:
					runErr := group.Wait()
					closeErr := director.Close()
					require.NoError(t, closeErr)
					if tc == "occupied listener" {
						require.ErrorContains(t, runErr, "failed to initialize gRPC listener")
					} else {
						require.NoError(t, runErr)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("director did not join before shared-memory cleanup")
				}
			})
			if tc != "empty read-only runtime" {
				select {
				case <-joined:
				case <-time.After(5 * time.Second):
					t.Fatal("startup did not terminate without cleanup cancellation")
				}
				return
			}
			select {
			case <-ready:
			case <-ctx.Done():
				t.Fatal("director did not become ready")
			}
			exposed := logs.FilterMessage("exposing gRPC gateway").All()
			require.Len(t, exposed, 1)
			address, ok := exposed[0].ContextMap()["addr"].(string)
			require.True(t, ok)
			connection, err := grpc.NewClient(address,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, connection.Close()) })
			discovery, err := ynpb.NewGatewayClient(connection).ListServices(ctx,
				&ynpb.ListServicesRequest{}, grpc.WaitForReady(true),
			)
			require.NoError(t, err)
			serviceName := l3bpb.L3BService_ServiceDesc.ServiceName
			var modules []string
			for _, service := range discovery.GetServices() {
				if service.GetKind() == ynpb.BackendKind_BACKEND_KIND_IN_PROCESS {
					modules = append(modules, service.GetBackend().GetName())
				}
			}
			require.Equal(t, []string{serviceName}, modules)
			require.Equal(t, 1, logs.FilterMessage("registered service in registry").
				FilterField(zap.String("service", serviceName)).Len())

			client := l3bpb.NewL3BServiceClient(connection)
			services, err := client.ListServices(ctx, &l3bpb.ListServicesRequest{})
			require.NoError(t, err)
			require.Empty(t, services.GetServices())
			configs, err := client.ListModuleConfigs(ctx, &l3bpb.ListModuleConfigsRequest{})
			require.NoError(t, err)
			require.Empty(t, configs.GetConfigs())
			_, err = client.GetService(ctx, &l3bpb.GetServiceRequest{Name: "missing"})
			require.Equal(t, codes.NotFound, status.Code(err))
			_, err = client.ListSessions(ctx, &l3bpb.ListSessionsRequest{Service: "missing", Limit: 1})
			require.Equal(t, codes.NotFound, status.Code(err))
		})
	}
}

// Test_Director_L3BNontrafficConstructorError verifies that the production
// route propagates L3B construction failures and releases its initial mapping.
func Test_Director_L3BNontrafficConstructorError(t *testing.T) {
	config := newL3BDirectorConfig(t)
	config.Modules.L3B.Unwrap().MemoryPath = xcfg.MustNonEmptyString(filepath.Join(t.TempDir(), "missing"))
	director, err := yncp.NewDirector(config)
	if director != nil {
		t.Cleanup(func() { require.NoError(t, director.Close()) })
	}
	require.Nil(t, director)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, "failed to initialize bundle")
	require.ErrorContains(t, err, "l3b module")
}

// Test_Director_L3BReleasedOnGatewayFailure verifies that rejected Gateway
// construction releases an already constructed L3B service's mapping.
func Test_Director_L3BReleasedOnGatewayFailure(t *testing.T) {
	config := newL3BDirectorConfig(t)
	missing := xcfg.MustNonEmptyString(filepath.Join(t.TempDir(), "missing"))
	config.Gateway.Server.TLS = &gateway.TLSConfig{CertFile: missing, KeyFile: missing}
	director, err := yncp.NewDirector(config)
	if director != nil {
		t.Cleanup(func() { require.NoError(t, director.Close()) })
	}
	require.Nil(t, director)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, "failed to create gateway")
}
