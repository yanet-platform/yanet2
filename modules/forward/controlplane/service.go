package forward

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
)

type ffiConfigUpdater func(m *ForwardService, name string) error

type forwardConfig struct {
	rules  []*forwardpb.Rule
	module *ModuleConfig
}

type ForwardService struct {
	forwardpb.UnimplementedForwardServiceServer

	mu      sync.Mutex
	agent   *ffi.Agent
	configs map[string]forwardConfig
}

func NewForwardService(agent *ffi.Agent) *ForwardService {
	return &ForwardService{
		agent:   agent,
		configs: make(map[string]forwardConfig),
	}
}

func (m *ForwardService) ListConfigs(
	ctx context.Context, request *forwardpb.ListConfigsRequest,
) (*forwardpb.ListConfigsResponse, error) {

	response := &forwardpb.ListConfigsResponse{
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

func (m *ForwardService) ShowConfig(ctx context.Context, req *forwardpb.ShowConfigRequest) (*forwardpb.ShowConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	config, ok := m.configs[req.Name]

	if !ok {
		return nil, status.Error(codes.InvalidArgument, "not found")
	}

	response := &forwardpb.ShowConfigResponse{
		Name:  req.Name,
		Rules: config.rules,
	}

	return response, nil
}

func (m *ForwardService) UpdateConfig(ctx context.Context, req *forwardpb.UpdateConfigRequest) (*forwardpb.UpdateConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	reqRules := req.Rules

	rules := make([]forwardRule, 0, len(reqRules))
	for _, reqRule := range reqRules {
		devices, err := filter.MakeDevices(reqRule.Devices)
		if err != nil {
			return nil, err
		}
		vlanRanges, err := filter.MakeVlanRanges(reqRule.VlanRanges)
		if err != nil {
			return nil, err
		}
		src4s, err := filter.MakeIPNet4s(reqRule.Srcs)
		if err != nil {
			return nil, err
		}
		dst4s, err := filter.MakeIPNet4s(reqRule.Dsts)
		if err != nil {
			return nil, err
		}
		src6s, err := filter.MakeIPNet6s(reqRule.Srcs)
		if err != nil {
			return nil, err
		}
		dst6s, err := filter.MakeIPNet6s(reqRule.Dsts)
		if err != nil {
			return nil, err
		}

		rule := forwardRule{
			target:     reqRule.Action.Target,
			mode:       modeNone,
			counter:    reqRule.Action.Counter,
			devices:    devices,
			vlanRanges: vlanRanges,
			src4s:      src4s,
			dst4s:      dst4s,
			src6s:      src6s,
			dst6s:      dst6s,
		}

		if reqRule.Action.Mode == forwardpb.ForwardMode_IN {
			rule.mode = modeIn
		}
		if reqRule.Action.Mode == forwardpb.ForwardMode_OUT {
			rule.mode = modeOut
		}

		rules = append(rules, rule)
	}

	module, err := NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)

	}

	if err := module.Update(rules); err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		return nil, fmt.Errorf("failed to update module: %w", err)
	}

	if oldModule, ok := m.configs[name]; ok {
		FreeModuleConfig(oldModule.module)
	}

	m.configs[name] = forwardConfig{
		rules:  reqRules,
		module: module,
	}

	return &forwardpb.UpdateConfigResponse{}, nil
}

func (m *ForwardService) DeleteConfig(ctx context.Context, req *forwardpb.DeleteConfigRequest) (*forwardpb.DeleteConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}
	// Remove module configuration from the control plane.
	// TODO

	deleted := DeleteConfig(m, name)

	response := &forwardpb.DeleteConfigResponse{
		Deleted: deleted,
	}
	return response, nil
}
