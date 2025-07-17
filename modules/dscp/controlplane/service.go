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
	agents  []*ffi.Agent
	configs map[instanceKey]*instanceConfig
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

func NewDscpService(agents []*ffi.Agent, log *zap.SugaredLogger) *DscpService {
	return &DscpService{
		agents:  agents,
		configs: map[instanceKey]*instanceConfig{},
		log:     log,
	}
}

func (m *DscpService) ListConfigs(
	ctx context.Context, request *dscppb.ListConfigsRequest,
) (*dscppb.ListConfigsResponse, error) {

	response := &dscppb.ListConfigsResponse{
		InstanceConfigs: make([]*dscppb.InstanceConfigs, len(m.agents)),
	}
	for inst := range m.agents {
		response.InstanceConfigs[inst] = &dscppb.InstanceConfigs{
			Instance: uint32(inst),
		}
	}

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()

	for key := range m.configs {
		instConfig := response.InstanceConfigs[key.dataplaneInstance]
		instConfig.Configs = append(instConfig.Configs, key.name)
	}

	return response, nil
}

func (m *DscpService) ShowConfig(
	ctx context.Context,
	request *dscppb.ShowConfigRequest,
) (*dscppb.ShowConfigResponse, error) {
	name, inst, err := request.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}

	key := instanceKey{name: name, dataplaneInstance: inst}
	response := &dscppb.ShowConfigResponse{Instance: inst}

	m.mu.Lock()
	defer m.mu.Unlock()

	if config, ok := m.configs[key]; ok {
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

	name, inst, err := request.GetTarget().Validate(uint32(len(m.agents)))
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

	key := instanceKey{name: name, dataplaneInstance: inst}
	config, ok := m.configs[key]
	if !ok {
		m.configs[key] = &instanceConfig{
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

	return &dscppb.AddPrefixesResponse{}, m.updateModuleConfig(name, inst)
}

func (m *DscpService) RemovePrefixes(
	ctx context.Context,
	request *dscppb.RemovePrefixesRequest,
) (*dscppb.RemovePrefixesResponse, error) {
	name, inst, err := request.GetTarget().Validate(uint32(len(m.agents)))
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

	key := instanceKey{name: name, dataplaneInstance: inst}
	config, ok := m.configs[key]
	if ok {
		config.prefixes = slices.DeleteFunc(config.prefixes, func(prefix netip.Prefix) bool {
			return slices.Contains(toRemove, prefix)
		})
	}

	return &dscppb.RemovePrefixesResponse{}, m.updateModuleConfig(name, inst)
}

func (m *DscpService) SetDscpMarking(
	ctx context.Context,
	request *dscppb.SetDscpMarkingRequest,
) (*dscppb.SetDscpMarkingResponse, error) {
	name, inst, err := request.GetTarget().Validate(uint32(len(m.agents)))
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

	key := instanceKey{name: name, dataplaneInstance: inst}
	config, ok := m.configs[key]
	if !ok {
		m.configs[key] = &instanceConfig{
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

	return &dscppb.SetDscpMarkingResponse{}, m.updateModuleConfig(name, inst)
}

func (m *DscpService) updateModuleConfig(
	name string,
	instance uint32,
) error {
	m.log.Debugw("update config", zap.String("module", name), zap.Uint32("instance", instance))

	agent := m.agents[instance]

	config, err := NewModuleConfig(agent, name)
	if err != nil {
		return fmt.Errorf("failed to create %q module config: %w", name, err)
	}

	moduleConfig := m.configs[instanceKey{name: name, dataplaneInstance: instance}]
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

	if err := agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}); err != nil {
		return fmt.Errorf("failed to update module: %w", err)
	}

	m.log.Infow("successfully updated module",
		zap.String("name", name),
		zap.Uint32("instance", instance),
	)
	return nil
}
