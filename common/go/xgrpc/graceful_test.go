package xgrpc

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// blockingHealthServer is a fake Health server whose Watch handler blocks on
// the stream context instead of returning, reproducing a server-streaming
// RPC that only ends once the client disconnects.
type blockingHealthServer struct {
	grpc_health_v1.UnimplementedHealthServer
}

func (m *blockingHealthServer) Watch(
	req *grpc_health_v1.HealthCheckRequest,
	stream grpc_health_v1.Health_WatchServer,
) error {
	if err := stream.Send(&grpc_health_v1.HealthCheckResponse{}); err != nil {
		return err
	}

	<-stream.Context().Done()
	return stream.Context().Err()
}

// TestStopGracefully_ReturnsWithinTimeout verifies that StopGracefully
// returns close to the given timeout, and not before it elapses, while a
// client keeps a server-streaming RPC open — reproducing the hang a bare
// GracefulStop causes when a handler goroutine never observes cancellation.
func TestStopGracefully_ReturnsWithinTimeout(t *testing.T) {
	t.Parallel()

	server := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(server, &blockingHealthServer{})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var group errgroup.Group
	group.Go(func() error {
		return server.Serve(listener)
	})

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := grpc_health_v1.NewHealthClient(conn)

	// The stream is deliberately left open: the client never calls
	// CloseSend or cancels its context, matching the reproduction where an
	// open watch stream wedges GracefulStop forever.
	var stream grpc.ServerStreamingClient[grpc_health_v1.HealthCheckResponse]
	require.Eventually(t, func() bool {
		var watchErr error
		stream, watchErr = client.Watch(t.Context(), &grpc_health_v1.HealthCheckRequest{})
		if watchErr != nil {
			return false
		}

		_, watchErr = stream.Recv()
		return watchErr == nil
	}, 5*time.Second, 50*time.Millisecond, "failed to open health watch stream")

	const timeout = 200 * time.Millisecond

	var forceStopCount atomic.Int32

	started := time.Now()
	stopped := make(chan struct{})
	go func() {
		StopGracefully(server, timeout, func() { forceStopCount.Add(1) })
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("StopGracefully did not return within the shutdown grace period")
	}
	elapsed := time.Since(started)

	require.GreaterOrEqual(t, elapsed, timeout, "StopGracefully returned before the grace period elapsed")
	require.Less(t, elapsed, timeout+5*time.Second, "StopGracefully took far longer than the grace period")
	require.EqualValues(t, 1, forceStopCount.Load(), "onForceStop must fire exactly once on the forced path")

	require.NoError(t, group.Wait())
}

// TestStopGracefully_NoForceStopWithinTimeout verifies that onForceStop is
// not called when GracefulStop completes within the grace period.
func TestStopGracefully_NoForceStopWithinTimeout(t *testing.T) {
	t.Parallel()

	server := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(server, &grpc_health_v1.UnimplementedHealthServer{})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var group errgroup.Group
	group.Go(func() error {
		return server.Serve(listener)
	})

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := grpc_health_v1.NewHealthClient(conn)

	// Wait for the server to actually be serving before stopping it:
	// calling StopGracefully before Serve's listener loop has started makes
	// Serve return grpc.ErrServerStopped instead of nil.
	require.Eventually(t, func() bool {
		_, checkErr := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})
		return status.Code(checkErr) == codes.Unimplemented
	}, 5*time.Second, 50*time.Millisecond, "failed to observe the server serving")

	var forceStopCount atomic.Int32

	StopGracefully(server, GracefulStopTimeout, func() { forceStopCount.Add(1) })

	require.EqualValues(t, 0, forceStopCount.Load(), "onForceStop must not fire when the server stops within the grace period")

	require.NoError(t, group.Wait())
}
