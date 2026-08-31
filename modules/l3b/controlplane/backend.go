package l3b

import (
	"fmt"
	"net/netip"
	"sync"

	"github.com/yanet-platform/xnetip"

	"github.com/yanet-platform/yanet2/bindings/go/filterpbconv/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/l3b/bindings/go/cl3b"
	l3bpb "github.com/yanet-platform/yanet2/modules/l3b/controlplane/l3bpb/v1"
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
}

type managedService struct {
	virtualService *cl3b.VirtualService
	handle         *cl3b.VirtualServiceHandle
	// weights[i] is the current weight of real server i; the scheduler ring
	// is rebuilt whenever a weight changes.
	weights []uint32
}

type managedConfig struct {
	module *cl3b.ModuleConfig
}

// backend is the real Backend backed by shared memory.
type backend struct {
	agent *ffi.Agent

	mu       sync.Mutex
	services map[string]*managedService
	configs  map[string]*managedConfig
}

// NewBackend creates a Backend that operates on real shared memory.
func NewBackend(agent *ffi.Agent) Backend {
	return &backend{
		agent:    agent,
		services: map[string]*managedService{},
		configs:  map[string]*managedConfig{},
	}
}

func (m *backend) CreateService(service *l3bpb.VirtualService) error {
	name := service.GetName()
	config, err := buildVirtualServiceConfig(service)
	if err != nil {
		return err
	}

	virtualService, err := cl3b.CreateVirtualService(m.agent, config)
	if err != nil {
		return fmt.Errorf("failed to create virtual service %q: %w", name, err)
	}

	weights := defaultWeights(len(config.RealServers))
	if err := virtualService.UpdateRing(ringFromWeights(weights)); err != nil {
		return fmt.Errorf("failed to populate real server ring: %w", err)
	}

	handle, err := cl3b.CreateVirtualServiceHandle(m.agent, virtualService)
	if err != nil {
		return fmt.Errorf("failed to create virtual service handle %q: %w", name, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.services[name]; ok {
		return fmt.Errorf("virtual service %q already exists", name)
	}

	m.services[name] = &managedService{
		virtualService: virtualService,
		handle:         handle,
		weights:        weights,
	}
	return nil
}

func (m *backend) UpdateService(service *l3bpb.VirtualService) error {
	name := service.GetName()
	config, err := buildVirtualServiceConfig(service)
	if err != nil {
		return err
	}

	virtualService, err := cl3b.CreateVirtualService(m.agent, config)
	if err != nil {
		return fmt.Errorf("failed to create virtual service %q: %w", name, err)
	}

	weights := defaultWeights(len(config.RealServers))
	if err := virtualService.UpdateRing(ringFromWeights(weights)); err != nil {
		return fmt.Errorf("failed to populate real server ring: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.services[name]
	if !ok {
		return fmt.Errorf("virtual service %q not found", name)
	}

	// Swap the handle to the new service so the dataplane picks it up without
	// rebuilding the module configuration.
	existing.handle.Update(virtualService)
	existing.virtualService = virtualService
	existing.weights = weights
	return nil
}

func (m *backend) DeleteService(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.services[name]; !ok {
		return fmt.Errorf("virtual service %q not found", name)
	}

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
	return names
}

func (m *backend) UpdateModuleConfig(config *l3bpb.ModuleConfig) error {
	name := config.GetName()

	m.mu.Lock()
	defer m.mu.Unlock()

	managed, ok := m.configs[name]
	if !ok {
		module, err := cl3b.NewModuleConfig(m.agent, name)
		if err != nil {
			return fmt.Errorf("failed to create module config %q: %w", name, err)
		}
		managed = &managedConfig{module: module}
		m.configs[name] = managed
	}

	// Resolve service names into handles in index order.
	handles := make([]*cl3b.VirtualServiceHandle, 0, len(config.GetServices()))
	serviceIndex := make(map[string]uint32, len(config.GetServices()))
	for idx, serviceName := range config.GetServices() {
		service, ok := m.services[serviceName]
		if !ok {
			return fmt.Errorf("unknown virtual service %q", serviceName)
		}
		handles = append(handles, service.handle)
		serviceIndex[serviceName] = uint32(idx)
	}

	rules := make([]cl3b.DestinationFilterRule, 0, len(config.GetDestinationFilterRules()))
	for _, rule := range config.GetDestinationFilterRules() {
		serviceName := rule.GetService()
		index, ok := serviceIndex[serviceName]
		if !ok {
			return fmt.Errorf("destination rule references unknown service %q", serviceName)
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
			Net6s:               net6s,
			Net4s:               net4s,
			ProtoRanges:         protoRanges,
			VirtualServiceIndex: index,
		})
	}

	if err := managed.module.Update(rules, handles); err != nil {
		return fmt.Errorf("failed to update module config %q: %w", name, err)
	}

	modules := make([]ffi.ModuleConfig, 0, len(m.configs))
	for _, cfg := range m.configs {
		modules = append(modules, cfg.module.AsFFIModule())
	}
	return m.agent.UpdateModules(modules)
}

func (m *backend) ListModuleConfigs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.configs))
	for name := range m.configs {
		names = append(names, name)
	}
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
		return fmt.Errorf("virtual service %q not found", service)
	}

	if err := existing.virtualService.SetRealServerState(realServerIndex, enabled); err != nil {
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
		return fmt.Errorf("virtual service %q not found", service)
	}

	if int(realServerIndex) >= len(existing.weights) {
		return fmt.Errorf("real server index %d out of range", realServerIndex)
	}

	if weight > maxRealServerWeight {
		weight = maxRealServerWeight
	}

	existing.weights[realServerIndex] = weight
	if err := existing.virtualService.UpdateRing(ringFromWeights(existing.weights)); err != nil {
		return fmt.Errorf("failed to rebuild real server ring: %w", err)
	}
	return nil
}

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

// ringFromWeights expands per-server weights into a weighted-round-robin
// scheduler ring: each server index appears as many times as its weight, but
// the occurrences are interleaved so a heavy server is spread evenly across
// the ring rather than clustered.
func ringFromWeights(weights []uint32) []uint32 {
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
) (cl3b.VirtualServiceConfig, error) {
	realServers := make([]cl3b.RealServer, 0, len(service.GetRealServers()))
	for _, server := range service.GetRealServers() {
		destinationAddress, ok := netip.AddrFromSlice(server.GetDestinationAddress())
		if !ok {
			return cl3b.VirtualServiceConfig{}, fmt.Errorf("invalid real server destination address")
		}

		sourceNet, err := sourceNetworkFromIPNet(server.GetSourceNetwork())
		if err != nil {
			return cl3b.VirtualServiceConfig{}, fmt.Errorf("invalid real server source network: %w", err)
		}

		family := cl3b.IPv4
		if destinationAddress.Is6() {
			family = cl3b.IPv6
		}

		realServers = append(realServers, cl3b.RealServer{
			Type:               family,
			DestinationAddress: destinationAddress,
			SourceNet:          sourceNet,
		})
	}

	sourceFilterRules := make([]cl3b.SourceFilterRule, 0, len(service.GetSourceFilterRules()))
	for _, rule := range service.GetSourceFilterRules() {
		net6s, err := filterpbconv.ToNet6s(rule.GetNet6S())
		if err != nil {
			return cl3b.VirtualServiceConfig{}, fmt.Errorf("invalid source net6s: %w", err)
		}
		net4s, err := filterpbconv.ToNet4s(rule.GetNet4S())
		if err != nil {
			return cl3b.VirtualServiceConfig{}, fmt.Errorf("invalid source net4s: %w", err)
		}
		portRanges, err := filterpbconv.ToPortRanges(rule.GetPortRanges())
		if err != nil {
			return cl3b.VirtualServiceConfig{}, fmt.Errorf("invalid source port ranges: %w", err)
		}

		sourceFilterRules = append(sourceFilterRules, cl3b.SourceFilterRule{
			Net6s:      net6s,
			Net4s:      net4s,
			PortRanges: portRanges,
		})
	}

	return cl3b.VirtualServiceConfig{
		SourceFilterRules: sourceFilterRules,
		RealServers:       realServers,
		HashMask:          service.GetHashMask(),
		IndexMask:         service.GetIndexMask(),
		RingCapacity:      uint32(len(realServers)) * maxRealServerWeight,
	}, nil
}
