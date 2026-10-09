package l3b_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/l3b/bindings/go/cl3b"
	controlplane "github.com/yanet-platform/yanet2/modules/l3b/controlplane"
	l3bpb "github.com/yanet-platform/yanet2/modules/l3b/controlplane/l3bpb/v1"
	cl3bobject "github.com/yanet-platform/yanet2/objects/l3b/bindings/go/cl3bobject"
)

var errFakeRing = errors.New("ring update failed")

// waitForBackendSignal waits for a channel barrier and fails on a deadlock.
func waitForBackendSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("backend did not reach the channel barrier")
	}
}

type fakeSharedMemory struct {
	hook     func(string) error
	services []*fakeVirtualService
	tables   []*fakeSessionTable
}

func (m *fakeSharedMemory) call(operation string) error {
	if m.hook != nil {
		return m.hook(operation)
	}
	return nil
}

func (m *fakeSharedMemory) WorkerCount() uint32 { return 1 }

func (m *fakeSharedMemory) CreateSessionTable(name string, workers uint16, size uint32) (controlplane.SessionTableHandle, error) {
	if err := m.call("create-table"); err != nil {
		return nil, err
	}
	table := &fakeSessionTable{operations: m}
	m.tables = append(m.tables, table)
	return table, nil
}

func (m *fakeSharedMemory) CreateVirtualService(name string, config cl3bobject.VirtualServiceConfig, table controlplane.SessionTableHandle) (controlplane.VirtualServiceHandle, error) {
	if err := m.call("create-service"); err != nil {
		return nil, err
	}
	service := &fakeVirtualService{operations: m}
	m.services = append(m.services, service)
	return service, nil
}

func (m *fakeSharedMemory) DeleteVirtualService(name string) error {
	return m.call("delete-service")
}

func (m *fakeSharedMemory) NewModuleConfig(name string) (controlplane.ModuleHandle, error) {
	if err := m.call("create-module"); err != nil {
		return nil, err
	}
	return &fakeModule{operations: m}, nil
}

func (m *fakeSharedMemory) UpdateModules(modules []ffi.ModuleConfig) error {
	return m.call("publish-module")
}

type fakeVirtualService struct {
	operations *fakeSharedMemory
	freeCalls  atomic.Int32
	refusals   atomic.Int32
}

func (m *fakeVirtualService) Free() error {
	if err := m.operations.call("free-service"); err != nil {
		return err
	}
	m.freeCalls.Add(1)
	if m.refusals.Load() > 0 {
		m.refusals.Add(-1)
		return ffi.ErrStillReferenced
	}
	return nil
}

func (m *fakeVirtualService) Publish() error { return m.operations.call("publish-service") }
func (m *fakeVirtualService) UpdateRing(indexes []uint32) error {
	return m.operations.call("update-ring")
}
func (m *fakeVirtualService) SetRealServerState(index uint32, enabled bool) error {
	return m.operations.call("set-state")
}
func (m *fakeVirtualService) ReadSessions(cursor uint64, limit uint32) ([]cl3bobject.Session, uint64, uint64, error) {
	return nil, 0, 0, m.operations.call("read-sessions")
}

type fakeSessionTable struct {
	operations *fakeSharedMemory
	freeCalls  atomic.Int32
}

func (m *fakeSessionTable) Free() error {
	if err := m.operations.call("free-table"); err != nil {
		return err
	}
	m.freeCalls.Add(1)
	return nil
}

type fakeModule struct{ operations *fakeSharedMemory }

func (m *fakeModule) Free() error { return m.operations.call("free-module") }
func (m *fakeModule) Update(rules []cl3b.DestinationFilterRule) error {
	return m.operations.call("update-module")
}
func (m *fakeModule) AsFFIModule() ffi.ModuleConfig { return ffi.ModuleConfig{} }

// newTestBackend returns a shared-memory backend over a harness with the l3b
// module and its objects loaded.
func newTestBackend(t *testing.T) controlplane.Backend {
	return controlplane.NewBackend(newTestAgent(t))
}

// newTestAgent returns a real shared-memory agent owned by the test harness.
func newTestAgent(t *testing.T) *ffi.Agent {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(64 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   1,
		Modules:       []string{"l3b"},
		ObjectsToLoad: []string{"l3b_virtual_service", "l3b_session_table"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	agent, err := harness.SharedMemory().AgentAttach("l3b-backend-test", 0, 16*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	return agent
}

type stalledPublishOps struct {
	controlplane.SharedMemoryOps
	entered chan struct{}
	release chan struct{}
}

func (m *stalledPublishOps) CreateVirtualService(name string, config cl3bobject.VirtualServiceConfig, table controlplane.SessionTableHandle) (controlplane.VirtualServiceHandle, error) {
	service, err := m.SharedMemoryOps.CreateVirtualService(name, config, table)
	if err != nil {
		return nil, err
	}
	return &stalledPublishService{VirtualServiceHandle: service, operations: m}, nil
}

type stalledPublishService struct {
	controlplane.VirtualServiceHandle
	operations *stalledPublishOps
}

func (m *stalledPublishService) Publish() error {
	if m.operations.entered != nil {
		close(m.operations.entered)
		<-m.operations.release
	}
	return m.VirtualServiceHandle.Publish()
}

// Test_Backend_RealSessions_DuringReplacement verifies that a C-backed
// session read finishes while a replacement waits before shared-memory publish.
func Test_Backend_RealSessions_DuringReplacement(t *testing.T) {
	operations := &stalledPublishOps{SharedMemoryOps: controlplane.NewSharedMemoryOps(newTestAgent(t))}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
	_, _, _, err := backend.ListSessions("vs0", 0, 1)
	require.NoError(t, err)
	operations.entered = make(chan struct{})
	operations.release = make(chan struct{})
	mutation := make(chan error, 1)
	go func() { mutation <- backend.UpdateService(serviceWithRealServer("vs0")) }()
	waitForBackendSignal(t, operations.entered)
	read := make(chan error, 1)
	go func() {
		_, _, _, err := backend.ListSessions("vs0", 0, 1)
		read <- err
	}()
	select {
	case err := <-read:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		close(operations.release)
		t.Fatal("C-backed session read waited for replacement")
	}
	close(operations.release)
	require.NoError(t, <-mutation)
}

// Test_Backend_UpdateRealServer_IndexOutOfRange verifies that a real server
// index past the service's real servers is reported as InvalidArgument.
func Test_Backend_UpdateRealServer_IndexOutOfRange(t *testing.T) {
	backend := newTestBackend(t)
	require.NoError(t, backend.CreateService(sampleService("vs0")))

	err := backend.UpdateRealServerState("vs0", 0, false)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	err = backend.UpdateRealServerWeight("vs0", 0, 1)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// Test_Backend_CreateService_FilterErrorNotNested verifies that an invalid
// source filter network is reported as InvalidArgument without a nested status.
func Test_Backend_CreateService_FilterErrorNotNested(t *testing.T) {
	backend := newTestBackend(t)
	service := sampleService("vs0")
	service.SourceFilterRules = []*l3bpb.SourceFilterRule{{
		Net6S: []*commonpb.IPv6Network{commonpb.NewIPv6NetworkFrom6(xnetip.MustParseNetwork6("2001:db8::/ffff:0:ffff::"))},
	}}

	err := backend.CreateService(service)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.NotContains(t, status.Convert(err).Message(), "rpc error")
}

// serviceWithRealServer returns a service with one enabled IPv4 destination.
func serviceWithRealServer(name string) *l3bpb.VirtualService {
	service := sampleService(name)
	service.RealServers = []*l3bpb.RealServer{{
		DestinationAddress: []byte{192, 0, 2, 1},
		SourceNetwork:      commonpb.NewIPNetworkFrom(xnetip.MustParseNetwork("198.51.100.0/24")),
	}}
	return service
}

// Test_Backend_Reads_DuringStalledMutations verifies that all four read
// methods finish against published state while shared-memory work is stalled.
func Test_Backend_Reads_DuringStalledMutations(t *testing.T) {
	cases := []struct {
		name         string
		operation    string
		expectedMask uint32
		mutate       func(controlplane.Backend) error
	}{
		{"create allocation", "create-table", 0xff, func(backend controlplane.Backend) error {
			return backend.CreateService(sampleService("vs1"))
		}},
		{"replacement publication", "publish-service", 0xff, func(backend controlplane.Backend) error {
			replacement := serviceWithRealServer("vs0")
			replacement.HashMask = 0x55
			return backend.UpdateService(replacement)
		}},
		{"module publication", "publish-module", 0xff, func(backend controlplane.Backend) error {
			return backend.UpdateModuleConfig(&l3bpb.ModuleConfig{Name: "cfg1"})
		}},
		{"deletion", "delete-service", 0xff, func(backend controlplane.Backend) error {
			return backend.DeleteService("vs0")
		}},
		{"state update", "set-state", 0xff, func(backend controlplane.Backend) error {
			return backend.UpdateRealServerState("vs0", 0, false)
		}},
		{"ring rebuild", "update-ring", 0xff, func(backend controlplane.Backend) error {
			return backend.UpdateRealServerWeight("vs0", 0, 2)
		}},
		{"retired free", "free-service", 0x55, func(backend controlplane.Backend) error {
			replacement := serviceWithRealServer("vs0")
			replacement.HashMask = 0x55
			return backend.UpdateService(replacement)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			operations := &fakeSharedMemory{}
			backend := controlplane.NewBackendWithSharedMemory(operations)
			require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
			require.NoError(t, backend.UpdateModuleConfig(&l3bpb.ModuleConfig{Name: "cfg0"}))

			entered := make(chan struct{})
			release := make(chan struct{})
			operations.hook = func(operation string) error {
				if operation == tc.operation {
					close(entered)
					<-release
				}
				return nil
			}
			mutation := make(chan error, 1)
			go func() { mutation <- tc.mutate(backend) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("mutation did not enter the requested phase")
			}

			reads := make(chan error, 1)
			go func() {
				if names := backend.ListServices(); len(names) != 1 || names[0] != "vs0" {
					reads <- errors.New("service list changed before publication")
					return
				}
				if names := backend.ListModuleConfigs(); len(names) != 1 || names[0] != "cfg0" {
					reads <- errors.New("module list changed before publication")
					return
				}
				response, err := backend.GetService("vs0")
				if err != nil {
					reads <- err
					return
				}
				if response.HashMask != tc.expectedMask || response.RealServers[0].Weight != 1 || !response.RealServers[0].Enabled {
					reads <- errors.New("inspection observed an unfinished mutation")
					return
				}
				_, _, _, err = backend.ListSessions("vs0", 0, 0)
				reads <- err
			}()
			select {
			case err := <-reads:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("read blocked behind shared-memory work")
			}
			close(release)
			require.NoError(t, <-mutation)
		})
	}
}

// Test_Backend_SessionRead_RetainsBothHandles verifies that deletion cannot
// free either object reached by a reader admitted before replacement.
func Test_Backend_SessionRead_RetainsBothHandles(t *testing.T) {
	operations := &fakeSharedMemory{}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
	oldService, table := operations.services[0], operations.tables[0]
	oldService.refusals.Store(1)

	entered := make(chan struct{})
	release := make(chan struct{})
	operations.hook = func(operation string) error {
		if operation == "read-sessions" {
			close(entered)
			<-release
		}
		return nil
	}
	read := make(chan error, 1)
	go func() {
		_, _, _, err := backend.ListSessions("vs0", 0, 1)
		read <- err
	}()
	waitForBackendSignal(t, entered)
	require.NoError(t, backend.UpdateService(serviceWithRealServer("vs0")))
	require.NoError(t, backend.DeleteService("vs0"))
	require.Empty(t, backend.ListServices())
	require.Equal(t, int32(0), oldService.freeCalls.Load())
	require.Equal(t, int32(0), table.freeCalls.Load())
	close(release)
	require.NoError(t, <-read)
	require.Equal(t, int32(0), oldService.freeCalls.Load())
	require.Equal(t, int32(0), table.freeCalls.Load())
	require.NoError(t, backend.CreateService(sampleService("vs1")))
	require.Equal(t, int32(1), oldService.freeCalls.Load())
	require.Equal(t, int32(1), table.freeCalls.Load())
	require.NoError(t, backend.CreateService(sampleService("vs2")))
	require.Equal(t, int32(2), oldService.freeCalls.Load())
	require.Equal(t, int32(1), table.freeCalls.Load())
}

// Test_Backend_OverlappingSessionReaders_RetainSharedTable verifies that
// replacement and deletion retain the table through both service generations.
func Test_Backend_OverlappingSessionReaders_RetainSharedTable(t *testing.T) {
	operations := &fakeSharedMemory{}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
	oldService, table := operations.services[0], operations.tables[0]

	oldEntered := make(chan struct{})
	oldRelease := make(chan struct{})
	newEntered := make(chan struct{})
	newRelease := make(chan struct{})
	var readCount atomic.Int32
	operations.hook = func(operation string) error {
		if operation == "read-sessions" {
			if readCount.Add(1) == 1 {
				close(oldEntered)
				<-oldRelease
			} else {
				close(newEntered)
				<-newRelease
			}
		}
		return nil
	}
	oldRead := make(chan error, 1)
	go func() {
		_, _, _, err := backend.ListSessions("vs0", 0, 1)
		oldRead <- err
	}()
	waitForBackendSignal(t, oldEntered)
	require.NoError(t, backend.UpdateService(serviceWithRealServer("vs0")))
	newService := operations.services[1]
	newRead := make(chan error, 1)
	go func() {
		_, _, _, err := backend.ListSessions("vs0", 0, 1)
		newRead <- err
	}()
	waitForBackendSignal(t, newEntered)
	require.NoError(t, backend.DeleteService("vs0"))
	require.Equal(t, int32(0), oldService.freeCalls.Load())
	require.Equal(t, int32(0), newService.freeCalls.Load())
	require.Equal(t, int32(0), table.freeCalls.Load())

	close(oldRelease)
	require.NoError(t, <-oldRead)
	require.NoError(t, backend.CreateService(sampleService("trigger1")))
	require.Equal(t, int32(1), oldService.freeCalls.Load())
	require.Equal(t, int32(0), newService.freeCalls.Load())
	require.Equal(t, int32(0), table.freeCalls.Load())

	close(newRelease)
	require.NoError(t, <-newRead)
	require.Equal(t, int32(0), table.freeCalls.Load())
	require.NoError(t, backend.CreateService(sampleService("trigger2")))
	require.Equal(t, int32(1), newService.freeCalls.Load())
	require.Equal(t, int32(1), table.freeCalls.Load())
	require.NoError(t, backend.CreateService(sampleService("trigger3")))
	require.Equal(t, int32(1), oldService.freeCalls.Load())
	require.Equal(t, int32(1), newService.freeCalls.Load())
	require.Equal(t, int32(1), table.freeCalls.Load())
}

// Test_Backend_GetService_AfterDelete verifies that inspection of a deleted
// service reports NotFound after the deletion returns.
func Test_Backend_GetService_AfterDelete(t *testing.T) {
	backend := controlplane.NewBackendWithSharedMemory(&fakeSharedMemory{})
	require.NoError(t, backend.CreateService(sampleService("vs0")))
	require.NoError(t, backend.DeleteService("vs0"))
	_, err := backend.GetService("vs0")
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_Backend_GetService_RealIndexMask verifies that real shared-memory
// publication preserves the configured nonzero scheduler index mask.
func Test_Backend_GetService_RealIndexMask(t *testing.T) {
	backend := newTestBackend(t)
	require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
	response, err := backend.GetService("vs0")
	require.NoError(t, err)
	require.Equal(t, uint32(0x0f), response.IndexMask)
}

// Test_Backend_RealOps_RejectWrappedTable verifies that an unsupported table
// handle reports an error before the real agent is accessed.
func Test_Backend_RealOps_RejectWrappedTable(t *testing.T) {
	operations := controlplane.NewSharedMemoryOps(nil)
	_, err := operations.CreateVirtualService("vs0", cl3bobject.VirtualServiceConfig{}, &fakeSessionTable{})
	require.ErrorContains(t, err, "invalid session table handle")
}

// Test_Backend_SchedulerError_PublishesAppliedState verifies that a failed
// ring rebuild still exposes the already applied state or weight change.
func Test_Backend_SchedulerError_PublishesAppliedState(t *testing.T) {
	operations := &fakeSharedMemory{}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
	operations.hook = func(operation string) error {
		if operation == "update-ring" {
			return errFakeRing
		}
		return nil
	}
	err := backend.UpdateRealServerState("vs0", 0, false)
	require.Equal(t, codes.Internal, status.Code(err))
	response, err := backend.GetService("vs0")
	require.NoError(t, err)
	require.False(t, response.RealServers[0].Enabled)

	err = backend.UpdateRealServerWeight("vs0", 0, 7)
	require.Equal(t, codes.Internal, status.Code(err))
	response, err = backend.GetService("vs0")
	require.NoError(t, err)
	require.Equal(t, uint32(7), response.RealServers[0].Weight)
}

// Test_Backend_GetService_ReturnsIndependentStorage verifies that callers
// cannot alter later inspection results through a returned response.
func Test_Backend_GetService_ReturnsIndependentStorage(t *testing.T) {
	backend := controlplane.NewBackendWithSharedMemory(&fakeSharedMemory{})
	require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
	response, err := backend.GetService("vs0")
	require.NoError(t, err)
	response.RealServers[0].DestinationAddress[0] = 0
	response.RealServers[0].Weight = 99
	response.RealServers[0].SourceNetwork = nil
	response, err = backend.GetService("vs0")
	require.NoError(t, err)
	require.Equal(t, byte(192), response.RealServers[0].DestinationAddress[0])
	require.Equal(t, uint32(1), response.RealServers[0].Weight)
	require.NotNil(t, response.RealServers[0].SourceNetwork)
}

// Test_Backend_Writers_SerializeAcrossPublicationAndFree verifies that a
// second writer cannot enter allocation while the first publishes or frees.
func Test_Backend_Writers_SerializeAcrossPublicationAndFree(t *testing.T) {
	for _, phase := range []string{"publish-service", "free-service"} {
		t.Run(phase, func(t *testing.T) {
			operations := &fakeSharedMemory{}
			backend := controlplane.NewBackendWithSharedMemory(operations)
			require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
			entered := make(chan struct{})
			release := make(chan struct{})
			secondEntered := make(chan struct{})
			var blockOnce sync.Once
			operations.hook = func(operation string) error {
				if operation == phase {
					blockOnce.Do(func() {
						close(entered)
						<-release
					})
				}
				if operation == "create-table" {
					close(secondEntered)
				}
				return nil
			}
			first := make(chan error, 1)
			go func() { first <- backend.UpdateService(serviceWithRealServer("vs0")) }()
			waitForBackendSignal(t, entered)
			started := make(chan struct{})
			second := make(chan error, 1)
			go func() {
				close(started)
				second <- backend.CreateService(sampleService("vs1"))
			}()
			waitForBackendSignal(t, started)
			select {
			case <-secondEntered:
				close(release)
				t.Fatal("second writer entered shared-memory allocation")
			case <-time.After(100 * time.Millisecond):
			}
			require.Equal(t, []string{"vs0"}, backend.ListServices())
			close(release)
			require.NoError(t, <-first)
			require.NoError(t, <-second)
		})
	}
}

// Test_Backend_FailedPublication_KeepsPublishedView verifies that a failed
// replacement and a failed new publish leave the prior visible names intact.
func Test_Backend_FailedPublication_KeepsPublishedView(t *testing.T) {
	operations := &fakeSharedMemory{}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
	operations.hook = func(operation string) error {
		if operation == "publish-service" || operation == "publish-module" {
			return errFakeRing
		}
		return nil
	}
	replacement := serviceWithRealServer("vs0")
	replacement.HashMask = 0x55
	require.Error(t, backend.UpdateService(replacement))
	require.Error(t, backend.CreateService(sampleService("vs1")))
	require.Error(t, backend.UpdateModuleConfig(&l3bpb.ModuleConfig{Name: "cfg1"}))
	require.Equal(t, []string{"vs0"}, backend.ListServices())
	require.Empty(t, backend.ListModuleConfigs())
	response, err := backend.GetService("vs0")
	require.NoError(t, err)
	require.Equal(t, uint32(0xff), response.HashMask)
	require.Equal(t, int32(1), operations.services[1].freeCalls.Load())
	require.Equal(t, int32(1), operations.services[2].freeCalls.Load())
	require.Equal(t, int32(1), operations.tables[1].freeCalls.Load())
}
