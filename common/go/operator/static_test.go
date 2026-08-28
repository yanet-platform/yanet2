package operator_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// fakeGateway accepts registrations, records pipeline updates as the module
// config stand-in, and holds functions the way the real gateway does.
type fakeGateway struct {
	ynpb.UnimplementedGatewayServer

	mu        sync.Mutex
	pipelines []*ynpb.UpdatePipelineRequest
	functions map[string]*ynpb.Function
}

func (m *fakeGateway) Register(
	ctx context.Context,
	req *ynpb.RegisterRequest,
) (*ynpb.RegisterResponse, error) {
	return &ynpb.RegisterResponse{Status: ynpb.RegistrationStatus_REGISTRATION_STATUS_REGISTERED}, nil
}

// pipelineServer records every pipeline update the fake gateway receives.
type pipelineServer struct {
	ynpb.UnimplementedPipelineServiceServer

	gateway *fakeGateway
}

func (m *pipelineServer) Update(
	ctx context.Context,
	req *ynpb.UpdatePipelineRequest,
) (*ynpb.UpdatePipelineResponse, error) {
	m.gateway.mu.Lock()
	defer m.gateway.mu.Unlock()
	m.gateway.pipelines = append(m.gateway.pipelines, proto.Clone(req).(*ynpb.UpdatePipelineRequest))
	return &ynpb.UpdatePipelineResponse{}, nil
}

// functionServer serves the functions the fake gateway holds.
type functionServer struct {
	ynpb.UnimplementedFunctionServiceServer

	gateway *fakeGateway
}

func (m *functionServer) Get(
	ctx context.Context,
	req *ynpb.GetFunctionRequest,
) (*ynpb.GetFunctionResponse, error) {
	m.gateway.mu.Lock()
	defer m.gateway.mu.Unlock()
	function, ok := m.gateway.functions[req.GetId().GetName()]
	if !ok {
		return nil, status.Error(codes.NotFound, "no such function")
	}
	return &ynpb.GetFunctionResponse{Function: function}, nil
}

func (m *functionServer) Update(
	ctx context.Context,
	req *ynpb.UpdateFunctionRequest,
) (*ynpb.UpdateFunctionResponse, error) {
	m.gateway.mu.Lock()
	defer m.gateway.mu.Unlock()
	function := proto.Clone(req.GetFunction()).(*ynpb.Function)
	m.gateway.functions[function.GetId().GetName()] = function
	return &ynpb.UpdateFunctionResponse{}, nil
}

// snapshot returns the pushed pipeline updates and functions under the lock.
func (m *fakeGateway) snapshot() ([]*ynpb.UpdatePipelineRequest, map[string]*ynpb.Function) {
	m.mu.Lock()
	defer m.mu.Unlock()
	functions := map[string]*ynpb.Function{}
	for name, function := range m.functions {
		functions[name] = proto.Clone(function).(*ynpb.Function)
	}
	return append([]*ynpb.UpdatePipelineRequest(nil), m.pipelines...), functions
}

// startFakeGateway serves the fake on a loopback port until the test ends
// and returns its endpoint.
func startFakeGateway(t *testing.T) (*fakeGateway, string) {
	t.Helper()
	gateway := &fakeGateway{functions: map[string]*ynpb.Function{}}
	listener, err := net.Listen("tcp", "[::1]:0")
	require.NoError(t, err)

	server := grpc.NewServer()
	ynpb.RegisterGatewayServer(server, gateway)
	ynpb.RegisterPipelineServiceServer(server, &pipelineServer{gateway: gateway})
	ynpb.RegisterFunctionServiceServer(server, &functionServer{gateway: gateway})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	return gateway, listener.Addr().String()
}

// freeEndpoint reserves a loopback port and hands it back released, so the
// operator's own server can bind it and the test can dial it.
func freeEndpoint(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "[::1]:0")
	require.NoError(t, err)
	endpoint := listener.Addr().String()
	require.NoError(t, listener.Close())
	return endpoint
}

// staticConfig builds an operator config for one gateway with a fast
// reconcile cadence.
func staticConfig(gateway, server string) operator.StaticConfig {
	return operator.StaticConfig{
		Server:   operator.GRPCServerConfig{Endpoint: xcfg.MustNonEmptyString(server)},
		Gateways: []operator.GatewayConfig{{Name: "gw0", Endpoint: xcfg.MustNonEmptyString(gateway)}},
		Register: operator.RegisterConfig{Interval: xcfg.MustNonZero(time.Second)},
		Reconcile: operator.ReconcileConfig{
			Interval:       xcfg.MustNonZero(20 * time.Millisecond),
			InitialBackoff: xcfg.MustNonZero(10 * time.Millisecond),
			MaxBackoff:     xcfg.MustNonZero(50 * time.Millisecond),
		},
	}
}

// pipelineTarget builds a target whose module config stand-in is a pipeline
// update, the one unary method the fake gateway records.
func pipelineTarget(function string) operator.StaticTarget {
	return operator.StaticTarget{
		Method: ynpb.PipelineService_Update_FullMethodName,
		Request: &ynpb.UpdatePipelineRequest{
			Pipeline: &ynpb.Pipeline{Id: &commonpb.PipelineId{Name: "main"}},
		},
		Function: &ynpb.Function{
			Id: &commonpb.FunctionId{Name: function},
			Chains: []*ynpb.FunctionChain{{
				Chain: &ynpb.Chain{
					Name:    "default",
					Modules: []*commonpb.ModuleId{{Type: "forward", Name: "fwd0"}},
				},
				Weight: 1,
			}},
		},
	}
}

// Test_NewStaticModuleOperator_RejectsInvalidTargets verifies that every
// target defect is refused at construction, before any gateway is dialled.
func Test_NewStaticModuleOperator_RejectsInvalidTargets(t *testing.T) {
	cases := []struct {
		name    string
		targets []operator.StaticTarget
		wantErr string
	}{
		{
			name:    "no targets",
			targets: nil,
			wantErr: "at least one target",
		},
		{
			name:    "method without a slash",
			targets: []operator.StaticTarget{{Method: "controlplane.ynpb.v1.PipelineService.Update"}},
			wantErr: "package.Service/Method",
		},
		{
			name: "service not linked",
			targets: []operator.StaticTarget{{
				Method: "modules.lldp.controlplane.lldppb.v1.LLDPService/UpdateConfig",
			}},
			wantErr: "not linked into this binary",
		},
		{
			name:    "unknown method",
			targets: []operator.StaticTarget{{Method: "controlplane.ynpb.v1.PipelineService/Replace"}},
			wantErr: `has no method "Replace"`,
		},
		{
			name:    "streaming method",
			targets: []operator.StaticTarget{{Method: "controlplane.ynpb.v1.ReadinessService/Watch"}},
			wantErr: "is streaming",
		},
		{
			name: "request of another type",
			targets: []operator.StaticTarget{{
				Method:  ynpb.PipelineService_Update_FullMethodName,
				Request: &ynpb.GetPipelineRequest{},
			}},
			wantErr: "takes controlplane.ynpb.v1.UpdatePipelineRequest, not " +
				"controlplane.ynpb.v1.GetPipelineRequest",
		},
		{
			name: "typed nil request",
			targets: []operator.StaticTarget{{
				Method:  ynpb.PipelineService_Update_FullMethodName,
				Request: (*ynpb.UpdatePipelineRequest)(nil),
			}},
			wantErr: "has no request",
		},
		{
			name: "function without a name",
			targets: func() []operator.StaticTarget {
				target := pipelineTarget("fn:one")
				target.Function.Id = nil
				return []operator.StaticTarget{target}
			}(),
			wantErr: "the function has no name",
		},
		{
			name: "function without chains",
			targets: func() []operator.StaticTarget {
				target := pipelineTarget("fn:one")
				target.Function.Chains = nil
				return []operator.StaticTarget{target}
			}(),
			wantErr: `function "fn:one" has no chains`,
		},
		{
			name:    "function declared twice",
			targets: []operator.StaticTarget{pipelineTarget("fn:one"), pipelineTarget("fn:one")},
			wantErr: `function "fn:one" is declared twice`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := staticConfig("[::1]:1", "[::1]:0")
			_, err := operator.NewStaticModuleOperator("test", cfg, tc.targets)

			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// Test_NewStaticModuleOperator_DetachesMessages verifies that a request or
// function mutated by the caller after construction is not what gets pushed.
func Test_NewStaticModuleOperator_DetachesMessages(t *testing.T) {
	target := pipelineTarget("fn:one")
	gateway, endpoint := startFakeGateway(t)
	op, err := operator.NewStaticModuleOperator(
		"test", staticConfig(endpoint, freeEndpoint(t)), []operator.StaticTarget{target},
	)
	require.NoError(t, err)
	target.Function.Chains[0].Weight = 7
	target.Request.(*ynpb.UpdatePipelineRequest).Pipeline.Id.Name = "mutated"

	pipelines, functions := runUntilPushed(t, op, gateway)

	require.Equal(t, uint64(1), functions["fn:one"].GetChains()[0].GetWeight())
	require.Equal(t, "main", pipelines[0].GetPipeline().GetId().GetName())
}

// runUntilPushed runs the operator until the fake gateway holds the module
// config and the function, then stops it and returns what was pushed.
func runUntilPushed(
	t *testing.T,
	op operator.Runnable,
	gateway *fakeGateway,
) ([]*ynpb.UpdatePipelineRequest, map[string]*ynpb.Function) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return op.Run(ctx) })

	require.Eventually(t, func() bool {
		pipelines, functions := gateway.snapshot()
		return len(pipelines) >= 2 && len(functions) == 1
	}, 5*time.Second, 10*time.Millisecond)

	cancel()
	err := group.Wait()
	require.True(t, err == nil || errors.Is(err, context.Canceled), "got %v", err)
	require.NoError(t, op.Close())

	return gateway.snapshot()
}

// Test_StaticModuleOperator_PushesConfigAndFunction verifies that every pass
// sends the request as given and the function reaches the gateway as declared.
func Test_StaticModuleOperator_PushesConfigAndFunction(t *testing.T) {
	gateway, endpoint := startFakeGateway(t)
	target := pipelineTarget("fn:one")
	op, err := operator.NewStaticModuleOperator(
		"test", staticConfig(endpoint, freeEndpoint(t)), []operator.StaticTarget{target},
	)
	require.NoError(t, err)

	pipelines, functions := runUntilPushed(t, op, gateway)

	for _, pushed := range pipelines {
		require.True(t, proto.Equal(target.Request, pushed), "got %v", pushed)
	}
	require.True(t, proto.Equal(target.Function, functions["fn:one"]), "got %v", functions["fn:one"])
}

// Test_StaticModuleOperator_ReadinessUnderInstanceName verifies that readiness
// answers under the name-derived service and turns ready once a pass applied.
func Test_StaticModuleOperator_ReadinessUnderInstanceName(t *testing.T) {
	gateway, endpoint := startFakeGateway(t)
	server := freeEndpoint(t)
	op, err := operator.NewStaticModuleOperator(
		"forward", staticConfig(endpoint, server), []operator.StaticTarget{pipelineTarget("fn:one")},
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return op.Run(ctx) })
	require.Eventually(t, func() bool {
		pipelines, _ := gateway.snapshot()
		return len(pipelines) >= 1
	}, 5*time.Second, 10*time.Millisecond)

	conn, err := grpc.NewClient(server, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	service := operator.ReadinessServiceName("forward")
	require.Equal(t, "operators.forward.operatorpb.v1.ReadinessService", service)
	response := &readinesspb.ReadyResponse{}
	require.Eventually(t, func() bool {
		err := conn.Invoke(ctx, "/"+service+"/Ready", &readinesspb.ReadyRequest{}, response)
		if err != nil {
			return false
		}
		for _, scope := range response.GetScopes() {
			if scope.GetName() == "config:gw0" && scope.GetState() == readinesspb.State_STATE_READY {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "got %v", response)
	for _, scope := range response.GetScopes() {
		interval := scope.GetExpectedObservationInterval().AsDuration()
		require.Equal(t, 20*time.Millisecond, interval, scope.GetName())
	}

	cancel()
	err = group.Wait()
	require.True(t, err == nil || errors.Is(err, context.Canceled), "got %v", err)
	require.NoError(t, op.Close())
}
