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
	agent   *ffi.Agent
	configs map[string][]netip.Prefix
	log     *zap.SugaredLogger
}

func NewDecapService(agent *ffi.Agent, log *zap.SugaredLogger) *DecapService {
	return &DecapService{
		agent:   agent,
		configs: map[string][]netip.Prefix{},
		log:     log,
	}
}

func (m *DecapService) ListConfigs(
	ctx context.Context, request *decappb.ListConfigsRequest,
) (*decappb.ListConfigsResponse, error) {

	response := &decappb.ListConfigsResponse{
		Configs: make([]string, 0),
	}

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()

	for name := range m.configs {
		response.Configs = append(response.Configs, name)
	}

	return response, nil
}

func (m *DecapService) ShowConfig(
	ctx context.Context,
	request *decappb.ShowConfigRequest,
) (*decappb.ShowConfigResponse, error) {
	name, err := request.GetTarget().Validate()
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	prefixes, ok := m.configs[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	response := &decappb.ShowConfigResponse{
		Prefixes: make([]string, 0, len(prefixes)),
	}

	for _, p := range prefixes {
		response.Prefixes = append(response.Prefixes, p.String())
	}

	return response, nil
}

func (m *DecapService) AddPrefixes(
	ctx context.Context,
	request *decappb.AddPrefixesRequest,
) (*decappb.AddPrefixesResponse, error) {

	name, err := request.GetTarget().Validate()
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

	prefixes, ok := m.configs[name]
	if !ok {
		m.configs[name] = toAdd
	} else {
		m.configs[name] = slices.Compact(
			slices.SortedFunc(
				slices.Values(slices.Concat(prefixes, toAdd)),
				func(a netip.Prefix, b netip.Prefix) int {
					return cmp.Compare(a.String(), b.String())
				}),
		)
	}

	return &decappb.AddPrefixesResponse{}, m.updateModuleConfig(name)
}

func (m *DecapService) RemovePrefixes(
	ctx context.Context,
	request *decappb.RemovePrefixesRequest,
) (*decappb.RemovePrefixesResponse, error) {
	name, err := request.GetTarget().Validate()
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

	prefixes, ok := m.configs[name]
	if !ok {
		return &decappb.RemovePrefixesResponse{}, nil
	}

	m.configs[name] = slices.DeleteFunc(prefixes, func(prefix netip.Prefix) bool {
		return slices.Contains(toRemove, prefix)
	})

	return &decappb.RemovePrefixesResponse{}, m.updateModuleConfig(name)
}

func (m *DecapService) updateModuleConfig(name string) error {
	m.log.Debugw("update config", zap.String("module", name))

	config, err := NewModuleConfig(m.agent, name)
	if err != nil {
		return fmt.Errorf("failed to create %q module config: %w", name, err)
	}
	for _, prefix := range m.configs[name] {
		if err := config.PrefixAdd(prefix); err != nil {
			return fmt.Errorf("failed to add prefix for %s: %w", name, err)
		}
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}); err != nil {
		return fmt.Errorf("failed to update module: %w", err)
	}

	return nil
}
