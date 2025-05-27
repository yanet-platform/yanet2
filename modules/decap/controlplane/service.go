package decap

import (
	"cmp"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb"
)

type DecapService struct {
	decappb.UnimplementedDecapServiceServer

	mu      sync.Mutex
	agents  []*ffi.Agent
	configs map[instanceKey][]netip.Prefix
	log     *zap.SugaredLogger
}

func NewDecapService(agents []*ffi.Agent, log *zap.SugaredLogger) *DecapService {
	return &DecapService{
		agents:  agents,
		configs: map[instanceKey][]netip.Prefix{},
		log:     log,
	}
}

func (m *DecapService) ShowConfig(
	ctx context.Context,
	request *decappb.ShowConfigRequest,
) (*decappb.ShowConfigResponse, error) {
	name, numa, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := instanceKey{name: name, numaIdx: numa}
	prefixes, ok := m.configs[key]
	if !ok {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	instanceConfig := &decappb.InstanceConfig{
		Numa:     numa,
		Prefixes: make([]string, 0, len(prefixes)),
	}

	for _, p := range prefixes {
		instanceConfig.Prefixes = append(instanceConfig.Prefixes, p.String())
	}

	return &decappb.ShowConfigResponse{Config: instanceConfig}, nil
}

func (m *DecapService) AddPrefixes(
	ctx context.Context,
	request *decappb.AddPrefixesRequest,
) (*decappb.AddPrefixesResponse, error) {

	name, numa, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	toAdd := make([]netip.Prefix, 0, len(request.GetPrefixes()))
	for _, prefixStr := range request.GetPrefixes() {
		prefix, err := netip.ParsePrefix(prefixStr)
		if err != nil {
			return nil, err
		}
		toAdd = append(toAdd, prefix.Masked())
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := instanceKey{name: name, numaIdx: numa}
	prefixes, ok := m.configs[key]
	if !ok {
		m.configs[key] = toAdd
	} else {
		m.configs[key] = slices.Compact(
			slices.SortedFunc(
				slices.Values(slices.Concat(prefixes, toAdd)),
				func(a netip.Prefix, b netip.Prefix) int {
					return cmp.Compare(a.String(), b.String())
				}),
		)
	}

	return &decappb.AddPrefixesResponse{}, m.updateModuleConfig(name, numa)
}

func (m *DecapService) RemovePrefixes(
	ctx context.Context,
	request *decappb.RemovePrefixesRequest,
) (*decappb.RemovePrefixesResponse, error) {
	name, numa, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	toRemove := make([]netip.Prefix, 0, len(request.GetPrefixes()))
	for _, prefixStr := range request.GetPrefixes() {
		prefix, err := netip.ParsePrefix(prefixStr)
		if err != nil {
			return nil, err
		}
		toRemove = append(toRemove, prefix.Masked())
	}

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()

	key := instanceKey{name: name, numaIdx: numa}
	prefixes, ok := m.configs[key]
	if !ok {
		return &decappb.RemovePrefixesResponse{}, nil
	}

	m.configs[key] = slices.DeleteFunc(prefixes, func(prefix netip.Prefix) bool {
		return slices.Contains(toRemove, prefix)
	})

	return &decappb.RemovePrefixesResponse{}, m.updateModuleConfig(name, numa)
}

func (m *DecapService) updateModuleConfig(
	name string,
	numaIdx uint32,
) error {
	m.log.Debugw("update config", zap.String("module", name), zap.Uint32("numa", numaIdx))

	agent := m.agents[numaIdx]

	config, err := NewModuleConfig(agent, name)
	if err != nil {
		return fmt.Errorf("failed to create %q module config: %w", name, err)
	}
	for _, prefix := range m.configs[instanceKey{name: name, numaIdx: numaIdx}] {
		if err := config.PrefixAdd(prefix); err != nil {
			return fmt.Errorf("failed to add prefix for %s: %w", name, err)
		}
	}

	if err := agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}); err != nil {
		return fmt.Errorf("failed to update module: %w", err)
	}

	m.log.Infow("successfully updated module",
		zap.String("name", name),
		zap.Uint32("numa", numaIdx),
	)

	return nil
}
