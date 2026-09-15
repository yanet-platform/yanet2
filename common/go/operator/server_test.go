package operator

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

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

// validateProbeRequest decodes through an embedded well-known proto type, so
// this package's probe service needs no generated message of its own.
type validateProbeRequest struct {
	*wrapperspb.StringValue
}

func (m *validateProbeRequest) Validate() error {
	if m.GetValue() == "" {
		return errors.New("value is required")
	}
	return nil
}

type validateProbeServer interface {
	Unary(ctx context.Context, req *validateProbeRequest) (*emptypb.Empty, error)
	Stream(req *validateProbeRequest, stream grpc.ServerStreamingServer[emptypb.Empty]) error
}

// recordingValidateProbeServer records whether its handlers ran, so a
// wiring test can assert an invalid probe request never reaches them.
type recordingValidateProbeServer struct {
	unaryCalled  atomic.Bool
	streamCalled atomic.Bool
}

func (m *recordingValidateProbeServer) Unary(ctx context.Context, req *validateProbeRequest) (*emptypb.Empty, error) {
	m.unaryCalled.Store(true)
	return &emptypb.Empty{}, nil
}

func (m *recordingValidateProbeServer) Stream(req *validateProbeRequest, stream grpc.ServerStreamingServer[emptypb.Empty]) error {
	m.streamCalled.Store(true)
	return stream.Send(&emptypb.Empty{})
}

const validateProbeServiceName = "yanet.test.ValidateProbe"

// validateProbeServiceDesc is a hand-written grpc.ServiceDesc mirroring the
// shape protoc-gen-go-grpc emits.
//
// It lets this package drive a real unary and a real server-streaming RPC
// without a compiled proto package of its own.
var validateProbeServiceDesc = grpc.ServiceDesc{
	ServiceName: validateProbeServiceName,
	HandlerType: (*validateProbeServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Unary",
			Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				req := &validateProbeRequest{StringValue: &wrapperspb.StringValue{}}
				if err := dec(req); err != nil {
					return nil, err
				}
				if interceptor == nil {
					return srv.(validateProbeServer).Unary(ctx, req)
				}
				info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/" + validateProbeServiceName + "/Unary"}
				handler := func(ctx context.Context, req any) (any, error) {
					return srv.(validateProbeServer).Unary(ctx, req.(*validateProbeRequest))
				}
				return interceptor(ctx, req, info, handler)
			},
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Stream",
			ServerStreams: true,
			Handler: func(srv any, stream grpc.ServerStream) error {
				req := &validateProbeRequest{StringValue: &wrapperspb.StringValue{}}
				if err := stream.RecvMsg(req); err != nil {
					return err
				}
				return srv.(validateProbeServer).Stream(req, &grpc.GenericServerStream[validateProbeRequest, emptypb.Empty]{ServerStream: stream})
			},
		},
	},
	Metadata: "validate_probe_test",
}

// startValidateProbeServer serves the probe on the operator's real gRPC server
// until the test ends.
func startValidateProbeServer(t *testing.T) (probe *recordingValidateProbeServer, conn *grpc.ClientConn) {
	t.Helper()

	probe = &recordingValidateProbeServer{}
	registrar := func(server *grpc.Server) string {
		server.RegisterService(&validateProbeServiceDesc, probe)
		return validateProbeServiceName
	}

	server, _ := NewGRPCServer(GRPCServerConfig{}, []ServiceRegistrar{registrar}, WithGRPCLog(zap.NewNop()))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return server.Run(ctx, listener) })
	t.Cleanup(func() {
		cancel()
		require.NoError(t, group.Wait())
	})

	conn, err = grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return probe, conn
}

// Test_NewGRPCServer_RejectsInvalidProbeUnary verifies that an invalid unary
// request never reaches the handler.
func Test_NewGRPCServer_RejectsInvalidProbeUnary(t *testing.T) {
	t.Parallel()

	probe, conn := startValidateProbeServer(t)

	invokeErr := conn.Invoke(t.Context(), "/"+validateProbeServiceName+"/Unary", &wrapperspb.StringValue{}, &emptypb.Empty{})

	statusErr, ok := status.FromError(invokeErr)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, statusErr.Code())
	require.Equal(t, "value is required", statusErr.Message())
	require.False(t, probe.unaryCalled.Load(), "handler must not run for an invalid probe request")
}

// Test_NewGRPCServer_RejectsInvalidProbeStream verifies that an invalid
// streamed request never reaches the handler.
func Test_NewGRPCServer_RejectsInvalidProbeStream(t *testing.T) {
	t.Parallel()

	probe, conn := startValidateProbeServer(t)

	stream, err := conn.NewStream(t.Context(), &grpc.StreamDesc{StreamName: "Stream", ServerStreams: true}, "/"+validateProbeServiceName+"/Stream")
	require.NoError(t, err)
	require.NoError(t, stream.SendMsg(&wrapperspb.StringValue{}))
	require.NoError(t, stream.CloseSend())

	recvErr := stream.RecvMsg(&emptypb.Empty{})
	statusErr, ok := status.FromError(recvErr)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, statusErr.Code())
	require.Equal(t, "value is required", statusErr.Message())
	require.False(t, probe.streamCalled.Load(), "handler must not run for an invalid probe request")
}
