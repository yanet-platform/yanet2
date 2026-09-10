package operator

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// blockingReadinessServer is a fake ReadinessServiceServer whose Watch
// handler blocks on the stream context instead of returning, reproducing a
// server-streaming RPC that only ends once the client disconnects.
type blockingReadinessServer struct {
	ynpb.UnimplementedReadinessServiceServer
}

func (m *blockingReadinessServer) Watch(
	req *readinesspb.ReadyRequest,
	stream ynpb.ReadinessService_WatchServer,
) error {
	if err := stream.Send(&readinesspb.ReadyResponse{}); err != nil {
		return err
	}

	<-stream.Context().Done()
	return stream.Context().Err()
}

// countingReadinessServer is a fake ReadinessServiceServer that reports
// how many scopes the request carried, so a caller can tell an accepted
// large request from a refused one.
type countingReadinessServer struct {
	ynpb.UnimplementedReadinessServiceServer
}

func (m *countingReadinessServer) Ready(
	ctx context.Context,
	req *readinesspb.ReadyRequest,
) (*readinesspb.ReadyResponse, error) {
	scopes := make([]*readinesspb.Scope, 0, len(req.GetScopes()))
	for _, scope := range req.GetScopes() {
		scopes = append(scopes, &readinesspb.Scope{Name: scope})
	}

	return &readinesspb.ReadyResponse{Scopes: scopes[:min(len(scopes), 1)]}, nil
}

// Test_GRPCServer_Ready_AcceptsARequestPastTheGRPCDefault verifies that a
// request larger than the four mebibytes gRPC accepts by default reaches
// the handler, so a bulk configuration push is not refused as exhausted.
func Test_GRPCServer_Ready_AcceptsARequestPastTheGRPCDefault(t *testing.T) {
	t.Parallel()

	registrar := func(server *grpc.Server) string {
		ynpb.RegisterReadinessServiceServer(server, &countingReadinessServer{})
		return ynpb.ReadinessService_ServiceDesc.ServiceName
	}

	server, _ := NewGRPCServer(GRPCServerConfig{}, []ServiceRegistrar{registrar}, WithGRPCLog(zap.NewNop()))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var group errgroup.Group
	group.Go(func() error {
		return server.Run(ctx, listener)
	})

	conn, err := grpc.NewClient(listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(maxRequestSize)),
	)
	require.NoError(t, err)
	defer conn.Close()

	// Just past the four mebibytes gRPC would otherwise refuse.
	const scopeCount = 5 * 1024
	const scopeSize = 1024

	scopes := make([]string, scopeCount)
	for idx := range scopes {
		scopes[idx] = strings.Repeat("s", scopeSize)
	}

	response, err := ynpb.NewReadinessServiceClient(conn).Ready(ctx, &readinesspb.ReadyRequest{Scopes: scopes})

	require.NoError(t, err)
	require.Len(t, response.GetScopes(), 1)

	cancel()
	require.NoError(t, group.Wait())
}

// TestGRPCServer_Run_ShutsDownWithOpenStream verifies that GRPCServer.Run
// returns within a bounded time after its context is canceled, even while a
// client keeps a server-streaming ReadinessService.Watch call open,
// reproducing the hang a bare GracefulStop causes for an operator whose
// readiness stream is proxied through the gateway.
func TestGRPCServer_Run_ShutsDownWithOpenStream(t *testing.T) {
	t.Parallel()

	registrar := func(server *grpc.Server) string {
		ynpb.RegisterReadinessServiceServer(server, &blockingReadinessServer{})
		return ynpb.ReadinessService_ServiceDesc.ServiceName
	}

	server, serviceNames := NewGRPCServer(GRPCServerConfig{}, []ServiceRegistrar{registrar}, WithGRPCLog(zap.NewNop()))
	require.Equal(t, []string{ynpb.ReadinessService_ServiceDesc.ServiceName}, serviceNames)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())

	var group errgroup.Group
	group.Go(func() error {
		return server.Run(ctx, listener)
	})

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := ynpb.NewReadinessServiceClient(conn)

	// The stream is deliberately left open: the client never calls
	// CloseSend or cancels its context, matching the reproduction where an
	// open readiness watch wedges GracefulStop forever.
	var stream ynpb.ReadinessService_WatchClient
	require.Eventually(t, func() bool {
		var watchErr error
		stream, watchErr = client.Watch(t.Context(), &readinesspb.ReadyRequest{})
		if watchErr != nil {
			return false
		}

		_, watchErr = stream.Recv()
		return watchErr == nil
	}, 5*time.Second, 50*time.Millisecond, "failed to open readiness watch stream")

	cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- group.Wait() }()

	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("GRPCServer.Run did not return within the shutdown grace period")
	}
}
