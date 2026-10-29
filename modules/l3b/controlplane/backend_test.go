package l3b_test

import (
	"errors"
	"math"
	"net/netip"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

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
	"github.com/yanet-platform/yanet2/modules/l3b/internal/testutils"
	"github.com/yanet-platform/yanet2/objects/l3b/bindings/go/cl3bobject"
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

// waitForBackendResult bounds assertions while teardown still joins the call.
func waitForBackendResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("backend call did not complete")
		return nil
	}
}

// backendWorkers releases owned barriers before joining every started call.
type backendWorkers struct {
	releases    []func()
	completions []<-chan struct{}
}

// newBackendWorkers joins calls before previously registered fixture cleanup.
func newBackendWorkers(t *testing.T, barriers ...chan struct{}) *backendWorkers {
	t.Helper()
	m := &backendWorkers{}
	for _, barrier := range barriers {
		var releaseOnce sync.Once
		m.releases = append(m.releases, func() {
			releaseOnce.Do(func() { close(barrier) })
		})
	}
	t.Cleanup(func() {
		for _, release := range m.releases {
			release()
		}
		for _, completion := range m.completions {
			<-completion
		}
	})
	return m
}

// Start registers a call for teardown before launching it asynchronously.
func (m *backendWorkers) Start(call func()) <-chan struct{} {
	completion := make(chan struct{})
	m.completions = append(m.completions, completion)
	go func() {
		defer close(completion)
		call()
	}()
	return completion
}

// Run buffers the result so teardown can join an unobserved call.
func (m *backendWorkers) Run(call func() error) <-chan error {
	result := make(chan error, 1)
	m.Start(func() { result <- call() })
	return result
}

// Release opens an owned barrier at most once, including during teardown.
func (m *backendWorkers) Release(idx int) {
	m.releases[idx]()
}

// fakeSharedMemory exposes barriers and injected failures without C allocations.
type fakeSharedMemory struct {
	Hook     func(string) error
	Services []*fakeVirtualService
	Tables   []*fakeSessionTable
}

// Call runs an operation's configured barrier or failure hook.
func (m *fakeSharedMemory) Call(operation string) error {
	if m.Hook != nil {
		return m.Hook(operation)
	}
	return nil
}

func (m *fakeSharedMemory) WorkerCount() uint32 { return 1 }

func (m *fakeSharedMemory) CreateSessionTable(name string, workers uint16, size uint32) (controlplane.SessionTableHandle, error) {
	if err := m.Call("create-table"); err != nil {
		return nil, err
	}
	table := &fakeSessionTable{Operations: m, SessionIndexSize: size}
	m.Tables = append(m.Tables, table)
	return table, nil
}

func (m *fakeSharedMemory) CreateVirtualService(name string, config cl3bobject.VirtualServiceConfig, table controlplane.SessionTableHandle) (controlplane.VirtualServiceHandle, error) {
	if err := m.Call("create-service"); err != nil {
		return nil, err
	}
	service := &fakeVirtualService{Operations: m}
	m.Services = append(m.Services, service)
	return service, nil
}

func (m *fakeSharedMemory) DeleteVirtualService(name string) error {
	return m.Call("delete-service")
}

func (m *fakeSharedMemory) NewModuleConfig(name string) (controlplane.ModuleHandle, error) {
	if err := m.Call("create-module"); err != nil {
		return nil, err
	}
	return &fakeModule{Operations: m}, nil
}

func (m *fakeSharedMemory) UpdateModules(modules []ffi.ModuleConfig) error {
	return m.Call("publish-module")
}

// fakeVirtualService distinguishes refused frees from successful destruction.
type fakeVirtualService struct {
	Operations   *fakeSharedMemory
	FreeCalls    atomic.Int32
	Refusals     atomic.Int32
	Destructions atomic.Int32
	Sessions     []cl3bobject.Session
	NextCursor   uint64
	CurrentTime  uint64
}

func (m *fakeVirtualService) Free() error {
	if err := m.Operations.Call("free-service"); err != nil {
		return err
	}
	m.FreeCalls.Add(1)
	if m.Refusals.Load() > 0 {
		m.Refusals.Add(-1)
		return ffi.ErrStillReferenced
	}
	m.Destructions.Add(1)
	return nil
}

func (m *fakeVirtualService) Publish() error { return m.Operations.Call("publish-service") }
func (m *fakeVirtualService) UpdateRing(indexes []uint32) error {
	return m.Operations.Call("update-ring")
}
func (m *fakeVirtualService) SetRealServerState(index uint32, enabled bool) error {
	return m.Operations.Call("set-state")
}
func (m *fakeVirtualService) ReadSessions(cursor uint64, limit uint32) ([]cl3bobject.Session, uint64, uint64, error) {
	if err := m.Operations.Call("read-sessions"); err != nil {
		return nil, 0, 0, err
	}
	return m.Sessions, m.NextCursor, m.CurrentTime, nil
}

// fakeSessionTable records a table's lifetime across service generations.
type fakeSessionTable struct {
	Operations       *fakeSharedMemory
	SessionIndexSize uint32
	FreeCalls        atomic.Int32
	Destructions     atomic.Int32
}

func (m *fakeSessionTable) Free() error {
	if err := m.Operations.Call("free-table"); err != nil {
		return err
	}
	m.FreeCalls.Add(1)
	m.Destructions.Add(1)
	return nil
}

// fakeModule supports module barriers without entering the C publication path.
type fakeModule struct{ Operations *fakeSharedMemory }

func (m *fakeModule) Free() error { return m.Operations.Call("free-module") }
func (m *fakeModule) Update(rules []cl3b.DestinationFilterRule) error {
	return m.Operations.Call("update-module")
}
func (m *fakeModule) AsFFIModule() ffi.ModuleConfig { return ffi.ModuleConfig{} }

// recordingModuleOps records real module submissions without changing ownership.
type recordingModuleOps struct {
	controlplane.SharedMemoryOps
	Modules          []*recordedModule
	Submissions      [][]unsafe.Pointer
	PublicationError error
}

func (m *recordingModuleOps) NewModuleConfig(name string) (controlplane.ModuleHandle, error) {
	module, err := m.SharedMemoryOps.NewModuleConfig(name)
	if err != nil {
		return nil, err
	}
	recorded := &recordedModule{
		ModuleHandle: module,
		Token:        module.AsFFIModule().AsRawPtr(),
	}
	m.Modules = append(m.Modules, recorded)
	return recorded, nil
}

func (m *recordingModuleOps) UpdateModules(modules []ffi.ModuleConfig) error {
	tokens := make([]unsafe.Pointer, len(modules))
	for idx, module := range modules {
		tokens[idx] = module.AsRawPtr()
	}
	m.Submissions = append(m.Submissions, tokens)
	if m.PublicationError != nil {
		return m.PublicationError
	}
	return m.SharedMemoryOps.UpdateModules(modules)
}

// recordedModule separates destruction attempts from successful C reclamation.
//
// Comparison tokens are snapshots taken before reclamation. Destruction counts
// belong to individual allocations, independent of reused addresses.
type recordedModule struct {
	controlplane.ModuleHandle
	Token        unsafe.Pointer
	FreeCalls    int
	FreeResults  []error
	Destructions int
}

func (m *recordedModule) Free() error {
	m.FreeCalls++
	hadAllocation := m.ModuleHandle.AsFFIModule().AsRawPtr() != nil
	err := m.ModuleHandle.Free()
	m.FreeResults = append(m.FreeResults, err)
	if err != nil {
		return err
	}
	if hadAllocation {
		m.Destructions++
	}
	return nil
}

// AssertRetired accepts only a C reference refusal or one successful destruction.
func (m *recordedModule) AssertRetired(t *testing.T) {
	t.Helper()
	require.Positive(t, m.FreeCalls)
	require.Len(t, m.FreeResults, m.FreeCalls)
	var successes int
	for _, err := range m.FreeResults {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, ffi.ErrStillReferenced)
		}
	}
	require.LessOrEqual(t, successes, 1)
	require.Equal(t, successes, m.Destructions)
}

// capturingServiceOps retains the real service handle for C-backed inspection.
type capturingServiceOps struct {
	controlplane.SharedMemoryOps
	Agent              *ffi.Agent
	Service            *capturedService
	Fail               string
	ConstructedEnabled []bool
}

func (m *capturingServiceOps) CreateVirtualService(name string, config cl3bobject.VirtualServiceConfig, table controlplane.SessionTableHandle) (controlplane.VirtualServiceHandle, error) {
	sharedTable, ok := table.(*cl3bobject.SessionTableObject)
	if !ok {
		return nil, errors.New("capture adapter requires a real session table")
	}
	object, err := cl3bobject.CreateVirtualService(m.Agent, name, config, sharedTable)
	if err != nil {
		return nil, err
	}
	info, err := object.Inspect()
	if err != nil {
		return nil, errors.Join(err, object.Free())
	}
	m.ConstructedEnabled = nil
	for _, real := range info.RealServers {
		m.ConstructedEnabled = append(m.ConstructedEnabled, real.Enabled)
	}
	m.Service = &capturedService{VirtualServiceObject: object, Agent: m.Agent, Fail: m.Fail}
	return m.Service, nil
}

// capturedService publishes and reads through the same agent as its allocation.
type capturedService struct {
	*cl3bobject.VirtualServiceObject
	Agent *ffi.Agent
	Fail  string
}

func (m *capturedService) Publish() error {
	if m.Fail == "publish-service" {
		return errFakeRing
	}
	return m.VirtualServiceObject.Publish(m.Agent)
}

func (m *capturedService) UpdateRing(indexes []uint32) error {
	if m.Fail == "update-ring" {
		return errFakeRing
	}
	return m.VirtualServiceObject.UpdateRing(indexes)
}

func (m *capturedService) SetRealServerState(index uint32, enabled bool) error {
	if m.Fail == "set-state" {
		return errFakeRing
	}
	return m.VirtualServiceObject.SetRealServerState(index, enabled)
}

func (m *capturedService) ReadSessions(cursor uint64, limit uint32) ([]cl3bobject.Session, uint64, uint64, error) {
	return m.VirtualServiceObject.ReadSessions(m.Agent, cursor, limit)
}

// newTestBackend returns a shared-memory backend over a harness with the l3b
// module and its objects loaded.
func newTestBackend(t *testing.T) controlplane.Backend {
	t.Helper()
	backend, _, _ := newTestBackendFixture(t)
	return backend
}

// newTestAgent returns a real shared-memory agent owned by the test harness.
func newTestAgent(t *testing.T) *ffi.Agent {
	t.Helper()
	_, agent, _ := newTestBackendFixture(t)
	return agent
}

// newTestBackendFixture returns a real backend and its accounting/worker owners.
func newTestBackendFixture(t *testing.T) (controlplane.Backend, *ffi.Agent, *dataplaneut.Harness) {
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

	return controlplane.NewBackend(agent), agent, harness
}

// stalledPublishOps pauses real publication while retaining C-backed reads.
type stalledPublishOps struct {
	controlplane.SharedMemoryOps
	Entered chan struct{}
	Release chan struct{}
}

func (m *stalledPublishOps) CreateVirtualService(name string, config cl3bobject.VirtualServiceConfig, table controlplane.SessionTableHandle) (controlplane.VirtualServiceHandle, error) {
	service, err := m.SharedMemoryOps.CreateVirtualService(name, config, table)
	if err != nil {
		return nil, err
	}
	return &stalledPublishService{VirtualServiceHandle: service, Operations: m}, nil
}

// stalledPublishService blocks before publication, leaving accepted state live.
type stalledPublishService struct {
	controlplane.VirtualServiceHandle
	Operations *stalledPublishOps
}

func (m *stalledPublishService) Publish() error {
	if m.Operations.Entered != nil {
		close(m.Operations.Entered)
		<-m.Operations.Release
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
	operations.Entered = make(chan struct{})
	operations.Release = make(chan struct{})
	workers := newBackendWorkers(t, operations.Release)
	mutation := workers.Run(func() error {
		return backend.UpdateService(serviceWithRealServer("vs0"))
	})
	waitForBackendSignal(t, operations.Entered)
	read := workers.Run(func() error {
		_, _, _, err := backend.ListSessions("vs0", 0, 1)
		return err
	})
	require.NoError(t, waitForBackendResult(t, read))
	workers.Release(0)
	require.NoError(t, waitForBackendResult(t, mutation))
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

// Test_Backend_CreateService_SessionCapacity verifies raw forwarding and rejection
// before shared-memory work, including when the requested name already exists.
func Test_Backend_CreateService_SessionCapacity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		size  uint32
		valid bool
	}{
		{"zero selects default", 0, true},
		{"minimum positive rounds natively", 1, true},
		{"minimum native size", 16, true},
		{"non power of two", 1000, true},
		{"maximum admitted input", 1073741824, true},
		{"above maximum", 1073741825, false},
		{"maximum uint32", math.MaxUint32, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operations := &fakeSharedMemory{}
			backend := controlplane.NewBackendWithSharedMemory(operations)
			require.NoError(t, backend.CreateService(sampleService("existing")))
			var calls []string
			operations.Hook = func(operation string) error {
				calls = append(calls, operation)
				return nil
			}
			request := sampleService("candidate")
			request.SessionIndexSize = tc.size
			err := backend.CreateService(request)
			if tc.valid {
				require.NoError(t, err)
				require.Len(t, operations.Tables, 2)
				require.Equal(t, tc.size, operations.Tables[1].SessionIndexSize)
			} else {
				require.Equal(t, codes.InvalidArgument, status.Code(err))
				require.Empty(t, calls)
				require.Len(t, operations.Tables, 1)
				require.Len(t, operations.Services, 1)
				require.Equal(t, []string{"existing"}, backend.ListServices())
			}
			require.Equal(t, tc.size, request.SessionIndexSize)

			calls = nil
			request.Name = "existing"
			err = backend.CreateService(request)
			if tc.valid {
				require.Equal(t, codes.AlreadyExists, status.Code(err))
			} else {
				require.Equal(t, codes.InvalidArgument, status.Code(err))
			}
			require.Empty(t, calls)
		})
	}
}

// Test_Parity_SessionCapacity verifies native and request admission agree without
// allocating a table, using an empty allocator to distinguish admission from rejection.
func Test_Parity_SessionCapacity(t *testing.T) {
	for _, tc := range []struct {
		name string
		size uint32
		want syscall.Errno
	}{
		{"default", 0, syscall.ENOMEM},
		{"minimum positive", 1, syscall.ENOMEM},
		{"minimum native size", 16, syscall.ENOMEM},
		{"non power of two", 1000, syscall.ENOMEM},
		{"maximum admitted", l3bpb.MaxSessionIndexSize, syscall.ENOMEM},
		{"above maximum", l3bpb.MaxSessionIndexSize + 1, syscall.EINVAL},
		{"maximum uint32", math.MaxUint32, syscall.EINVAL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, testutils.SessionCapacityErrno(tc.size))
			err := (&l3bpb.CreateServiceRequest{Service: &l3bpb.VirtualService{
				Name: "vs0", SessionIndexSize: tc.size,
			}}).Validate()
			if tc.want == syscall.ENOMEM {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// Test_Backend_CreateService_InvalidCapacitySkipsReclamation verifies that an
// invalid request leaves even reclaimable retired handles untouched.
func Test_Backend_CreateService_InvalidCapacitySkipsReclamation(t *testing.T) {
	operations := &fakeSharedMemory{}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	require.NoError(t, backend.CreateService(sampleService("vs0")))
	retired := operations.Services[0]
	retired.Refusals.Store(1)
	require.NoError(t, backend.UpdateService(sampleService("vs0")))
	require.Zero(t, retired.Destructions.Load())
	retired.Refusals.Store(0)
	freeCalls := retired.FreeCalls.Load()
	request := sampleService("vs1")
	request.SessionIndexSize = math.MaxUint32
	require.Equal(t, codes.InvalidArgument, status.Code(backend.CreateService(request)))
	require.Equal(t, freeCalls, retired.FreeCalls.Load())
	require.Zero(t, retired.Destructions.Load())
	request.SessionIndexSize = 16
	require.NoError(t, backend.CreateService(request))
	require.EqualValues(t, 1, retired.Destructions.Load())
}

// Test_Backend_UpdateService_PreservesSessionCapacity verifies that the creation
// bound does not add resizing or validation to the legacy replacement path.
func Test_Backend_UpdateService_PreservesSessionCapacity(t *testing.T) {
	operations := &fakeSharedMemory{}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	request := sampleService("vs0")
	request.SessionIndexSize = 16
	require.NoError(t, backend.CreateService(request))
	table := operations.Tables[0]
	request.SessionIndexSize = math.MaxUint32
	require.NoError(t, backend.UpdateService(request))
	require.Len(t, operations.Tables, 1)
	require.Same(t, table, operations.Tables[0])
	require.Equal(t, uint32(16), table.SessionIndexSize)
	require.Zero(t, table.Destructions.Load())
}

// Test_Backend_CreateService_SmallCapacity verifies that default and small
// positive requests still construct and publish through the native path.
func Test_Backend_CreateService_SmallCapacity(t *testing.T) {
	for _, tc := range []struct {
		name string
		size uint32
	}{
		{"default", 0},
		{"minimum positive", 1},
		{"minimum native size", 16},
		{"non power of two", 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := newTestBackend(t)
			request := serviceWithRealServer("vs0")
			request.SessionIndexSize = tc.size
			require.NoError(t, backend.CreateService(request))
			view, err := backend.GetService("vs0")
			require.NoError(t, err)
			require.False(t, view.RealServers[0].Enabled)
			require.Equal(t, tc.size, request.SessionIndexSize)
			require.NoError(t, backend.DeleteService("vs0"))
		})
	}
}

// Test_Backend_CreateService_InvalidCapacityPreservesNativeState verifies that
// rejected creation cannot consume arena space or replace accepted native state.
func Test_Backend_CreateService_InvalidCapacityPreservesNativeState(t *testing.T) {
	backend, agent, harness := newTestBackendFixture(t)
	request := serviceWithRealServer("existing")
	request.SessionIndexSize = 16
	require.NoError(t, backend.CreateService(request))
	t.Cleanup(func() { require.NoError(t, backend.DeleteService("existing")) })
	before, err := backend.GetService("existing")
	require.NoError(t, err)
	require.False(t, before.RealServers[0].Enabled)
	capacity := agent.BlockAllocatorFreeSize()
	generation, err := harness.PublishedExecutionContext(0)
	require.NoError(t, err)
	require.NotZero(t, generation)
	for _, name := range []string{"absent", "existing"} {
		for _, size := range []uint32{1073741825, math.MaxUint32} {
			request := sampleService(name)
			request.SessionIndexSize = size
			require.Equal(t, codes.InvalidArgument, status.Code(backend.CreateService(request)))
			require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
			after, err := backend.GetService("existing")
			require.NoError(t, err)
			require.Equal(t, before, after)
			current, err := harness.PublishedExecutionContext(0)
			require.NoError(t, err)
			require.Equal(t, generation, current)
			require.Equal(t, []string{"existing"}, backend.ListServices())
		}
	}
}

// serviceWithRealServer returns a descriptor with one IPv4 destination.
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
			require.NoError(t, backend.UpdateRealServerState("vs0", 0, true))
			require.NoError(t, backend.UpdateModuleConfig(&l3bpb.ModuleConfig{Name: "cfg0"}))

			entered := make(chan struct{})
			release := make(chan struct{})
			workers := newBackendWorkers(t, release)
			operations.Hook = func(operation string) error {
				if operation == tc.operation {
					close(entered)
					<-release
				}
				return nil
			}
			mutation := workers.Run(func() error { return tc.mutate(backend) })
			waitForBackendSignal(t, entered)

			reads := workers.Run(func() error {
				if names := backend.ListServices(); len(names) != 1 || names[0] != "vs0" {
					return errors.New("service list changed before publication")
				}
				if names := backend.ListModuleConfigs(); len(names) != 1 || names[0] != "cfg0" {
					return errors.New("module list changed before publication")
				}
				response, err := backend.GetService("vs0")
				if err != nil {
					return err
				}
				if response == nil || response.HashMask != tc.expectedMask || len(response.RealServers) != 1 || response.RealServers[0].Weight != 1 || !response.RealServers[0].Enabled {
					return errors.New("inspection observed an unfinished mutation")
				}
				_, _, _, err = backend.ListSessions("vs0", 0, 0)
				return err
			})
			require.NoError(t, waitForBackendResult(t, reads))
			workers.Release(0)
			require.NoError(t, waitForBackendResult(t, mutation))
			if tc.operation == "set-state" || tc.operation == "update-ring" {
				response, err := backend.GetService("vs0")
				require.NoError(t, err)
				require.Len(t, response.RealServers, 1)
				expectedWeight := uint32(1)
				if tc.operation == "update-ring" {
					expectedWeight = 2
				}
				require.Equal(t, tc.operation != "set-state", response.RealServers[0].Enabled)
				require.Equal(t, expectedWeight, response.RealServers[0].Weight)
			}
		})
	}
}

// Test_Backend_UpdateModuleConfig_PreservesAcceptedHandles verifies that only
// accepted replacements retire old handles and unrelated modules stay published.
func Test_Backend_UpdateModuleConfig_PreservesAcceptedHandles(t *testing.T) {
	publicationError := errors.New("module publication rejected")
	for _, tc := range []struct {
		name             string
		publicationError error
	}{
		{name: "accepted replacement retains the unrelated module"},
		{name: "rejected replacement retains both accepted modules", publicationError: publicationError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := newTestAgent(t)
			operations := &recordingModuleOps{SharedMemoryOps: controlplane.NewSharedMemoryOps(agent)}
			backend := controlplane.NewBackendWithSharedMemory(operations)
			require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
			firstConfig := &l3bpb.ModuleConfig{
				Name:                   "cfg0",
				DestinationFilterRules: []*l3bpb.DestinationFilterRule{{Service: "vs0"}},
			}
			require.NoError(t, backend.UpdateModuleConfig(firstConfig))
			first := operations.Modules[0]
			require.NotNil(t, first.Token)
			require.ElementsMatch(t, []unsafe.Pointer{first.Token}, operations.Submissions[0])
			secondConfig := &l3bpb.ModuleConfig{
				Name:                   "cfg1",
				DestinationFilterRules: []*l3bpb.DestinationFilterRule{{Service: "vs0"}},
			}
			require.NoError(t, backend.UpdateModuleConfig(secondConfig))
			unrelated := operations.Modules[1]
			require.NotNil(t, unrelated.Token)
			require.ElementsMatch(t, []unsafe.Pointer{first.Token, unrelated.Token}, operations.Submissions[1])

			operations.PublicationError = tc.publicationError
			err := backend.UpdateModuleConfig(&l3bpb.ModuleConfig{Name: "cfg0"})
			if tc.publicationError == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, publicationError)
			}
			require.Len(t, operations.Modules, 3)
			require.Len(t, operations.Submissions, 3)
			replacement := operations.Modules[2]
			require.NotNil(t, replacement.Token)
			require.ElementsMatch(t, []unsafe.Pointer{replacement.Token, unrelated.Token}, operations.Submissions[2])
			require.Equal(t, []string{"cfg0", "cfg1"}, backend.ListModuleConfigs())
			var registry []string
			for _, config := range agent.DPConfig().CPConfigs() {
				registry = append(registry, config.Type+"/"+config.Name)
			}
			require.ElementsMatch(t, []string{"l3b/cfg0", "l3b/cfg1"}, registry)
			require.Zero(t, unrelated.FreeCalls)
			require.Zero(t, unrelated.Destructions)
			retained, retired := replacement, first
			if tc.publicationError != nil {
				retained, retired = first, replacement
				require.Equal(t, 1, replacement.FreeCalls)
				require.Equal(t, 1, replacement.Destructions)
			}
			require.Zero(t, retained.FreeCalls)
			require.Zero(t, retained.Destructions)
			require.Equal(t, 1, retired.FreeCalls)
			retired.AssertRetired(t)

			operations.PublicationError = nil
			require.NoError(t, backend.UpdateModuleConfig(&l3bpb.ModuleConfig{Name: "cfg1"}))
			require.Len(t, operations.Modules, 4)
			require.Len(t, operations.Submissions, 4)
			secondReplacement := operations.Modules[3]
			require.NotNil(t, secondReplacement.Token)
			require.ElementsMatch(
				t,
				[]unsafe.Pointer{retained.Token, secondReplacement.Token},
				operations.Submissions[3],
			)
			require.Equal(t, []string{"cfg0", "cfg1"}, backend.ListModuleConfigs())
			registry = nil
			for _, config := range agent.DPConfig().CPConfigs() {
				registry = append(registry, config.Type+"/"+config.Name)
			}
			require.ElementsMatch(t, []string{"l3b/cfg0", "l3b/cfg1"}, registry)
			require.Zero(t, retained.FreeCalls)
			require.Zero(t, retained.Destructions)
			require.Zero(t, secondReplacement.FreeCalls)
			require.Zero(t, secondReplacement.Destructions)
			retired.AssertRetired(t)
			unrelated.AssertRetired(t)
		})
	}
}

// Test_Backend_RuntimeUpdates_AdvanceSnapshot verifies that successful mutations
// retain each server's state and configured weight through re-enabling.
func Test_Backend_RuntimeUpdates_AdvanceSnapshot(t *testing.T) {
	backend := controlplane.NewBackendWithSharedMemory(&fakeSharedMemory{})
	service := serviceWithRealServer("vs0")
	service.RealServers = append(service.RealServers, &l3bpb.RealServer{
		DestinationAddress: netip.MustParseAddr("2001:db8:1::2").AsSlice(),
		SourceNetwork:      commonpb.NewIPNetworkFrom(xnetip.MustParseNetwork("2001:db8:2::/64")),
	})
	require.NoError(t, backend.CreateService(service))
	for _, tc := range []struct {
		name    string
		mutate  func() error
		enabled bool
		weight  uint32
	}{
		{name: "new service starts disabled with unit weight", weight: 1},
		{
			name:   "disabling preserves the configured weight",
			mutate: func() error { return backend.UpdateRealServerState("vs0", 1, false) },
			weight: 1,
		},
		{
			name:   "weight update advances the disabled service snapshot",
			mutate: func() error { return backend.UpdateRealServerWeight("vs0", 1, 7) },
			weight: 7,
		},
		{
			name:    "re-enabling preserves the updated weight",
			mutate:  func() error { return backend.UpdateRealServerState("vs0", 1, true) },
			enabled: true,
			weight:  7,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mutate != nil {
				require.NoError(t, tc.mutate())
			}
			response, err := backend.GetService("vs0")
			require.NoError(t, err)
			require.Len(t, response.RealServers, 2)
			require.Equal(t, []byte{192, 0, 2, 1}, response.RealServers[0].DestinationAddress)
			require.False(t, response.RealServers[0].Enabled)
			require.Equal(t, uint32(1), response.RealServers[0].Weight)
			require.Equal(t, netip.MustParseAddr("2001:db8:1::2").AsSlice(), response.RealServers[1].DestinationAddress)
			require.Equal(t, tc.enabled, response.RealServers[1].Enabled)
			require.Equal(t, tc.weight, response.RealServers[1].Weight)
		})
	}
}

// Test_Backend_GetService_PreservesDescriptorsAcrossReplacement verifies that
// every address, source mask and server position survives snapshot publication.
func Test_Backend_GetService_PreservesDescriptorsAcrossReplacement(t *testing.T) {
	type serverDescriptor struct {
		Destination   string
		SourceNetwork string
		SourceAddress string
		SourceMask    string
	}
	backend := controlplane.NewBackendWithSharedMemory(&fakeSharedMemory{})
	cases := []struct {
		name      string
		hashMask  uint32
		indexMask uint32
		servers   []serverDescriptor
	}{
		{
			name:      "creation retains interleaved IPv4 and IPv6 descriptors",
			hashMask:  0x19,
			indexMask: 0x0f,
			servers: []serverDescriptor{
				{"192.0.2.11", "198.51.100.0/25", "198.51.100.0", "255.255.255.128"},
				{"2001:db8:10::1234", "2001:db8:100::/64", "2001:db8:100::", "ffff:ffff:ffff:ffff::"},
				{"203.0.113.57", "10.42.7.96/27", "10.42.7.96", "255.255.255.224"},
				{"2001:db8:20::abcd", "2001:db8:200:3:4:5::/96", "2001:db8:200:3:4:5::", "ffff:ffff:ffff:ffff:ffff:ffff::"},
			},
		},
		{
			name:      "replacement advances all descriptors and their order",
			hashMask:  0x2b,
			indexMask: 0x07,
			servers: []serverDescriptor{
				{"2001:db8:30::4567", "2001:db8:300:8:9::/80", "2001:db8:300:8:9::", "ffff:ffff:ffff:ffff:ffff::"},
				{"198.51.100.219", "172.16.9.192/26", "172.16.9.192", "255.255.255.192"},
				{"2001:db8:40::89ab", "2001:db8:400:9:a:b:c::/112", "2001:db8:400:9:a:b:c::", "ffff:ffff:ffff:ffff:ffff:ffff:ffff::"},
				{"192.0.2.99", "10.99.1.4/30", "10.99.1.4", "255.255.255.252"},
			},
		},
	}
	for idx, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := sampleService("vs0")
			service.HashMask = tc.hashMask
			service.IndexMask = tc.indexMask
			for _, descriptor := range tc.servers {
				service.RealServers = append(service.RealServers, &l3bpb.RealServer{
					DestinationAddress: netip.MustParseAddr(descriptor.Destination).AsSlice(),
					SourceNetwork: commonpb.NewIPNetworkFrom(
						xnetip.MustParseNetwork(descriptor.SourceNetwork),
					),
				})
			}
			if idx == 0 {
				require.NoError(t, backend.CreateService(service))
			} else {
				require.NoError(t, backend.UpdateService(service))
			}
			response, err := backend.GetService("vs0")
			require.NoError(t, err)
			require.Equal(t, tc.hashMask, response.HashMask)
			require.Equal(t, tc.indexMask, response.IndexMask)
			require.Len(t, response.RealServers, len(tc.servers))
			for idx, descriptor := range tc.servers {
				realServer := response.RealServers[idx]
				require.Equal(t, netip.MustParseAddr(descriptor.Destination).AsSlice(), realServer.DestinationAddress)
				require.NotNil(t, realServer.SourceNetwork)
				sourceNetwork, err := realServer.SourceNetwork.ToNetwork()
				require.NoError(t, err)
				require.Equal(t, netip.MustParseAddr(descriptor.SourceAddress).AsSlice(), sourceNetwork.Addr().AsSlice())
				require.Equal(t, netip.MustParseAddr(descriptor.SourceMask).AsSlice(), sourceNetwork.Mask().AsSlice())
				require.Equal(t, uint32(1), realServer.Weight)
				require.False(t, realServer.Enabled)
			}
		})
	}
}

// Test_Backend_ListSessions_PropagatesLeasedPage verifies that an admitted read
// returns its old generation's complete page, cursor and time after replacement.
func Test_Backend_ListSessions_PropagatesLeasedPage(t *testing.T) {
	const expectedCursor uint64 = 0x102030405060708
	const expectedTime uint64 = 1_723_456_789_012_345_678
	expectedSessions := []cl3bobject.Session{
		{
			SourceAddress: netip.MustParseAddr("203.0.113.101"),
			SourcePort:    42319,
			RealAddress:   netip.MustParseAddr("192.0.2.71"),
			ExpiresAt:     expectedTime + 5_000_000_000,
		},
		{
			SourceAddress: netip.MustParseAddr("2001:db8:ff::456"),
			SourcePort:    53487,
			RealAddress:   netip.MustParseAddr("2001:db8:1::99"),
			ExpiresAt:     expectedTime + 7_000_000_000,
		},
	}
	operations := &fakeSharedMemory{}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	service := serviceWithRealServer("vs0")
	service.RealServers[0].DestinationAddress = []byte{192, 0, 2, 71}
	service.RealServers = append(service.RealServers, &l3bpb.RealServer{
		DestinationAddress: netip.MustParseAddr("2001:db8:1::99").AsSlice(),
		SourceNetwork:      commonpb.NewIPNetworkFrom(xnetip.MustParseNetwork("2001:db8:2::/64")),
	})
	require.NoError(t, backend.CreateService(service))
	oldService := operations.Services[0]
	oldService.Sessions = append([]cl3bobject.Session(nil), expectedSessions...)
	oldService.NextCursor = expectedCursor
	oldService.CurrentTime = expectedTime
	entered := make(chan struct{})
	release := make(chan struct{})
	workers := newBackendWorkers(t, release)
	operations.Hook = func(operation string) error {
		if operation == "read-sessions" {
			close(entered)
			<-release
		}
		return nil
	}
	var sessions []cl3bobject.Session
	var nextCursor, currentTime uint64
	read := workers.Run(func() error {
		var err error
		sessions, nextCursor, currentTime, err = backend.ListSessions("vs0", 0x91, 2)
		return err
	})
	waitForBackendSignal(t, entered)
	replacement := workers.Run(func() error {
		return backend.UpdateService(serviceWithRealServer("vs0"))
	})
	require.NoError(t, waitForBackendResult(t, replacement))
	require.Equal(t, int32(0), oldService.FreeCalls.Load())
	workers.Release(0)
	require.NoError(t, waitForBackendResult(t, read))
	require.Equal(t, expectedSessions, sessions)
	require.Equal(t, expectedCursor, nextCursor)
	require.Equal(t, expectedTime, currentTime)
}

// Test_Backend_SessionRead_RetainsBothHandles verifies that deletion cannot
// free either object reached by a reader admitted before replacement.
func Test_Backend_SessionRead_RetainsBothHandles(t *testing.T) {
	operations := &fakeSharedMemory{}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
	oldService, table := operations.Services[0], operations.Tables[0]
	oldService.Refusals.Store(1)

	entered := make(chan struct{})
	release := make(chan struct{})
	workers := newBackendWorkers(t, release)
	operations.Hook = func(operation string) error {
		if operation == "read-sessions" {
			close(entered)
			<-release
		}
		return nil
	}
	read := workers.Run(func() error {
		_, _, _, err := backend.ListSessions("vs0", 0, 1)
		return err
	})
	waitForBackendSignal(t, entered)
	replacement := workers.Run(func() error {
		return backend.UpdateService(serviceWithRealServer("vs0"))
	})
	require.NoError(t, waitForBackendResult(t, replacement))
	deletion := workers.Run(func() error { return backend.DeleteService("vs0") })
	require.NoError(t, waitForBackendResult(t, deletion))
	names := make(chan []string, 1)
	listed := workers.Start(func() { names <- backend.ListServices() })
	waitForBackendSignal(t, listed)
	require.Empty(t, <-names)
	require.Equal(t, int32(0), oldService.FreeCalls.Load())
	require.Equal(t, int32(0), table.FreeCalls.Load())
	workers.Release(0)
	require.NoError(t, waitForBackendResult(t, read))
	require.Equal(t, int32(0), oldService.FreeCalls.Load())
	require.Equal(t, int32(0), table.FreeCalls.Load())
	firstRetry := workers.Run(func() error { return backend.CreateService(sampleService("vs1")) })
	require.NoError(t, waitForBackendResult(t, firstRetry))
	require.Equal(t, int32(1), oldService.FreeCalls.Load())
	require.Equal(t, int32(1), table.FreeCalls.Load())
	secondRetry := workers.Run(func() error { return backend.CreateService(sampleService("vs2")) })
	require.NoError(t, waitForBackendResult(t, secondRetry))
	require.Equal(t, int32(2), oldService.FreeCalls.Load())
	require.Equal(t, int32(1), table.FreeCalls.Load())
}

// Test_Backend_SessionReadError_ReleasesBothHandles verifies that a reader error
// releases both leases and later mutations reclaim each allocation exactly once.
func Test_Backend_SessionReadError_ReleasesBothHandles(t *testing.T) {
	readError := errors.New("session reader failed")
	operations := &fakeSharedMemory{}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
	oldService, table := operations.Services[0], operations.Tables[0]
	entered := make(chan struct{})
	release := make(chan struct{})
	workers := newBackendWorkers(t, release)
	operations.Hook = func(operation string) error {
		if operation == "read-sessions" {
			close(entered)
			<-release
			return readError
		}
		return nil
	}
	read := workers.Run(func() error {
		_, _, _, err := backend.ListSessions("vs0", 0, 1)
		return err
	})
	waitForBackendSignal(t, entered)
	mutation := workers.Run(func() error {
		if err := backend.UpdateService(serviceWithRealServer("vs0")); err != nil {
			return err
		}
		return backend.DeleteService("vs0")
	})
	require.NoError(t, waitForBackendResult(t, mutation))
	names := make(chan []string, 1)
	listed := workers.Start(func() { names <- backend.ListServices() })
	waitForBackendSignal(t, listed)
	require.Empty(t, <-names)
	require.Equal(t, int32(0), oldService.FreeCalls.Load())
	require.Equal(t, int32(0), table.FreeCalls.Load())
	require.Equal(t, int32(0), oldService.Destructions.Load())
	require.Equal(t, int32(0), table.Destructions.Load())
	workers.Release(0)
	require.ErrorIs(t, waitForBackendResult(t, read), readError)
	require.Equal(t, int32(0), oldService.FreeCalls.Load())
	require.Equal(t, int32(0), table.FreeCalls.Load())
	firstRetry := workers.Run(func() error { return backend.CreateService(sampleService("trigger1")) })
	require.NoError(t, waitForBackendResult(t, firstRetry))
	require.Equal(t, int32(1), oldService.FreeCalls.Load())
	require.Equal(t, int32(1), table.FreeCalls.Load())
	require.Equal(t, int32(1), oldService.Destructions.Load())
	require.Equal(t, int32(1), table.Destructions.Load())
	secondRetry := workers.Run(func() error { return backend.CreateService(sampleService("trigger2")) })
	require.NoError(t, waitForBackendResult(t, secondRetry))
	require.Equal(t, int32(1), oldService.FreeCalls.Load())
	require.Equal(t, int32(1), table.FreeCalls.Load())
	require.Equal(t, int32(1), oldService.Destructions.Load())
	require.Equal(t, int32(1), table.Destructions.Load())
}

// Test_Backend_OverlappingSessionReaders_RetainSharedTable verifies that
// replacement and deletion retain the table through both service generations.
func Test_Backend_OverlappingSessionReaders_RetainSharedTable(t *testing.T) {
	operations := &fakeSharedMemory{}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
	oldService, table := operations.Services[0], operations.Tables[0]

	oldEntered := make(chan struct{})
	oldRelease := make(chan struct{})
	newEntered := make(chan struct{})
	newRelease := make(chan struct{})
	workers := newBackendWorkers(t, oldRelease, newRelease)
	var readCount atomic.Int32
	operations.Hook = func(operation string) error {
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
	oldRead := workers.Run(func() error {
		_, _, _, err := backend.ListSessions("vs0", 0, 1)
		return err
	})
	waitForBackendSignal(t, oldEntered)
	replacement := workers.Run(func() error {
		return backend.UpdateService(serviceWithRealServer("vs0"))
	})
	require.NoError(t, waitForBackendResult(t, replacement))
	newService := operations.Services[1]
	newRead := workers.Run(func() error {
		_, _, _, err := backend.ListSessions("vs0", 0, 1)
		return err
	})
	waitForBackendSignal(t, newEntered)
	deletion := workers.Run(func() error { return backend.DeleteService("vs0") })
	require.NoError(t, waitForBackendResult(t, deletion))
	require.Equal(t, int32(0), oldService.FreeCalls.Load())
	require.Equal(t, int32(0), newService.FreeCalls.Load())
	require.Equal(t, int32(0), table.FreeCalls.Load())

	workers.Release(0)
	require.NoError(t, waitForBackendResult(t, oldRead))
	firstRetry := workers.Run(func() error { return backend.CreateService(sampleService("trigger1")) })
	require.NoError(t, waitForBackendResult(t, firstRetry))
	require.Equal(t, int32(1), oldService.FreeCalls.Load())
	require.Equal(t, int32(0), newService.FreeCalls.Load())
	require.Equal(t, int32(0), table.FreeCalls.Load())

	workers.Release(1)
	require.NoError(t, waitForBackendResult(t, newRead))
	require.Equal(t, int32(0), table.FreeCalls.Load())
	secondRetry := workers.Run(func() error { return backend.CreateService(sampleService("trigger2")) })
	require.NoError(t, waitForBackendResult(t, secondRetry))
	require.Equal(t, int32(1), newService.FreeCalls.Load())
	require.Equal(t, int32(1), table.FreeCalls.Load())
	thirdRetry := workers.Run(func() error { return backend.CreateService(sampleService("trigger3")) })
	require.NoError(t, waitForBackendResult(t, thirdRetry))
	require.Equal(t, int32(1), oldService.FreeCalls.Load())
	require.Equal(t, int32(1), newService.FreeCalls.Load())
	require.Equal(t, int32(1), table.FreeCalls.Load())
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

// Test_Backend_GetService_ReturnsConfiguredIndexMask verifies that inspection
// retains the configured scheduler mask when using the normal agent adapter.
func Test_Backend_GetService_ReturnsConfiguredIndexMask(t *testing.T) {
	backend := newTestBackend(t)
	require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
	response, err := backend.GetService("vs0")
	require.NoError(t, err)
	require.Equal(t, uint32(0x0f), response.IndexMask)
}

// Test_Backend_CreateService_PublishesRealIndexMask verifies that the captured
// C allocation contains the configured nonzero scheduler mask.
func Test_Backend_CreateService_PublishesRealIndexMask(t *testing.T) {
	agent := newTestAgent(t)
	operations := &capturingServiceOps{
		SharedMemoryOps: controlplane.NewSharedMemoryOps(agent),
		Agent:           agent,
	}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	service := serviceWithRealServer("vs0")
	service.IndexMask = 0x0f
	require.NoError(t, backend.CreateService(service))
	require.NotNil(t, operations.Service)
	info, err := operations.Service.VirtualServiceObject.Inspect()
	require.NoError(t, err)
	require.Equal(t, uint32(0x0f), info.IndexMask)
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
	require.NoError(t, backend.UpdateRealServerState("vs0", 0, true))
	operations.Hook = func(operation string) error {
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

// Test_Backend_Writers_SerializeAcrossPublicationAndFree verifies that a bounded
// probe observes no second allocation while the first writer publishes or frees.
func Test_Backend_Writers_SerializeAcrossPublicationAndFree(t *testing.T) {
	for _, phase := range []string{"publish-service", "free-service"} {
		t.Run(phase, func(t *testing.T) {
			operations := &fakeSharedMemory{}
			backend := controlplane.NewBackendWithSharedMemory(operations)
			require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
			entered := make(chan struct{})
			release := make(chan struct{})
			workers := newBackendWorkers(t, release)
			secondEntered := make(chan struct{})
			var blockOnce sync.Once
			operations.Hook = func(operation string) error {
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
			first := workers.Run(func() error {
				return backend.UpdateService(serviceWithRealServer("vs0"))
			})
			waitForBackendSignal(t, entered)
			started := make(chan struct{})
			second := workers.Run(func() error {
				close(started)
				return backend.CreateService(sampleService("vs1"))
			})
			waitForBackendSignal(t, started)
			select {
			case <-secondEntered:
				t.Fatal("second writer entered shared-memory allocation")
			case <-time.After(100 * time.Millisecond):
			}
			names := make(chan []string, 1)
			listed := workers.Start(func() { names <- backend.ListServices() })
			waitForBackendSignal(t, listed)
			require.Equal(t, []string{"vs0"}, <-names)
			workers.Release(0)
			require.NoError(t, waitForBackendResult(t, first))
			require.NoError(t, waitForBackendResult(t, second))
		})
	}
}

// Test_Backend_FailedPublication_KeepsPublishedView verifies that a failed
// replacement and a failed new publish leave the prior visible names intact.
func Test_Backend_FailedPublication_KeepsPublishedView(t *testing.T) {
	operations := &fakeSharedMemory{}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	require.NoError(t, backend.CreateService(serviceWithRealServer("vs0")))
	operations.Hook = func(operation string) error {
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
	require.Equal(t, int32(1), operations.Services[1].FreeCalls.Load())
	require.Equal(t, int32(1), operations.Services[2].FreeCalls.Load())
	require.Equal(t, int32(1), operations.Tables[1].FreeCalls.Load())
}

// requireRealStates compares accepted state with C records and installed indexes.
func requireRealStates(t *testing.T, backend controlplane.Backend, service *capturedService, enabled []bool, weights, ring []uint32) {
	t.Helper()
	response, err := backend.GetService("vs0")
	require.NoError(t, err)
	info, err := service.Inspect()
	require.NoError(t, err)
	require.Len(t, response.RealServers, len(enabled))
	require.Len(t, info.RealServers, len(enabled))
	for idx, active := range enabled {
		require.Equal(t, active, response.RealServers[idx].Enabled)
		require.Equal(t, active, info.RealServers[idx].Enabled)
		require.Equal(t, weights[idx], response.RealServers[idx].Weight)
		require.Equal(t, info.RealServers[idx].DestinationAddress.AsSlice(), response.RealServers[idx].DestinationAddress)
	}
	indexes, err := testutils.ReadRealRing(service.Agent, "vs0", 32)
	require.NoError(t, err)
	require.Equal(t, ring, indexes)
}

// Test_Backend_Replacement_RealIdentityContinuity verifies that only accepted
// surviving keys retain overrides and that construction never enables a real.
func Test_Backend_Replacement_RealIdentityContinuity(t *testing.T) {
	agent := newTestAgent(t)
	operations := &capturingServiceOps{SharedMemoryOps: controlplane.NewSharedMemoryOps(agent), Agent: agent}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	first := serviceWithRealServer("vs0").RealServers[0]
	second := &l3bpb.RealServer{
		DestinationAddress: netip.MustParseAddr("2001:db8::1").AsSlice(),
		SourceNetwork:      commonpb.NewIPNetworkFrom(xnetip.MustParseNetwork("2001:db8:2::/64")),
	}
	third := &l3bpb.RealServer{
		DestinationAddress: netip.MustParseAddr("192.0.2.2").AsSlice(),
		SourceNetwork:      first.SourceNetwork,
	}
	request := sampleService("vs0")
	request.RealServers = []*l3bpb.RealServer{first, second}
	require.NoError(t, backend.CreateService(request))
	require.Equal(t, []bool{false, false}, operations.ConstructedEnabled)
	requireRealStates(t, backend, operations.Service, []bool{false, false}, []uint32{1, 1}, []uint32{})

	for _, weight := range []uint32{0, 3} {
		require.NoError(t, backend.UpdateRealServerWeight("vs0", 0, weight))
		requireRealStates(t, backend, operations.Service, []bool{false, false}, []uint32{weight, 1}, []uint32{})
	}
	require.NoError(t, backend.UpdateRealServerWeight("vs0", 1, 0))
	require.NoError(t, backend.UpdateRealServerState("vs0", 0, true))
	require.NoError(t, backend.UpdateRealServerState("vs0", 1, true))

	for _, tc := range []struct {
		name          string
		reals         []*l3bpb.RealServer
		enabled       []bool
		weights, ring []uint32
	}{
		{"reorder retains enabled positive and zero weights", []*l3bpb.RealServer{second, first}, []bool{true, true}, []uint32{0, 3}, []uint32{1, 1, 1}},
		{"new key cannot inherit enabled position", []*l3bpb.RealServer{third, second, first}, []bool{false, true, true}, []uint32{1, 0, 3}, []uint32{2, 2, 2}},
		{"committed removal forgets the override", []*l3bpb.RealServer{third, second}, []bool{false, true}, []uint32{1, 0}, []uint32{}},
		{"readded key starts disabled with default weight", []*l3bpb.RealServer{first, second, third}, []bool{false, true, false}, []uint32{1, 0, 1}, []uint32{}},
		{"empty membership clears selection", nil, nil, nil, []uint32{}},
		{"readdition after empty has no survivors", []*l3bpb.RealServer{first, second}, []bool{false, false}, []uint32{1, 1}, []uint32{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request.RealServers = tc.reals
			require.NoError(t, backend.UpdateService(request))
			for _, enabled := range operations.ConstructedEnabled {
				require.False(t, enabled)
			}
			requireRealStates(t, backend, operations.Service, tc.enabled, tc.weights, tc.ring)
		})
	}
	require.NoError(t, backend.UpdateRealServerState("vs0", 0, true))
	require.NoError(t, backend.UpdateRealServerWeight("vs0", 0, 7))
	require.NoError(t, backend.UpdateRealServerWeight("vs0", 1, 5))
	first.SourceNetwork = commonpb.NewIPNetworkFrom(xnetip.MustParseNetwork("203.0.113.0/24"))
	require.NoError(t, backend.UpdateService(request))
	requireRealStates(t, backend, operations.Service, []bool{true, false}, []uint32{7, 5}, []uint32{0, 0, 0, 0, 0, 0, 0})
	info, err := operations.Service.Inspect()
	require.NoError(t, err)
	require.Equal(t, xnetip.MustParseNetwork("203.0.113.0/24"), info.RealServers[0].SourceNet)
	response, err := backend.GetService("vs0")
	require.NoError(t, err)
	source, err := response.RealServers[0].SourceNetwork.ToNetwork()
	require.NoError(t, err)
	require.Equal(t, info.RealServers[0].SourceNet, source)
	require.NoError(t, backend.DeleteService("vs0"))
	require.NoError(t, backend.CreateService(request))
	requireRealStates(t, backend, operations.Service, []bool{false, false}, []uint32{1, 1}, []uint32{})
}

// Test_Backend_Replacement_FailurePreservesRealState verifies that unpublished
// candidates cannot change selection or the source of later state carryover.
func Test_Backend_Replacement_FailurePreservesRealState(t *testing.T) {
	for _, failure := range []string{"set-state", "update-ring", "publish-service"} {
		t.Run(failure, func(t *testing.T) {
			agent := newTestAgent(t)
			operations := &capturingServiceOps{SharedMemoryOps: controlplane.NewSharedMemoryOps(agent), Agent: agent}
			backend := controlplane.NewBackendWithSharedMemory(operations)
			request := serviceWithRealServer("vs0")
			require.NoError(t, backend.CreateService(request))
			require.NoError(t, backend.UpdateRealServerState("vs0", 0, true))
			require.NoError(t, backend.UpdateRealServerWeight("vs0", 0, 3))
			previous := operations.Service
			operations.Fail = failure
			capacity := agent.BlockAllocatorFreeSize()
			for range 3 {
				require.ErrorIs(t, backend.UpdateService(request), errFakeRing)
				require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
				requireRealStates(t, backend, previous, []bool{true}, []uint32{3}, []uint32{0, 0, 0})
			}

			operations.Fail = "publish-service"
			require.ErrorIs(t, backend.UpdateService(sampleService("vs0")), errFakeRing)
			operations.Fail = ""
			require.NoError(t, backend.UpdateService(request))
			requireRealStates(t, backend, operations.Service, []bool{true}, []uint32{3}, []uint32{0, 0, 0})
			require.NoError(t, backend.UpdateService(sampleService("vs0")))
			operations.Fail = "publish-service"
			require.ErrorIs(t, backend.UpdateService(request), errFakeRing)
			operations.Fail = ""
			require.NoError(t, backend.UpdateService(request))
			requireRealStates(t, backend, operations.Service, []bool{false}, []uint32{1}, []uint32{})
		})
	}
}

// Test_Backend_RealKeys_RejectDuplicatesBeforeAllocation verifies that duplicate
// destinations reject whole candidates while native and mapped IPv4 stay distinct.
func Test_Backend_RealKeys_RejectDuplicatesBeforeAllocation(t *testing.T) {
	for _, address := range []string{"192.0.2.1", "2001:db8::1", "::ffff:192.0.2.1"} {
		t.Run(address, func(t *testing.T) {
			operations := &fakeSharedMemory{}
			backend := controlplane.NewBackendWithSharedMemory(operations)
			request := serviceWithRealServer("vs0")
			real := request.RealServers[0]
			real.DestinationAddress = netip.MustParseAddr(address).AsSlice()
			if netip.MustParseAddr(address).Is6() {
				real.SourceNetwork = commonpb.NewIPNetworkFrom(xnetip.MustParseNetwork("2001:db8::/64"))
			}
			request.RealServers = append(request.RealServers, real)
			require.Equal(t, codes.InvalidArgument, status.Code(backend.CreateService(request)))
			require.Empty(t, operations.Tables)
			require.Empty(t, operations.Services)
			request.RealServers = request.RealServers[:1]
			require.NoError(t, backend.CreateService(request))
			require.NoError(t, backend.UpdateRealServerWeight("vs0", 0, 7))
			request.RealServers = append(request.RealServers, real)
			require.Equal(t, codes.InvalidArgument, status.Code(backend.UpdateService(request)))
			require.Len(t, operations.Services, 1)
			response, err := backend.GetService("vs0")
			require.NoError(t, err)
			require.Equal(t, uint32(7), response.RealServers[0].Weight)
		})
	}
	operations := &fakeSharedMemory{}
	backend := controlplane.NewBackendWithSharedMemory(operations)
	request := serviceWithRealServer("vs0")
	require.NoError(t, backend.CreateService(request))
	require.NoError(t, backend.UpdateRealServerState("vs0", 0, true))
	request.RealServers = append(request.RealServers, &l3bpb.RealServer{
		DestinationAddress: netip.MustParseAddr("::ffff:192.0.2.1").AsSlice(),
		SourceNetwork:      commonpb.NewIPNetworkFrom(xnetip.MustParseNetwork("2001:db8::/64")),
	})
	require.NoError(t, backend.UpdateService(request))
	response, err := backend.GetService("vs0")
	require.NoError(t, err)
	require.True(t, response.RealServers[0].Enabled)
	require.False(t, response.RealServers[1].Enabled)
}
