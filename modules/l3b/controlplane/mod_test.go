package l3b_test

import (
	"context"
	"io/fs"
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
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	l3b "github.com/yanet-platform/yanet2/modules/l3b/controlplane"
	l3bpb "github.com/yanet-platform/yanet2/modules/l3b/controlplane/l3bpb/v1"
)

// Test_NewL3BModule_MissingMemoryFile verifies that attachment failure reports
// the configured shared-memory path without returning a module.
func Test_NewL3BModule_MissingMemoryFile(t *testing.T) {
	memoryPath := filepath.Join(t.TempDir(), "missing-shared-memory")
	config := l3b.DefaultConfig()
	config.InstanceID = xcfg.NewRequired(uint32(0))
	config.MemoryPath = xcfg.MustNonEmptyString(memoryPath)

	module, err := l3b.NewL3BModule(config)
	if module != nil {
		t.Cleanup(func() { require.NoError(t, module.Close()) })
	}
	require.Nil(t, module)
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.ErrorContains(t, err, memoryPath)
}

// Test_L3BModule_NontrafficAdmission verifies that direct construction exposes
// exactly one service through the Gateway and releases its mapping on shutdown.
func Test_L3BModule_NontrafficAdmission(t *testing.T) {
	config := l3b.DefaultConfig()
	config.MemoryPath = xcfg.MustNonEmptyString(testshm.NewStorage(t))
	config.InstanceID = xcfg.NewRequired(uint32(0))
	module, err := l3b.NewL3BModule(config)
	require.NoError(t, err)
	ownedByGateway := false
	t.Cleanup(func() {
		if !ownedByGateway {
			require.NoError(t, module.Close())
		}
	})

	serviceName := l3bpb.L3BService_ServiceDesc.ServiceName
	require.Equal(t, "l3b", module.Name())
	require.Equal(t, []string{serviceName}, module.ServicesNames())
	server := grpc.NewServer()
	module.RegisterService(server)
	require.Len(t, server.GetServiceInfo(), 1)
	require.Contains(t, server.GetServiceInfo(), serviceName)
	server.Stop()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
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
	proxy, err := gateway.NewGateway(gateway.DefaultConfig(),
		gateway.WithListener(listener), gateway.WithService(module), gateway.WithLog(log),
	)
	require.NoError(t, err)
	ownedByGateway = true
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	var group errgroup.Group
	joined := make(chan struct{})
	group.Go(func() error {
		defer close(joined)
		return proxy.Run(ctx)
	})
	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
			runErr := group.Wait()
			closeErr := proxy.Close()
			require.NoError(t, runErr)
			require.NoError(t, closeErr)
		case <-time.After(10 * time.Second):
			t.Fatal("gateway did not join before shared-memory cleanup")
		}
	})
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("gateway did not become ready")
	}

	connection, err := grpc.NewClient(listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	discovery, err := ynpb.NewGatewayClient(connection).ListServices(ctx,
		&ynpb.ListServicesRequest{}, grpc.WaitForReady(true),
	)
	require.NoError(t, err)
	var matches int
	for _, service := range discovery.GetServices() {
		if service.GetBackend().GetName() == serviceName {
			matches++
			require.Equal(t, ynpb.BackendKind_BACKEND_KIND_IN_PROCESS, service.GetKind())
		}
	}
	require.Equal(t, 1, matches)
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
}

// Test_L3BModule_NontrafficConstructorErrors verifies that a failed attachment
// returns no module and leaves no mapping of the test-owned storage.
func Test_L3BModule_NontrafficConstructorErrors(t *testing.T) {
	for _, tc := range []string{"missing memory file", "invalid instance"} {
		t.Run(tc, func(t *testing.T) {
			config := l3b.DefaultConfig()
			config.InstanceID = xcfg.NewRequired(uint32(0))
			if tc == "missing memory file" {
				config.MemoryPath = xcfg.MustNonEmptyString(filepath.Join(t.TempDir(), "missing"))
			} else {
				config.MemoryPath = xcfg.MustNonEmptyString(testshm.NewStorage(t))
				config.InstanceID = xcfg.NewRequired(uint32(1))
			}
			module, err := l3b.NewL3BModule(config)
			if module != nil {
				t.Cleanup(func() { require.NoError(t, module.Close()) })
			}
			require.Nil(t, module)
			require.Error(t, err)
			if tc == "missing memory file" {
				require.ErrorIs(t, err, os.ErrNotExist)
			} else {
				require.ErrorContains(t, err, "failed to attach agent")
			}
		})
	}
}
