package dscp

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
	"github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb"
)

type DscpService struct {
	dscppb.UnimplementedDscpServiceServer

	mu      sync.Mutex
	agent   *ffi.Agent
	configs map[string]*instanceConfig
	log     *zap.SugaredLogger
}

type instanceConfig struct {
	prefixes []netip.Prefix
	dscpCfg  dscpConfig
}

type dscpConfig struct {
	flag uint8
	mark uint8
}

func NewDscpService(agent *ffi.Agent, log *zap.SugaredLogger) *DscpService {
	return &DscpService{
		agent:   agent,
		configs: map[string]*instanceConfig{},
		log:     log,
	}
}

func (m *DscpService) ListConfigs(
	ctx context.Context, request *dscppb.ListConfigsRequest,
) (*dscppb.ListConfigsResponse, error) {

	response := &dscppb.ListConfigsResponse{
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

func (m *DscpService) ShowConfig(
	ctx context.Context,
	request *dscppb.ShowConfigRequest,
) (*dscppb.ShowConfigResponse, error) {
	name, err := request.GetTarget().Validate()
	if err != nil {
		return nil, err
	}

	response := &dscppb.ShowConfigResponse{}

	m.mu.Lock()
	defer m.mu.Unlock()

	if config, ok := m.configs[name]; ok {
		instanceConfig := &dscppb.Config{
			Prefixes: make([]string, 0, len(config.prefixes)),
			DscpConfig: &dscppb.DscpConfig{
				Flag: uint32(config.dscpCfg.flag),
				Mark: uint32(config.dscpCfg.mark),
			},
		}

		for _, p := range config.prefixes {
			instanceConfig.Prefixes = append(instanceConfig.Prefixes, p.String())
		}
		response.Config = instanceConfig
	}

	return response, nil
}

func (m *DscpService) AddPrefixes(
	ctx context.Context,
	request *dscppb.AddPrefixesRequest,
) (*dscppb.AddPrefixesResponse, error) {

	name, err := request.GetTarget().Validate()
	if err != nil {
		return nil, err
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

	config, ok := m.configs[name]
	if !ok {
		m.configs[name] = &instanceConfig{
			prefixes: toAdd,
			dscpCfg:  dscpConfig{flag: 0, mark: 0},
		}
	} else {
		config.prefixes = slices.Compact(
			slices.SortedFunc(
				slices.Values(slices.Concat(config.prefixes, toAdd)),
				func(a netip.Prefix, b netip.Prefix) int {
					return cmp.Compare(a.String(), b.String())
				}),
		)
	}

	return &dscppb.AddPrefixesResponse{}, m.updateModuleConfig(name)
}

func (m *DscpService) RemovePrefixes(
	ctx context.Context,
	request *dscppb.RemovePrefixesRequest,
) (*dscppb.RemovePrefixesResponse, error) {
	name, err := request.GetTarget().Validate()
	if err != nil {
		return nil, err
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

	config, ok := m.configs[name]
	if ok {
		config.prefixes = slices.DeleteFunc(config.prefixes, func(prefix netip.Prefix) bool {
			return slices.Contains(toRemove, prefix)
		})
	}

	return &dscppb.RemovePrefixesResponse{}, m.updateModuleConfig(name)
}

func (m *DscpService) SetDscpMarking(
	ctx context.Context,
	request *dscppb.SetDscpMarkingRequest,
) (*dscppb.SetDscpMarkingResponse, error) {
	name, err := request.GetTarget().Validate()
	if err != nil {
		return nil, err
	}

	dscpCfg := request.GetDscpConfig()
	if dscpCfg == nil {
		return nil, status.Error(codes.InvalidArgument, "DSCP config is required")
	}

	// Validate flag value
	flag := uint8(dscpCfg.GetFlag())
	if flag > 2 {
		return nil, status.Error(codes.InvalidArgument, "invalid flag value (must be 0, 1, or 2)")
	}

	// Validate mark value (6-bit field)
	mark := uint8(dscpCfg.GetMark())
	if mark > 63 {
		return nil, status.Error(codes.InvalidArgument, "invalid mark value (must be 0-63)")
	}

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()

	config, ok := m.configs[name]
	if !ok {
		m.configs[name] = &instanceConfig{
			prefixes: []netip.Prefix{},
			dscpCfg: dscpConfig{
				flag: flag,
				mark: mark,
			},
		}
	} else {
		config.dscpCfg.flag = flag
		config.dscpCfg.mark = mark
	}

	return &dscppb.SetDscpMarkingResponse{}, m.updateModuleConfig(name)
}

func (m *DscpService) updateModuleConfig(
	name string,
) error {
	m.log.Debugw("update config", zap.String("module", name))

	config, err := NewModuleConfig(m.agent, name)
	if err != nil {
		return fmt.Errorf("failed to create %q module config: %w", name, err)
	}

	moduleConfig := m.configs[name]
	if moduleConfig != nil {
		// Add prefixes
		for _, prefix := range moduleConfig.prefixes {
			if err := config.PrefixAdd(prefix); err != nil {
				return fmt.Errorf("failed to add prefix for %s: %w", name, err)
			}
		}

		// Set DSCP marking
		if err := config.SetDscpMarking(moduleConfig.dscpCfg.flag, moduleConfig.dscpCfg.mark); err != nil {
			return fmt.Errorf("failed to set DSCP marking for %s: %w", name, err)
		}
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}); err != nil {
		return fmt.Errorf("failed to update module: %w", err)
	}

	return nil
}
