package l3b_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	controlplane "github.com/yanet-platform/yanet2/modules/l3b/controlplane"
	l3bpb "github.com/yanet-platform/yanet2/modules/l3b/controlplane/l3bpb/v1"
	"github.com/yanet-platform/yanet2/modules/l3b/internal/testutils"
	"github.com/yanet-platform/yanet2/objects/l3b/bindings/go/cl3bobject"
)

type mockBackend struct {
	services           map[string]*l3bpb.VirtualService
	moduleConfigs      map[string]*l3bpb.ModuleConfig
	sessions           []cl3bobject.Session
	getServiceResponse *l3bpb.GetServiceResponse
	createErr          error
	createCallCount    int
}

func newMockBackend() *mockBackend {
	return &mockBackend{
		services:      map[string]*l3bpb.VirtualService{},
		moduleConfigs: map[string]*l3bpb.ModuleConfig{},
	}
}

func (m *mockBackend) CreateService(service *l3bpb.VirtualService) error {
	m.createCallCount++
	if m.createErr != nil {
		return m.createErr
	}
	m.services[service.GetName()] = service
	return nil
}

func (m *mockBackend) UpdateService(service *l3bpb.VirtualService) error {
	if _, ok := m.services[service.GetName()]; !ok {
		return errNotFound
	}
	m.services[service.GetName()] = service
	return nil
}

func (m *mockBackend) DeleteService(name string) error {
	if _, ok := m.services[name]; !ok {
		return errNotFound
	}
	delete(m.services, name)
	return nil
}

func (m *mockBackend) ListServices() []string {
	names := make([]string, 0, len(m.services))
	for name := range m.services {
		names = append(names, name)
	}
	return names
}

func (m *mockBackend) UpdateModuleConfig(config *l3bpb.ModuleConfig) error {
	for _, rule := range config.GetDestinationFilterRules() {
		if _, ok := m.services[rule.GetService()]; !ok {
			return errNotFound
		}
	}
	m.moduleConfigs[config.GetName()] = config
	return nil
}

func (m *mockBackend) ListModuleConfigs() []string {
	names := make([]string, 0, len(m.moduleConfigs))
	for name := range m.moduleConfigs {
		names = append(names, name)
	}
	return names
}

func (m *mockBackend) UpdateRealServerState(service string, realServerIndex uint32, enabled bool) error {
	if _, ok := m.services[service]; !ok {
		return errNotFound
	}
	return nil
}

func (m *mockBackend) ListSessions(
	service string,
	cursor uint64,
	limit uint32,
) ([]cl3bobject.Session, uint64, uint64, error) {
	if _, ok := m.services[service]; !ok {
		return nil, 0, 0, errNotFound
	}
	if m.sessions == nil {
		return nil, 0, 0, nil
	}
	return m.sessions, 0, 0, nil
}

func (m *mockBackend) GetService(service string) (*l3bpb.GetServiceResponse, error) {
	if _, ok := m.services[service]; !ok {
		return nil, errNotFound
	}
	return m.getServiceResponse, nil
}

func (m *mockBackend) UpdateRealServerWeight(service string, realServerIndex uint32, weight uint32) error {
	if _, ok := m.services[service]; !ok {
		return errNotFound
	}
	return nil
}

var errNotFound = status.Error(codes.NotFound, "not found")

func newTestService(t *testing.T) (*controlplane.L3BService, *mockBackend) {
	t.Helper()
	backend := newMockBackend()
	return controlplane.NewL3BService(backend), backend
}

func sampleService(name string) *l3bpb.VirtualService {
	return &l3bpb.VirtualService{
		Name:      name,
		HashMask:  0xff,
		IndexMask: 0x0f,
	}
}

func Test_L3BService_CreateAndListService(t *testing.T) {
	svc, backend := newTestService(t)

	_, err := svc.CreateService(t.Context(), &l3bpb.CreateServiceRequest{
		Service: sampleService("vs0"),
	})
	require.NoError(t, err)

	resp, err := svc.ListServices(t.Context(), &l3bpb.ListServicesRequest{})
	require.NoError(t, err)
	require.Equal(t, []string{"vs0"}, resp.Services)
	require.Len(t, backend.services, 1)
}

// Test_L3BService_CreateService_CapacityValidation verifies interceptor rejection
// before the handler and preservation of direct-backend status by the service.
func Test_L3BService_CreateService_CapacityValidation(t *testing.T) {
	operations := &fakeSharedMemory{}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	require.NoError(t, backend.CreateService(sampleService("existing")))
	service := controlplane.NewL3BService(backend)
	for _, name := range []string{"absent", "existing"} {
		request := &l3bpb.CreateServiceRequest{Service: sampleService(name)}
		request.Service.SessionIndexSize = math.MaxUint32
		called := false
		_, err := xgrpc.ValidateUnaryInterceptor()(
			t.Context(), request, &grpc.UnaryServerInfo{},
			func(ctx context.Context, message any) (any, error) {
				called = true
				return service.CreateService(ctx, message.(*l3bpb.CreateServiceRequest))
			},
		)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
		require.False(t, called)
		_, err = service.CreateService(t.Context(), request)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
		require.Len(t, operations.Tables, 1)
		require.Len(t, operations.Services, 1)
	}
}

func Test_L3BService_UpdateServiceMissing(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.UpdateService(t.Context(), &l3bpb.UpdateServiceRequest{
		Service: sampleService("absent"),
	})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func Test_L3BService_DeleteService(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := t.Context()

	_, err := svc.CreateService(ctx, &l3bpb.CreateServiceRequest{Service: sampleService("vs0")})
	require.NoError(t, err)

	_, err = svc.DeleteService(ctx, &l3bpb.DeleteServiceRequest{Name: "vs0"})
	require.NoError(t, err)

	resp, err := svc.ListServices(ctx, &l3bpb.ListServicesRequest{})
	require.NoError(t, err)
	require.Empty(t, resp.Services)
}

// Test_L3BService_DeleteService_LinkedModules verifies that every accepted link
// blocks deletion without changing published state until the last unlink.
func Test_L3BService_DeleteService_LinkedModules(t *testing.T) {
	backend, agent, harness := newTestBackendFixture(t)
	service := controlplane.NewL3BService(backend)
	descriptor := serviceWithRealServer("vs0")
	descriptor.SessionIndexSize = 16
	_, err := service.CreateService(t.Context(), &l3bpb.CreateServiceRequest{Service: descriptor})
	require.NoError(t, err)
	for _, name := range []string{"cfg0", "cfg1"} {
		_, err := service.UpdateModuleConfig(t.Context(), &l3bpb.UpdateModuleConfigRequest{
			Config: &l3bpb.ModuleConfig{
				Name:                   name,
				DestinationFilterRules: []*l3bpb.DestinationFilterRule{{Service: "vs0"}},
			},
		})
		require.NoError(t, err)
	}
	for _, name := range []string{"cfg0", "cfg1"} {
		before, err := backend.GetService("vs0")
		require.NoError(t, err)
		require.False(t, before.RealServers[0].Enabled)
		modules := agent.DPConfig().CPConfigs()
		generation, err := harness.PublishedExecutionContext(0)
		require.NoError(t, err)
		require.NotZero(t, generation)
		capacity := agent.BlockAllocatorFreeSize()
		response, err := service.DeleteService(t.Context(), &l3bpb.DeleteServiceRequest{Name: "vs0"})
		require.Equal(t, codes.FailedPrecondition, status.Code(err))
		require.Nil(t, response)
		after, err := backend.GetService("vs0")
		require.NoError(t, err)
		require.Equal(t, before, after)
		require.Equal(t, []string{"vs0"}, backend.ListServices())
		require.Equal(t, []string{"cfg0", "cfg1"}, backend.ListModuleConfigs())
		require.ElementsMatch(t, modules, agent.DPConfig().CPConfigs())
		current, err := harness.PublishedExecutionContext(0)
		require.NoError(t, err)
		require.Equal(t, generation, current)
		require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
		_, err = service.UpdateModuleConfig(t.Context(), &l3bpb.UpdateModuleConfigRequest{
			Config: &l3bpb.ModuleConfig{Name: name},
		})
		require.NoError(t, err)
	}
	modules := agent.DPConfig().CPConfigs()
	response, err := service.DeleteService(t.Context(), &l3bpb.DeleteServiceRequest{Name: "vs0"})
	require.NoError(t, err)
	require.True(t, response.Deleted)
	_, err = backend.GetService("vs0")
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Empty(t, backend.ListServices())
	require.Equal(t, []string{"cfg0", "cfg1"}, backend.ListModuleConfigs())
	require.ElementsMatch(t, modules, agent.DPConfig().CPConfigs())
	_, err = testutils.ReadRealRing(agent, "vs0", 1)
	require.Error(t, err)
}

// Test_L3BService_DeleteService_Errors verifies that only reference refusals
// change classification while failed deletions retain owned state and handles.
func Test_L3BService_DeleteService_Errors(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code codes.Code
	}{
		{"wrapped reference refusal", fmt.Errorf("native refusal: %w", ffi.ErrBusy), codes.FailedPrecondition},
		{"wrapped native absence", fmt.Errorf("native absence: %w", ffi.ErrNotFound), codes.Internal},
		{"unclassified failure", errors.New("deletion failed"), codes.Internal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operations := &fakeSharedMemory{}
			backend := controlplane.NewBackendWithSharedMemory(operations)
			require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
			service := controlplane.NewL3BService(backend)
			before, err := backend.GetService("vs0")
			require.NoError(t, err)
			deletions := 0
			operations.Hook = func(operation string) error {
				if operation == "delete-service" {
					deletions++
					return tc.err
				}
				return nil
			}
			response, err := service.DeleteService(t.Context(), &l3bpb.DeleteServiceRequest{Name: "absent"})
			require.Equal(t, codes.NotFound, status.Code(err))
			require.Nil(t, response)
			require.Zero(t, deletions)
			response, err = service.DeleteService(t.Context(), &l3bpb.DeleteServiceRequest{Name: "vs0"})
			require.Equal(t, tc.code, status.Code(err))
			require.Nil(t, response)
			require.Equal(t, 1, deletions)
			after, err := backend.GetService("vs0")
			require.NoError(t, err)
			require.Equal(t, before, after)
			require.Equal(t, []string{"vs0"}, backend.ListServices())
			require.Zero(t, operations.Services[0].FreeCalls.Load())
			require.Zero(t, operations.Tables[0].FreeCalls.Load())
			operations.Hook = nil
			response, err = service.DeleteService(t.Context(), &l3bpb.DeleteServiceRequest{Name: "vs0"})
			require.NoError(t, err)
			require.True(t, response.Deleted)
		})
	}
}

func Test_L3BService_UpdateModuleConfigUnknownService(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.UpdateModuleConfig(t.Context(), &l3bpb.UpdateModuleConfigRequest{
		Config: &l3bpb.ModuleConfig{
			Name: "l3b0",
			DestinationFilterRules: []*l3bpb.DestinationFilterRule{{
				Service: "absent",
			}},
		},
	})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func Test_L3BService_UpdateAndListModuleConfig(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := t.Context()

	_, err := svc.CreateService(ctx, &l3bpb.CreateServiceRequest{Service: sampleService("vs0")})
	require.NoError(t, err)

	_, err = svc.UpdateModuleConfig(ctx, &l3bpb.UpdateModuleConfigRequest{
		Config: &l3bpb.ModuleConfig{
			Name: "l3b0",
			DestinationFilterRules: []*l3bpb.DestinationFilterRule{{
				Service: "vs0",
			}},
		},
	})
	require.NoError(t, err)

	resp, err := svc.ListModuleConfigs(ctx, &l3bpb.ListModuleConfigsRequest{})
	require.NoError(t, err)
	require.Equal(t, []string{"l3b0"}, resp.Configs)
}

func Test_RingFromWeights_WeightedRoundRobin(t *testing.T) {
	// Each server index must appear exactly as many times as its weight.
	ring := controlplane.RingFromWeights([]uint32{3, 1})
	require.Len(t, ring, 4)

	counts := map[uint32]int{}
	for _, index := range ring {
		counts[index]++
	}
	require.Equal(t, 3, counts[0])
	require.Equal(t, 1, counts[1])

	// A heavy server is spread across the ring, not clustered at the start:
	// with weights [3, 1] the interleaved sequence is [0, 0, 1, 0].
	require.Equal(t, []uint32{0, 0, 1, 0}, ring)
}

func Test_RingFromWeights_EqualWeightsRoundRobin(t *testing.T) {
	ring := controlplane.RingFromWeights([]uint32{1, 1, 1})
	require.Equal(t, []uint32{0, 1, 2}, ring)
}

func Test_RingFromWeights_AllZeroIsEmpty(t *testing.T) {
	require.Nil(t, controlplane.RingFromWeights([]uint32{0, 0}))
}

func Test_L3BService_ListSessions(t *testing.T) {
	svc, backend := newTestService(t)

	_, err := svc.CreateService(t.Context(), &l3bpb.CreateServiceRequest{Service: sampleService("vs0")})
	require.NoError(t, err)

	backend.sessions = []cl3bobject.Session{{
		SourceAddress: netip.MustParseAddr("10.0.0.1"),
		SourcePort:    12345,
		RealAddress:   netip.MustParseAddr("172.16.0.10"),
		ExpiresAt:     42,
	}}

	resp, err := svc.ListSessions(t.Context(), &l3bpb.ListSessionsRequest{Service: "vs0"})
	require.NoError(t, err)
	require.Len(t, resp.Sessions, 1)
	require.EqualValues(t, 12345, resp.Sessions[0].SourcePort)
	expectedSource := netip.MustParseAddr("10.0.0.1").As4()
	expectedReal := netip.MustParseAddr("172.16.0.10").As4()
	require.Equal(t, expectedSource[:], resp.Sessions[0].SourceAddress)
	require.Equal(t, expectedReal[:], resp.Sessions[0].RealAddress)
	require.EqualValues(t, 42, resp.Sessions[0].ExpiresAt)
	require.Zero(t, resp.NextCursor)

	_, err = svc.ListSessions(t.Context(), &l3bpb.ListSessionsRequest{Service: "absent"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func Test_L3BService_GetService(t *testing.T) {
	svc, backend := newTestService(t)

	_, err := svc.CreateService(t.Context(), &l3bpb.CreateServiceRequest{Service: sampleService("vs0")})
	require.NoError(t, err)

	backend.getServiceResponse = &l3bpb.GetServiceResponse{
		HashMask:  7,
		IndexMask: 3,
		RealServers: []*l3bpb.RealServerState{{
			DestinationAddress: []byte{172, 16, 0, 10},
			SourceNetwork:      commonpb.NewIPNetworkFrom(xnetip.MustParseNetwork("192.0.2.0/24")),
			Weight:             500,
			Enabled:            true,
		}},
	}

	resp, err := svc.GetService(t.Context(), &l3bpb.GetServiceRequest{Name: "vs0"})
	require.NoError(t, err)
	require.EqualValues(t, 7, resp.HashMask)
	require.Len(t, resp.RealServers, 1)
	require.Equal(t, []byte{172, 16, 0, 10}, resp.RealServers[0].DestinationAddress)
	require.EqualValues(t, 500, resp.RealServers[0].Weight)
	require.True(t, resp.RealServers[0].Enabled)
	require.NotNil(t, resp.RealServers[0].SourceNetwork)

	_, err = svc.GetService(t.Context(), &l3bpb.GetServiceRequest{Name: "absent"})
	require.Equal(t, codes.NotFound, status.Code(err))
}
