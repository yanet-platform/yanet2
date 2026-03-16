package forward

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/filter/device"
	"github.com/yanet-platform/yanet2/common/go/filter/ipnet4"
	"github.com/yanet-platform/yanet2/common/go/filter/ipnet6"
	"github.com/yanet-platform/yanet2/common/go/filter/vlanrange"
	cpffi "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
	"github.com/yanet-platform/yanet2/modules/forward/internal/ffi"
)

type forwardConfig struct {
	rules  []*forwardpb.Rule
	module *ffi.ModuleConfig
}

type ForwardService struct {
	forwardpb.UnimplementedForwardServiceServer

	mu      sync.Mutex
	agent   *cpffi.Agent
	configs map[string]forwardConfig
}

func NewForwardService(agent *cpffi.Agent) *ForwardService {
	return &ForwardService{
		agent:   agent,
		configs: map[string]forwardConfig{},
	}
}

func (m *ForwardService) ListConfigs(
	ctx context.Context, request *forwardpb.ListConfigsRequest,
) (*forwardpb.ListConfigsResponse, error) {

	response := &forwardpb.ListConfigsResponse{
		Configs: make([]string, 0),
	}

	// Lock instances store and module updates.
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

	rules := make([]ffi.ForwardRule, 0, len(reqRules))
	for _, reqRule := range reqRules {
		devices, err := device.FromDevices(reqRule.Devices)
		if err != nil {
			return nil, err
		}
		vlanRanges, err := vlanrange.FromVlanRanges(reqRule.VlanRanges)
		if err != nil {
			return nil, err
		}
		src4s, err := ipnet4.FromIPNets(reqRule.Srcs)
		if err != nil {
			return nil, err
		}
		dst4s, err := ipnet4.FromIPNets(reqRule.Dsts)
		if err != nil {
			return nil, err
		}
		src6s, err := ipnet6.FromIPNets(reqRule.Srcs)
		if err != nil {
			return nil, err
		}
		dst6s, err := ipnet6.FromIPNets(reqRule.Dsts)
		if err != nil {
			return nil, err
		}

		rule := ffi.ForwardRule{
			Target:     reqRule.Action.Target,
			Mode:       ffi.ModeNone,
			Counter:    reqRule.Action.Counter,
			Devices:    devices,
			VlanRanges: vlanRanges,
			Src4s:      src4s,
			Dst4s:      dst4s,
			Src6s:      src6s,
			Dst6s:      dst6s,
		}

		if reqRule.Action.Mode == forwardpb.ForwardMode_IN {
			rule.Mode = ffi.ModeIn
		}
		if reqRule.Action.Mode == forwardpb.ForwardMode_OUT {
			rule.Mode = ffi.ModeOut
		}

		rules = append(rules, rule)
	}

	module, err := ffi.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	if err := module.Update(rules); err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	if err := m.agent.UpdateModules([]cpffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		return nil, fmt.Errorf("failed to update module: %w", err)
	}

	if oldModule, ok := m.configs[name]; ok {
		oldModule.module.Free()
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

	deleted := m.agent.DeleteModuleConfig(name) == nil

	response := &forwardpb.DeleteConfigResponse{
		Deleted: deleted,
	}
	return response, nil
}
