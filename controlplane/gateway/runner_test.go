package gateway_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/yanet-platform/yanet2/controlplane/gateway"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// healthService is a Service exposing the standard gRPC health checker
// under a module name, with an endpoint of its own the runner must ignore.
type healthService struct{}

func (m *healthService) Name() string     { return "health-module" }
func (m *healthService) Endpoint() string { return "127.0.0.1:0" }

func (m *healthService) ServicesNames() []string {
	return []string{grpc_health_v1.Health_ServiceDesc.ServiceName}
}

func (m *healthService) RegisterService(server *grpc.Server) {
	grpc_health_v1.RegisterHealthServer(server, health.NewServer())
}

// startServiceRunner runs runner until the test ends, failing the test if
// it does not register within a bounded time or exits with an error.
func startServiceRunner(t *testing.T, runner *gateway.InProcessServiceRunner) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return runner.Run(ctx) })
	t.Cleanup(func() {
		cancel()
		require.NoError(t, group.Wait())
	})

	select {
	case <-runner.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("service runner did not become ready")
	}
}

// Test_InProcessServiceRunner_Run_ServesThroughRegistry verifies that a
// runner answers RPCs through the registry's connection to it.
//
// The services are recorded as in-process backends labeled with the
// gateway's endpoint, the address they are reachable at from outside.
func Test_InProcessServiceRunner_Run_ServesThroughRegistry(t *testing.T) {
	t.Parallel()

	const gatewayEndpoint = "gateway.test:8080"
	serviceName := grpc_health_v1.Health_ServiceDesc.ServiceName

	registry := gateway.NewBackendRegistry()
	t.Cleanup(func() { _ = registry.Close() })

	startServiceRunner(t, gateway.NewInProcessServiceRunner(&healthService{}, registry, gatewayEndpoint))

	entry := getBackendEntry(t, registry, serviceName)
	require.Equal(t, gateway.BackendKindInProcess, entry.Kind())
	require.Equal(t, gatewayEndpoint, entry.Endpoint())

	backend, release, ok := registry.GetBackend(serviceName)
	require.True(t, ok)
	defer release()

	ctx, conn, err := backend.GetConnection(t.Context(), "/"+serviceName+"/Check")
	require.NoError(t, err)

	response, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, response.GetStatus())
}

// Test_InProcessServiceRunner_Run_ShutsDownWithOpenStream verifies that the
// runner returns within a bounded time after its context is canceled.
//
// A client keeps a server-streaming RPC open on the runner's own gRPC server
// meanwhile, reproducing the hang an unattended readiness watch causes on
// shutdown.
func Test_InProcessServiceRunner_Run_ShutsDownWithOpenStream(t *testing.T) {
	t.Parallel()

	registry := gateway.NewBackendRegistry()
	t.Cleanup(func() { _ = registry.Close() })

	runner := gateway.NewInProcessServiceRunner(&blockingReadinessService{}, registry, "gateway.test:8080")

	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return runner.Run(ctx) })

	select {
	case <-runner.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("service runner did not become ready")
	}

	backend, release, ok := registry.GetBackend(ynpbReadinessServiceName)
	require.True(t, ok)
	defer release()

	streamCtx, conn, err := backend.GetConnection(t.Context(), "/"+ynpbReadinessServiceName+"/Watch")
	require.NoError(t, err)

	// The stream is deliberately left open across shutdown.
	_ = watchUntilOpen(t, streamCtx, ynpb.NewReadinessServiceClient(conn))

	cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- group.Wait() }()

	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("service runner did not return within the shutdown grace period")
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

// validateProbeModuleService is a Service that registers the probe service
// and contributes its own unary interceptor.
//
// The interceptor records the status code every call resolves with,
// standing in for a module's own interceptor, such as metrics, that must
// still observe a request the validate interceptor goes on to reject.
type validateProbeModuleService struct {
	probe         *recordingValidateProbeServer
	observedCodes chan codes.Code
}

func (m *validateProbeModuleService) Name() string     { return "validate-probe-module" }
func (m *validateProbeModuleService) Endpoint() string { return "" }

func (m *validateProbeModuleService) ServicesNames() []string {
	return []string{validateProbeServiceName}
}

func (m *validateProbeModuleService) RegisterService(server *grpc.Server) {
	server.RegisterService(&validateProbeServiceDesc, m.probe)
}

func (m *validateProbeModuleService) UnaryServerInterceptors() []grpc.UnaryServerInterceptor {
	return []grpc.UnaryServerInterceptor{
		func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			resp, err := handler(ctx, req)
			select {
			case m.observedCodes <- status.Code(err):
			default:
			}
			return resp, err
		},
	}
}

// Test_InProcessServiceRunner_Run_RejectsInvalidProbeUnary verifies that an
// invalid unary request never reaches the handler.
//
// A module's own unary interceptor still observes that rejection.
func Test_InProcessServiceRunner_Run_RejectsInvalidProbeUnary(t *testing.T) {
	t.Parallel()

	registry := gateway.NewBackendRegistry()
	t.Cleanup(func() { _ = registry.Close() })

	probe := &recordingValidateProbeServer{}
	module := &validateProbeModuleService{probe: probe, observedCodes: make(chan codes.Code, 1)}

	startServiceRunner(t, gateway.NewInProcessServiceRunner(module, registry, "gateway.test:8080"))

	backend, release, ok := registry.GetBackend(validateProbeServiceName)
	require.True(t, ok)
	defer release()

	ctx, conn, err := backend.GetConnection(t.Context(), "/"+validateProbeServiceName+"/Unary")
	require.NoError(t, err)

	invokeErr := conn.Invoke(ctx, "/"+validateProbeServiceName+"/Unary", &wrapperspb.StringValue{}, &emptypb.Empty{})
	require.Equal(t, codes.InvalidArgument, status.Code(invokeErr))
	require.False(t, probe.unaryCalled.Load(), "handler must not run for an invalid probe request")

	select {
	case observed := <-module.observedCodes:
		require.Equal(t, codes.InvalidArgument, observed, "module interceptor must observe the rejected request")
	case <-time.After(5 * time.Second):
		t.Fatal("module interceptor did not observe the request")
	}
}

// Test_InProcessServiceRunner_Run_RejectsInvalidProbeStream verifies that an
// invalid streamed request never reaches the handler.
func Test_InProcessServiceRunner_Run_RejectsInvalidProbeStream(t *testing.T) {
	t.Parallel()

	registry := gateway.NewBackendRegistry()
	t.Cleanup(func() { _ = registry.Close() })

	probe := &recordingValidateProbeServer{}
	module := &validateProbeModuleService{probe: probe, observedCodes: make(chan codes.Code, 1)}

	startServiceRunner(t, gateway.NewInProcessServiceRunner(module, registry, "gateway.test:8080"))

	backend, release, ok := registry.GetBackend(validateProbeServiceName)
	require.True(t, ok)
	defer release()

	ctx, conn, err := backend.GetConnection(t.Context(), "/"+validateProbeServiceName+"/Stream")
	require.NoError(t, err)

	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{StreamName: "Stream", ServerStreams: true}, "/"+validateProbeServiceName+"/Stream")
	require.NoError(t, err)
	require.NoError(t, stream.SendMsg(&wrapperspb.StringValue{}))
	require.NoError(t, stream.CloseSend())

	recvErr := stream.RecvMsg(&emptypb.Empty{})
	require.Equal(t, codes.InvalidArgument, status.Code(recvErr))
	require.False(t, probe.streamCalled.Load(), "handler must not run for an invalid probe request")
}
