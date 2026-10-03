package l3b

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"sync"

	"google.golang.org/protobuf/proto"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/bindings/go/filterpbconv/v1"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/l3b/bindings/go/cl3b"
	l3bpb "github.com/yanet-platform/yanet2/modules/l3b/controlplane/l3bpb/v1"
	cl3bobject "github.com/yanet-platform/yanet2/objects/l3b/bindings/go/cl3bobject"
)

// Backend abstracts the shared-memory operations behind the l3b service.
type Backend interface {
	// CreateService creates a named virtual service.
	CreateService(service *l3bpb.VirtualService) error
	// UpdateService replaces an existing named virtual service.
	UpdateService(service *l3bpb.VirtualService) error
	// DeleteService removes a named virtual service.
	DeleteService(name string) error
	// ListServices returns the names of all virtual services.
	ListServices() []string
	// UpdateModuleConfig installs destination filters and services into a
	// named module configuration and publishes it.
	UpdateModuleConfig(config *l3bpb.ModuleConfig) error
	// ListModuleConfigs returns the names of all module configurations.
	ListModuleConfigs() []string
	// UpdateRealServerState enables or disables a real server within a
	// named virtual service.
	UpdateRealServerState(service string, realServerIndex uint32, enabled bool) error
	// UpdateRealServerWeight sets the weight of a real server within a named
	// virtual service and rebuilds its scheduler ring.
	UpdateRealServerWeight(service string, realServerIndex uint32, weight uint32) error
	// ListSessions pages through the session records of a named virtual
	// service. Returns the page, the continuation token (0 when complete)
	// and the dataplane time the deadlines are relative to.
	ListSessions(
		service string,
		cursor uint64,
		limit uint32,
	) ([]cl3bobject.Session, uint64, uint64, error)
	// GetService inspects a named virtual service: scheduler masks and
	// the live real servers with addresses, weights and states.
	GetService(service string) (*l3bpb.GetServiceResponse, error)
}

// freeable is anything this backend owns whose destruction a live
// configuration generation can refuse.
type freeable interface {
	Free() error
}

// SessionTableHandle is a session table owned by the backend.
type SessionTableHandle interface {
	Free() error
}

// VirtualServiceHandle is a service object owned by the backend.
type VirtualServiceHandle interface {
	Free() error
	Publish() error
	UpdateRing([]uint32) error
	SetRealServerState(uint32, bool) error
	ReadSessions(uint64, uint32) ([]cl3bobject.Session, uint64, uint64, error)
}

// ModuleHandle is a module configuration owned by the backend.
type ModuleHandle interface {
	Free() error
	Update([]cl3b.DestinationFilterRule) error
	AsFFIModule() ffi.ModuleConfig
}

// SharedMemoryOps is the allocation and publication boundary used by Backend.
type SharedMemoryOps interface {
	WorkerCount() uint32
	CreateSessionTable(string, uint16, uint32) (SessionTableHandle, error)
	CreateVirtualService(string, cl3bobject.VirtualServiceConfig, SessionTableHandle) (VirtualServiceHandle, error)
	DeleteVirtualService(string) error
	NewModuleConfig(string) (ModuleHandle, error)
	UpdateModules([]ffi.ModuleConfig) error
}

type agentOps struct{ agent *ffi.Agent }

func (m agentOps) WorkerCount() uint32 { return m.agent.DPConfig().WorkerCount() }

func (m agentOps) CreateSessionTable(name string, workers uint16, size uint32) (SessionTableHandle, error) {
	return cl3bobject.CreateSessionTable(m.agent, name, workers, size)
}

func (m agentOps) CreateVirtualService(name string, config cl3bobject.VirtualServiceConfig, table SessionTableHandle) (VirtualServiceHandle, error) {
	sharedTable, ok := table.(*cl3bobject.SessionTableObject)
	if !ok {
		return nil, fmt.Errorf("invalid session table handle for virtual service %q: %T", name, table)
	}
	object, err := cl3bobject.CreateVirtualService(m.agent, name, config, sharedTable)
	if err != nil {
		return nil, err
	}
	return agentService{object: object, agent: m.agent}, nil
}

func (m agentOps) DeleteVirtualService(name string) error {
	return cl3bobject.DeleteVirtualService(m.agent, name)
}

func (m agentOps) NewModuleConfig(name string) (ModuleHandle, error) {
	return cl3b.NewModuleConfig(m.agent, name)
}

func (m agentOps) UpdateModules(modules []ffi.ModuleConfig) error {
	return m.agent.UpdateModules(modules)
}

type agentService struct {
	object *cl3bobject.VirtualServiceObject
	agent  *ffi.Agent
}

func (m agentService) Free() error                       { return m.object.Free() }
func (m agentService) Publish() error                    { return m.object.Publish(m.agent) }
func (m agentService) UpdateRing(indexes []uint32) error { return m.object.UpdateRing(indexes) }
func (m agentService) SetRealServerState(index uint32, enabled bool) error {
	return m.object.SetRealServerState(index, enabled)
}
func (m agentService) ReadSessions(cursor uint64, limit uint32) ([]cl3bobject.Session, uint64, uint64, error) {
	return m.object.ReadSessions(m.agent, cursor, limit)
}

type retainedHandle struct {
	handle  freeable
	readers int
}

// Free retries destruction after admitted readers have left.
func (m *retainedHandle) Free() error { return m.handle.Free() }

// HasReaders reports whether a Go reader still uses the handle.
func (m *retainedHandle) HasReaders() bool { return m.readers != 0 }

// Admit records one reader of the handle.
func (m *retainedHandle) Admit() { m.readers++ }

// Release drops one reader of the handle.
func (m *retainedHandle) Release() {
	m.readers--
}

// VirtualService returns the service carried by this retained handle.
func (m *retainedHandle) VirtualService() VirtualServiceHandle {
	return m.handle.(VirtualServiceHandle)
}

// SessionTable returns the table carried by this retained handle.
func (m *retainedHandle) SessionTable() SessionTableHandle {
	return m.handle.(SessionTableHandle)
}

type managedService struct {
	object *retainedHandle
	// The table outlives every generation published under this name and
	// is destroyed only when the named service is deleted.
	sessionTable *retainedHandle
	config       cl3bobject.VirtualServiceConfig
	// weights[i] is the configured weight of real server i.
	weights []uint32
	// enabled[i] is whether real server i takes traffic; disabled servers
	// leave the scheduler ring so their share shifts to the enabled ones.
	enabled []bool
}

// Replace installs a fresh service and returns the object it superseded.
func (m *managedService) Replace(
	object *retainedHandle,
	config cl3bobject.VirtualServiceConfig,
	weights []uint32,
) *retainedHandle {
	previous := m.object
	m.object = object
	m.config = config
	m.weights = weights
	m.enabled = make([]bool, len(weights))
	for idx := range m.enabled {
		m.enabled[idx] = true
	}
	return previous
}

// PublishTarget returns the service object currently serving traffic.
func (m *managedService) PublishTarget() VirtualServiceHandle {
	return m.object.VirtualService()
}

// SessionTable returns the table every service published under this name pins
// flows into.
func (m *managedService) SessionTable() SessionTableHandle {
	return m.sessionTable.SessionTable()
}

// AdmitSessionRead leases both objects reached by a session read.
func (m *managedService) AdmitSessionRead() (*retainedHandle, *retainedHandle) {
	m.object.Admit()
	m.sessionTable.Admit()
	return m.object, m.sessionTable
}

// Handles returns both objects owned by a deleted service.
func (m *managedService) Handles() (*retainedHandle, *retainedHandle) {
	return m.object, m.sessionTable
}

var errRealServerIndexOutOfRange = errors.New("real server index out of range")

// SetRealServerState enables or disables a single real server, mirrors the
// state into the service object and rebuilds the ring over the enabled
// servers only.
func (m *managedService) SetRealServerState(
	realServerIndex uint32,
	enabled bool,
) error {
	if int(realServerIndex) >= len(m.enabled) {
		return fmt.Errorf("%w: %d", errRealServerIndexOutOfRange, realServerIndex)
	}

	if err := m.PublishTarget().SetRealServerState(realServerIndex, enabled); err != nil {
		return err
	}

	m.enabled[realServerIndex] = enabled
	return m.rebuildRing()
}

// SetRealServerWeight clamps the weight into range, applies it and rebuilds
// the scheduler ring of the published object.
func (m *managedService) SetRealServerWeight(
	realServerIndex uint32,
	weight uint32,
) error {
	if int(realServerIndex) >= len(m.weights) {
		return fmt.Errorf("%w: %d", errRealServerIndexOutOfRange, realServerIndex)
	}

	if weight > maxRealServerWeight {
		weight = maxRealServerWeight
	}

	m.weights[realServerIndex] = weight
	return m.rebuildRing()
}

// rebuildRing installs a ring over the enabled servers only, each with its
// configured weight; an empty ring drops new flows until a server is enabled.
func (m *managedService) rebuildRing() error {
	effective := make([]uint32, len(m.weights))
	for idx, weight := range m.weights {
		if m.enabled[idx] {
			effective[idx] = weight
		}
	}
	return m.PublishTarget().UpdateRing(RingFromWeights(effective))
}

// Snapshot builds an owned response from the writer's applied state.
func (m *managedService) Snapshot() *l3bpb.GetServiceResponse {
	realServers := make([]*l3bpb.RealServerState, 0, len(m.config.RealServers))
	for idx, real := range m.config.RealServers {
		realServers = append(realServers, &l3bpb.RealServerState{
			DestinationAddress: real.DestinationAddress.AsSlice(),
			SourceNetwork:      commonpb.NewIPNetworkFrom(real.SourceNet),
			Weight:             m.weights[idx],
			Enabled:            m.enabled[idx],
		})
	}
	return &l3bpb.GetServiceResponse{
		HashMask:    m.config.HashMask,
		IndexMask:   m.config.IndexMask,
		RealServers: realServers,
	}
}

type managedConfig struct {
	module ModuleHandle
}

// FFIModule returns the shared-memory handle of the module configuration.
func (m *managedConfig) FFIModule() ffi.ModuleConfig {
	return m.module.AsFFIModule()
}

// Retire returns the module configuration for deferred destruction.
func (m *managedConfig) Retire() freeable {
	return m.module
}

// backend is the real Backend backed by shared memory.
type backend struct {
	operations SharedMemoryOps

	writerMu  sync.Mutex
	mu        sync.RWMutex
	services  map[string]*managedService
	snapshots map[string]*l3bpb.GetServiceResponse
	configs   map[string]*managedConfig

	// Retired handles wait for both Go readers and C generations to drain.
	deferred []*retainedHandle
}

// NewBackend creates a Backend that operates on real shared memory.
func NewBackend(agent *ffi.Agent) Backend {
	return NewBackendWithSharedMemory(NewSharedMemoryOps(agent))
}

// NewSharedMemoryOps adapts a real agent to the backend operation boundary.
func NewSharedMemoryOps(agent *ffi.Agent) SharedMemoryOps {
	return agentOps{agent: agent}
}

// NewBackendWithSharedMemory creates a backend over shared-memory operations.
func NewBackendWithSharedMemory(operations SharedMemoryOps) Backend {
	return &backend{
		operations: operations,
		services:   map[string]*managedService{},
		snapshots:  map[string]*l3bpb.GetServiceResponse{},
		configs:    map[string]*managedConfig{},
	}
}

// reclaimDeferred retries retired handles once their Go readers have drained.
// The caller holds the writer lock, and no reader lock spans a free.
func (m *backend) reclaimDeferred() {
	m.mu.Lock()
	ready := make([]*retainedHandle, 0, len(m.deferred))
	kept := m.deferred[:0]
	for _, handle := range m.deferred {
		if !handle.HasReaders() {
			ready = append(ready, handle)
		} else {
			kept = append(kept, handle)
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
	m.mu.Unlock()

	for _, handle := range ready {
		if isStillReferenced(handle.Free()) {
			m.mu.Lock()
			m.deferred = append(m.deferred, handle)
			m.mu.Unlock()
		}
	}
}

func isStillReferenced(err error) bool {
	return errors.Is(err, ffi.ErrStillReferenced)
}

// deferOrFree retires a handle and attempts serialized reclamation.
// The caller holds the writer lock.
func (m *backend) deferOrFree(handle freeable) {
	m.retire(&retainedHandle{handle: handle})
	m.reclaimDeferred()
}

func (m *backend) retire(handle *retainedHandle) {
	m.mu.Lock()
	m.deferred = append(m.deferred, handle)
	m.mu.Unlock()
}

func (m *backend) releaseSessionRead(service, table *retainedHandle) {
	m.mu.Lock()
	defer m.mu.Unlock()
	service.Release()
	table.Release()
}

func (m *backend) service(name string) (*managedService, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	service, ok := m.services[name]
	return service, ok
}

func (m *backend) CreateService(service *l3bpb.VirtualService) error {
	name := service.GetName()

	m.writerMu.Lock()
	defer m.writerMu.Unlock()

	if _, ok := m.service(name); ok {
		return status.Errorf(codes.AlreadyExists, "virtual service %q already exists", name)
	}

	m.reclaimDeferred()

	// The descriptor is validated before any shared memory is taken, so a
	// rejected request costs the agent's arena nothing.
	config, err := buildVirtualServiceConfig(service)
	if err != nil {
		return err
	}

	workerCount := m.operations.WorkerCount()
	table, err := m.operations.CreateSessionTable(
		name, uint16(workerCount), service.GetSessionIndexSize(),
	)
	if err != nil {
		return fmt.Errorf("failed to create session table %q: %w", name, err)
	}

	object, weights, err := m.publishService(config, name, table)
	if err != nil {
		// A failed candidate leaves its table outside the published view.
		m.deferOrFree(table)
		return err
	}

	managed := &managedService{sessionTable: &retainedHandle{handle: table}}
	managed.Replace(&retainedHandle{handle: object}, config, weights)
	m.mu.Lock()
	m.services[name] = managed
	m.snapshots[name] = managed.Snapshot()
	m.mu.Unlock()
	return nil
}

func (m *backend) UpdateService(service *l3bpb.VirtualService) error {
	name := service.GetName()

	m.writerMu.Lock()
	defer m.writerMu.Unlock()

	existing, ok := m.service(name)
	if !ok {
		return status.Errorf(codes.NotFound, "virtual service %q not found", name)
	}

	m.reclaimDeferred()

	config, err := buildVirtualServiceConfig(service)
	if err != nil {
		return err
	}

	// The replacement borrows the live session table, so pinned flows keep
	// their real servers across the update.
	object, weights, err := m.publishService(config, name, existing.SessionTable())
	if err != nil {
		return err
	}

	// The upsert swapped the registry slot atomically; the superseded
	// service object stays alive until its generations drain.
	m.mu.Lock()
	oldObject := existing.Replace(&retainedHandle{handle: object}, config, weights)
	m.snapshots[name] = existing.Snapshot()
	m.mu.Unlock()
	m.retire(oldObject)
	m.reclaimDeferred()
	return nil
}

// publishService builds a fresh virtual service object under the given name,
// installs its default scheduler ring and upserts it into the dataplane.
//
// The service borrows the session table, and the caller holds the writer lock.
func (m *backend) publishService(
	config cl3bobject.VirtualServiceConfig,
	name string,
	table SessionTableHandle,
) (VirtualServiceHandle, []uint32, error) {
	object, err := m.operations.CreateVirtualService(name, config, table)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create virtual service %q: %w", name, err)
	}

	weights := defaultWeights(len(config.RealServers))
	if err := object.UpdateRing(RingFromWeights(weights)); err != nil {
		m.deferOrFree(object)
		return nil, nil, fmt.Errorf("failed to populate real server ring: %w", err)
	}

	if err := object.Publish(); err != nil {
		m.deferOrFree(object)
		return nil, nil, fmt.Errorf("failed to publish virtual service %q: %w", name, err)
	}

	return object, weights, nil
}

func (m *backend) DeleteService(name string) error {
	m.writerMu.Lock()
	defer m.writerMu.Unlock()

	existing, ok := m.service(name)
	if !ok {
		return status.Errorf(codes.NotFound, "virtual service %q not found", name)
	}

	if err := m.operations.DeleteVirtualService(name); err != nil {
		return fmt.Errorf("failed to delete virtual service %q: %w", name, err)
	}

	// The service and table remain owned until readers and generations drain.
	m.mu.Lock()
	delete(m.services, name)
	delete(m.snapshots, name)
	m.mu.Unlock()
	object, table := existing.Handles()
	m.retire(object)
	m.retire(table)
	m.reclaimDeferred()
	return nil
}

func (m *backend) ListServices() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.services))
	for name := range m.services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *backend) UpdateModuleConfig(config *l3bpb.ModuleConfig) error {
	name := config.GetName()

	m.writerMu.Lock()
	defer m.writerMu.Unlock()

	// Resolve the service each rule names before building anything; the
	// rules themselves carry the names into the module configuration.
	rules := make([]cl3b.DestinationFilterRule, 0, len(config.GetDestinationFilterRules()))
	for _, rule := range config.GetDestinationFilterRules() {
		serviceName := rule.GetService()
		if _, ok := m.service(serviceName); !ok {
			return status.Errorf(codes.NotFound, "unknown virtual service %q", serviceName)
		}

		net6s, err := filterpbconv.ToNet6sFromNetworks(rule.GetNet6S())
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "invalid net6s: %s", status.Convert(err).Message())
		}
		net4s, err := filterpbconv.ToNet4sFromNetworks(rule.GetNet4S())
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "invalid net4s: %s", status.Convert(err).Message())
		}
		protoRanges, err := filterpbconv.ToProtoRanges(rule.GetProtoRanges())
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "invalid proto ranges: %s", status.Convert(err).Message())
		}

		rules = append(rules, cl3b.DestinationFilterRule{
			Net6s:          net6s,
			Net4s:          net4s,
			ProtoRanges:    protoRanges,
			VirtualService: serviceName,
		})
	}

	m.reclaimDeferred()

	// A module configuration is immutable once published: build a fresh one
	// per update and retire the module it replaces.
	module, err := m.operations.NewModuleConfig(name)
	if err != nil {
		return fmt.Errorf("failed to create module config %q: %w", name, err)
	}
	if err := module.Update(rules); err != nil {
		m.deferOrFree(module)
		return fmt.Errorf("failed to update module config %q: %w", name, err)
	}

	managed := &managedConfig{module: module}
	previous, ok := m.configs[name]

	modules := make([]ffi.ModuleConfig, 0, len(m.configs)+1)
	for configName, current := range m.configs {
		if configName != name {
			modules = append(modules, current.FFIModule())
		}
	}
	modules = append(modules, managed.FFIModule())
	if err := m.operations.UpdateModules(modules); err != nil {
		m.deferOrFree(module)
		return fmt.Errorf("failed to update module config %q: %w", name, err)
	}

	m.mu.Lock()
	m.configs[name] = managed
	m.mu.Unlock()
	if ok {
		m.deferOrFree(previous.Retire())
	}
	return nil
}

func (m *backend) ListModuleConfigs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.configs))
	for name := range m.configs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *backend) UpdateRealServerState(
	service string,
	realServerIndex uint32,
	enabled bool,
) error {
	m.writerMu.Lock()
	defer m.writerMu.Unlock()

	existing, ok := m.service(service)
	if !ok {
		return status.Errorf(codes.NotFound, "virtual service %q not found", service)
	}

	m.reclaimDeferred()

	err := existing.SetRealServerState(realServerIndex, enabled)
	m.mu.Lock()
	m.snapshots[service] = existing.Snapshot()
	m.mu.Unlock()
	if err != nil {
		code := codes.Internal
		if errors.Is(err, errRealServerIndexOutOfRange) {
			code = codes.InvalidArgument
		}
		return status.Errorf(
			code,
			"failed to set the state of real server %d of %q: %v", realServerIndex, service, err,
		)
	}
	return nil
}

func (m *backend) UpdateRealServerWeight(
	service string,
	realServerIndex uint32,
	weight uint32,
) error {
	m.writerMu.Lock()
	defer m.writerMu.Unlock()

	existing, ok := m.service(service)
	if !ok {
		return status.Errorf(codes.NotFound, "virtual service %q not found", service)
	}

	m.reclaimDeferred()

	err := existing.SetRealServerWeight(realServerIndex, weight)
	m.mu.Lock()
	m.snapshots[service] = existing.Snapshot()
	m.mu.Unlock()
	if err != nil {
		code := codes.Internal
		if errors.Is(err, errRealServerIndexOutOfRange) {
			code = codes.InvalidArgument
		}
		return status.Errorf(
			code,
			"failed to set the weight of real server %d of %q: %v", realServerIndex, service, err,
		)
	}
	return nil
}

// familyLabel names the address family of an address for error messages.
func familyLabel(address netip.Addr) string {
	if address.Is4() {
		return "IPv4"
	}
	return "IPv6"
}

func (m *backend) ListSessions(
	service string,
	cursor uint64,
	limit uint32,
) ([]cl3bobject.Session, uint64, uint64, error) {
	m.mu.Lock()

	existing, ok := m.services[service]
	if !ok {
		m.mu.Unlock()
		return nil, 0, 0, status.Errorf(codes.NotFound, "virtual service %q not found", service)
	}
	object, table := existing.AdmitSessionRead()
	m.mu.Unlock()
	defer m.releaseSessionRead(object, table)

	if limit == 0 {
		limit = defaultSessionPageLimit
	}
	if limit > maxSessionPageLimit {
		limit = maxSessionPageLimit
	}
	return object.VirtualService().ReadSessions(cursor, limit)
}

func (m *backend) GetService(service string) (*l3bpb.GetServiceResponse, error) {
	m.mu.RLock()
	snapshot, ok := m.snapshots[service]
	m.mu.RUnlock()
	if !ok {
		return nil, status.Errorf(codes.NotFound, "virtual service %q not found", service)
	}
	return proto.Clone(snapshot).(*l3bpb.GetServiceResponse), nil
}

const (
	// defaultSessionPageLimit bounds a ListSessions page when the caller
	// asks for no explicit limit.
	defaultSessionPageLimit = 1000
	// maxSessionPageLimit caps a requested page so a huge limit cannot
	// translate into a huge allocation.
	maxSessionPageLimit = 100000
)

// maxRealServerWeight is the upper bound on a real server weight; the per-ring
// capacity is sized so every server could max out at once.
const maxRealServerWeight uint32 = 1000

// defaultWeights returns one weight per real server, defaulting to 1.
func defaultWeights(realServerCount int) []uint32 {
	weights := make([]uint32, realServerCount)
	for idx := range weights {
		weights[idx] = 1
	}
	return weights
}

// RingFromWeights expands per-server weights into a weighted-round-robin
// scheduler ring: each server index appears as many times as its weight, but
// the occurrences are interleaved so a heavy server is spread evenly across
// the ring rather than clustered.
func RingFromWeights(weights []uint32) []uint32 {
	var total uint32
	for _, weight := range weights {
		total += weight
	}
	if total == 0 {
		return nil
	}

	// Classic interleaved WRR: accumulate each weight every step, emit the
	// server with the largest accumulated value, then subtract the total from
	// it. This yields an evenly distributed sequence.
	current := make([]int64, len(weights))
	ring := make([]uint32, 0, total)
	for range total {
		for idx := range weights {
			current[idx] += int64(weights[idx])
		}

		best := 0
		for idx := range weights {
			if current[idx] > current[best] {
				best = idx
			}
		}

		ring = append(ring, uint32(best))
		current[best] -= int64(total)
	}
	return ring
}

func buildVirtualServiceConfig(
	service *l3bpb.VirtualService,
) (cl3bobject.VirtualServiceConfig, error) {
	invalid := func(reason string) error {
		return status.Errorf(codes.InvalidArgument, "invalid virtual service %q: %s", service.GetName(), reason)
	}

	policy := service.GetSessionTimeouts()

	realServers := make([]cl3bobject.RealServer, 0, len(service.GetRealServers()))
	for _, server := range service.GetRealServers() {
		destinationAddress, ok := netip.AddrFromSlice(server.GetDestinationAddress())
		if !ok {
			return cl3bobject.VirtualServiceConfig{}, invalid("real server destination address is not a valid 4- or 16-byte IP address")
		}

		sourceNet, err := server.GetSourceNetwork().ToNetwork()
		if err != nil {
			return cl3bobject.VirtualServiceConfig{}, invalid(fmt.Sprintf("invalid real server source network: %v", err))
		}

		family := cl3bobject.IPv4
		if destinationAddress.Is6() {
			family = cl3bobject.IPv6
		}

		// The tunnel family selects which union members the dataplane
		// serializes; a mixed pair would read the wrong bytes or panic
		// on the address conversion below.
		sourceNetAddr := sourceNet.Addr()
		if sourceNetAddr.Is4() != destinationAddress.Is4() {
			return cl3bobject.VirtualServiceConfig{}, invalid(fmt.Sprintf(
				"real server %d pairs a %s destination with a %s source network",
				len(realServers), familyLabel(destinationAddress), familyLabel(sourceNetAddr),
			))
		}

		realServers = append(realServers, cl3bobject.RealServer{
			Type:               family,
			DestinationAddress: destinationAddress,
			SourceNet:          sourceNet,
		})
	}

	sourceFilterRules := make([]cl3bobject.SourceFilterRule, 0, len(service.GetSourceFilterRules()))
	for _, rule := range service.GetSourceFilterRules() {
		net6s, err := filterpbconv.ToNet6sFromNetworks(rule.GetNet6S())
		if err != nil {
			return cl3bobject.VirtualServiceConfig{}, invalid(fmt.Sprintf("invalid source net6s: %s", status.Convert(err).Message()))
		}
		net4s, err := filterpbconv.ToNet4sFromNetworks(rule.GetNet4S())
		if err != nil {
			return cl3bobject.VirtualServiceConfig{}, invalid(fmt.Sprintf("invalid source net4s: %s", status.Convert(err).Message()))
		}
		portRanges, err := filterpbconv.ToPortRanges(rule.GetPortRanges())
		if err != nil {
			return cl3bobject.VirtualServiceConfig{}, invalid(fmt.Sprintf("invalid source port ranges: %s", status.Convert(err).Message()))
		}

		sourceFilterRules = append(sourceFilterRules, cl3bobject.SourceFilterRule{
			Net6s:      net6s,
			Net4s:      net4s,
			PortRanges: portRanges,
		})
	}

	return cl3bobject.VirtualServiceConfig{
		SourceFilterRules: sourceFilterRules,
		RealServers:       realServers,
		HashMask:          service.GetHashMask(),
		IndexMask:         service.GetIndexMask(),
		RingCapacity:      uint32(len(realServers)) * maxRealServerWeight,
		SchedulerFlags:    service.GetSchedulerFlags(),
		Flags:             service.GetFlags(),
		DSCPFlags:         service.GetDscpFlags(),
		SessionTimeouts: cl3bobject.SessionTimeouts{
			TCP:       policy.GetTcp(),
			TCPSyn:    policy.GetTcpSyn(),
			TCPSynAck: policy.GetTcpSynAck(),
			TCPFin:    policy.GetTcpFin(),
			UDP:       policy.GetUdp(),
			Other:     policy.GetOther(),
		},
	}, nil
}
