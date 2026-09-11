package l3b_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	controlplane "github.com/yanet-platform/yanet2/modules/l3b/controlplane"
	l3bpb "github.com/yanet-platform/yanet2/modules/l3b/controlplane/l3bpb/v1"
	cl3bobject "github.com/yanet-platform/yanet2/objects/l3b/bindings/go/cl3bobject"
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

func Test_L3BService_CreateServiceEmptyName(t *testing.T) {
	svc, _ := newTestService(t)

	_, err := svc.CreateService(t.Context(), &l3bpb.CreateServiceRequest{
		Service: sampleService(""),
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
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
			SourceNetwork:      &filterpb.IPNet{Addr: []byte{192, 0, 2, 0}, Mask: []byte{255, 255, 255, 0}},
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
