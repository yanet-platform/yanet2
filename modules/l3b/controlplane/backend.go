package l3b

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"sync"

	"github.com/yanet-platform/xnetip"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/bindings/go/filterpbconv/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
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
}

// freeable is anything this backend owns whose destruction a live
// configuration generation can refuse.
type freeable interface {
	Free() error
}

type managedService struct {
	object *cl3bobject.VirtualServiceObject
	// weights[i] is the configured weight of real server i.
	weights []uint32
	// enabled[i] is whether real server i takes traffic; disabled servers
	// leave the scheduler ring so their share shifts to the enabled ones.
	enabled []bool
}

// Replace swaps in the object, weights and states of an updated service.
func (m *managedService) Replace(
	object *cl3bobject.VirtualServiceObject,
	weights []uint32,
) {
	m.object = object
	m.weights = weights
	m.enabled = make([]bool, len(weights))
	for idx := range m.enabled {
		m.enabled[idx] = true
	}
}

// serviceFreeable retires only the service object of a handle, leaving its
// session table alive for the handle that adopted it.
type serviceFreeable struct {
	object *cl3bobject.VirtualServiceObject
}

// Free implements the deferred-handle contract.
func (m serviceFreeable) Free() error {
	return m.object.RetireService()
}

// PublishTarget returns the handle whose session table an update adopts.
func (m *managedService) PublishTarget() *cl3bobject.VirtualServiceObject {
	return m.object
}

// Retire returns the currently published service for deferred destruction;
// its session table stays alive, adopted by the replacement handle.
func (m *managedService) Retire() freeable {
	return serviceFreeable{object: m.object}
}

// SetRealServerState enables or disables a single real server of the
// published object.
func (m *managedService) SetRealServerState(
	realServerIndex uint32,
	enabled bool,
) error {
	return m.object.SetRealServerState(realServerIndex, enabled)
}

// SetRealServerWeight clamps the weight into range, applies it and rebuilds
// the scheduler ring of the published object.
func (m *managedService) SetRealServerWeight(
	realServerIndex uint32,
	weight uint32,
) error {
	if int(realServerIndex) >= len(m.weights) {
		return fmt.Errorf("real server index %d out of range", realServerIndex)
	}

	if weight > maxRealServerWeight {
		weight = maxRealServerWeight
	}

	m.weights[realServerIndex] = weight
	return m.object.UpdateRing(RingFromWeights(m.weights))
}

type managedConfig struct {
	module *cl3b.ModuleConfig
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
	agent *ffi.Agent

	mu       sync.Mutex
	services map[string]*managedService
	configs  map[string]*managedConfig

	// deferred holds superseded objects and module configs whose free was
	// refused because a live configuration generation still referenced
	// them. This backend is their owner: it retries them on its next
	// mutating call.
	deferred []freeable
}

// NewBackend creates a Backend that operates on real shared memory.
func NewBackend(agent *ffi.Agent) Backend {
	return &backend{
		agent:    agent,
		services: map[string]*managedService{},
		configs:  map[string]*managedConfig{},
	}
}

// reclaimDeferred retries every deferred handle, dropping the ones whose
// generations have drained and keeping the rest deferred. The caller must hold
// the backend mutex.
func (m *backend) reclaimDeferred() {
	kept := m.deferred[:0]
	for _, handle := range m.deferred {
		if err := handle.Free(); isStillReferenced(err) {
			kept = append(kept, handle)
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}

func isStillReferenced(err error) bool {
	return errors.Is(err, ffi.ErrStillReferenced)
}

// deferOrFree retires a superseded handle immediately when its generations
// have drained, and otherwise parks it for a later retry. The caller must hold
// the backend mutex.
func (m *backend) deferOrFree(handle freeable) {
	if isStillReferenced(handle.Free()) {
		m.deferred = append(m.deferred, handle)
	}
}

func (m *backend) CreateService(service *l3bpb.VirtualService) error {
	name := service.GetName()

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.services[name]; ok {
		return status.Errorf(codes.AlreadyExists, "virtual service %q already exists", name)
	}

	m.reclaimDeferred()

	object, weights, err := m.publishService(service, name, nil)
	if err != nil {
		return err
	}

	managed := &managedService{}
	managed.Replace(object, weights)
	m.services[name] = managed
	return nil
}

func (m *backend) UpdateService(service *l3bpb.VirtualService) error {
	name := service.GetName()

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.services[name]
	if !ok {
		return status.Errorf(codes.NotFound, "virtual service %q not found", name)
	}

	m.reclaimDeferred()

	// The replacement adopts the published session table, so pinned flows
	// keep their real servers across the update.
	object, weights, err := m.publishService(service, name, existing.PublishTarget())
	if err != nil {
		return err
	}

	// The upsert swapped the registry slot atomically; the superseded
	// service object stays alive until its generations drain, while its
	// session table lives on in the replacement handle.
	m.deferOrFree(existing.Retire())

	existing.Replace(object, weights)
	return nil
}

// publishService builds a fresh virtual service object under the given name,
// installs its default scheduler ring and upserts it into the dataplane. On an
// update (previous non-nil) the new service adopts the existing session
// table, so every pinned flow keeps its real server. The caller must hold the
// backend mutex.
func (m *backend) publishService(
	service *l3bpb.VirtualService,
	name string,
	previous *cl3bobject.VirtualServiceObject,
) (*cl3bobject.VirtualServiceObject, []uint32, error) {
	config, err := buildVirtualServiceConfig(service)
	if err != nil {
		return nil, nil, err
	}

	// The session table's per-worker sizing must cover every worker.
	workerCount := m.agent.DPConfig().WorkerCount()

	object, err := cl3bobject.CreateVirtualService(m.agent, name, uint16(workerCount), config, previous)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create virtual service %q: %w", name, err)
	}

	weights := defaultWeights(len(config.RealServers))
	if err := object.UpdateRing(RingFromWeights(weights)); err != nil {
		_ = object.Free()
		return nil, nil, fmt.Errorf("failed to populate real server ring: %w", err)
	}

	if err := object.Publish(m.agent); err != nil {
		_ = object.Free()
		return nil, nil, fmt.Errorf("failed to publish virtual service %q: %w", name, err)
	}

	return object, weights, nil
}

func (m *backend) DeleteService(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.services[name]
	if !ok {
		return status.Errorf(codes.NotFound, "virtual service %q not found", name)
	}

	if err := cl3bobject.DeleteVirtualService(m.agent, name); err != nil {
		return fmt.Errorf("failed to delete virtual service %q: %w", name, err)
	}

	// The delete retired the generation holding the published object;
	// retry the deferred ones, then retire this one.
	m.reclaimDeferred()
	m.deferOrFree(existing.Retire())

	delete(m.services, name)
	return nil
}

func (m *backend) ListServices() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.services))
	for name := range m.services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *backend) UpdateModuleConfig(config *l3bpb.ModuleConfig) error {
	name := config.GetName()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Resolve the service each rule names before building anything; the
	// rules themselves carry the names into the module configuration.
	rules := make([]cl3b.DestinationFilterRule, 0, len(config.GetDestinationFilterRules()))
	for _, rule := range config.GetDestinationFilterRules() {
		serviceName := rule.GetService()
		if _, ok := m.services[serviceName]; !ok {
			return status.Errorf(codes.NotFound, "unknown virtual service %q", serviceName)
		}

		net6s, err := filterpbconv.ToNet6s(rule.GetNet6S())
		if err != nil {
			return fmt.Errorf("invalid net6s: %w", err)
		}
		net4s, err := filterpbconv.ToNet4s(rule.GetNet4S())
		if err != nil {
			return fmt.Errorf("invalid net4s: %w", err)
		}
		protoRanges, err := filterpbconv.ToProtoRanges(rule.GetProtoRanges())
		if err != nil {
			return fmt.Errorf("invalid proto ranges: %w", err)
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
	module, err := cl3b.NewModuleConfig(m.agent, name)
	if err != nil {
		return fmt.Errorf("failed to create module config %q: %w", name, err)
	}
	if err := module.Update(rules); err != nil {
		_ = module.Free()
		return fmt.Errorf("failed to update module config %q: %w", name, err)
	}

	managed := &managedConfig{module: module}
	previous, ok := m.configs[name]
	m.configs[name] = managed

	modules := make([]ffi.ModuleConfig, 0, len(m.configs))
	for _, cfg := range m.configs {
		modules = append(modules, cfg.FFIModule())
	}
	if err := m.agent.UpdateModules(modules); err != nil {
		// Roll back to the previous module so the map keeps the live one.
		if ok {
			m.configs[name] = previous
		} else {
			delete(m.configs, name)
		}
		_ = module.Free()
		return fmt.Errorf("failed to update module config %q: %w", name, err)
	}

	if ok {
		m.deferOrFree(previous.Retire())
	}
	return nil
}

func (m *backend) ListModuleConfigs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

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
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.services[service]
	if !ok {
		return status.Errorf(codes.NotFound, "virtual service %q not found", service)
	}

	m.reclaimDeferred()

	if err := existing.SetRealServerState(realServerIndex, enabled); err != nil {
		return fmt.Errorf("failed to set real server state: %w", err)
	}
	return nil
}

func (m *backend) UpdateRealServerWeight(
	service string,
	realServerIndex uint32,
	weight uint32,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.services[service]
	if !ok {
		return status.Errorf(codes.NotFound, "virtual service %q not found", service)
	}

	m.reclaimDeferred()

	if err := existing.SetRealServerWeight(realServerIndex, weight); err != nil {
		return fmt.Errorf("failed to rebuild real server ring: %w", err)
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
	defer m.mu.Unlock()

	existing, ok := m.services[service]
	if !ok {
		return nil, 0, 0, status.Errorf(codes.NotFound, "virtual service %q not found", service)
	}

	if limit == 0 {
		limit = defaultSessionPageLimit
	}
	return existing.PublishTarget().ReadSessions(m.agent, cursor, limit)
}

// defaultSessionPageLimit bounds a ListSessions page when the caller asks
// for no explicit limit.
const defaultSessionPageLimit = 1000

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

// sourceNetworkFromIPNet decodes a legacy filter IPNet message into a
// family-agnostic network; the mask may be non-contiguous.
func sourceNetworkFromIPNet(pb *filterpb.IPNet) (xnetip.Network, error) {
	addr, ok := netip.AddrFromSlice(pb.GetAddr())
	if !ok {
		return xnetip.Network{}, fmt.Errorf("invalid address")
	}
	mask, ok := netip.AddrFromSlice(pb.GetMask())
	if !ok {
		return xnetip.Network{}, fmt.Errorf("invalid mask")
	}
	if addr.Is4() != mask.Is4() {
		return xnetip.Network{}, fmt.Errorf("address and mask must be the same IP family")
	}
	return xnetip.NetworkFrom(addr, mask)
}

func buildVirtualServiceConfig(
	service *l3bpb.VirtualService,
) (cl3bobject.VirtualServiceConfig, error) {
	realServers := make([]cl3bobject.RealServer, 0, len(service.GetRealServers()))
	for _, server := range service.GetRealServers() {
		destinationAddress, ok := netip.AddrFromSlice(server.GetDestinationAddress())
		if !ok {
			return cl3bobject.VirtualServiceConfig{}, fmt.Errorf("invalid real server destination address")
		}

		sourceNet, err := sourceNetworkFromIPNet(server.GetSourceNetwork())
		if err != nil {
			return cl3bobject.VirtualServiceConfig{}, fmt.Errorf("invalid real server source network: %w", err)
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
			return cl3bobject.VirtualServiceConfig{}, fmt.Errorf(
				"real server %d pairs a %s destination with a %s source network",
				len(realServers), familyLabel(destinationAddress), familyLabel(sourceNetAddr),
			)
		}

		realServers = append(realServers, cl3bobject.RealServer{
			Type:               family,
			DestinationAddress: destinationAddress,
			SourceNet:          sourceNet,
		})
	}

	sourceFilterRules := make([]cl3bobject.SourceFilterRule, 0, len(service.GetSourceFilterRules()))
	for _, rule := range service.GetSourceFilterRules() {
		net6s, err := filterpbconv.ToNet6s(rule.GetNet6S())
		if err != nil {
			return cl3bobject.VirtualServiceConfig{}, fmt.Errorf("invalid source net6s: %w", err)
		}
		net4s, err := filterpbconv.ToNet4s(rule.GetNet4S())
		if err != nil {
			return cl3bobject.VirtualServiceConfig{}, fmt.Errorf("invalid source net4s: %w", err)
		}
		portRanges, err := filterpbconv.ToPortRanges(rule.GetPortRanges())
		if err != nil {
			return cl3bobject.VirtualServiceConfig{}, fmt.Errorf("invalid source port ranges: %w", err)
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
		SessionIndexSize:  service.GetSessionIndexSize(),
	}, nil
}
