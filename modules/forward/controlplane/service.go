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
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
)

// ModuleHandle is a handle to a module configuration.
type ModuleHandle interface {
	Free()
}

// Backend abstracts shared memory operations.
type Backend interface {
	// UpdateModule creates a module config, writes rules, and publishes
	// it to the dataplane.
	UpdateModule(name string, rules []cforward.ForwardRule) (ModuleHandle, error)
	// DeleteModule removes a module config.
	DeleteModule(name string) error
}

type forwardConfig struct {
	rules  []*forwardpb.Rule
	module ModuleHandle
}

type ForwardService struct {
	forwardpb.UnimplementedForwardServiceServer

	mu      sync.Mutex
	backend Backend
	configs map[string]forwardConfig
}

func NewForwardService(backend Backend) *ForwardService {
	return &ForwardService{
		backend: backend,
		configs: map[string]forwardConfig{},
	}
}

func (m *ForwardService) ListConfigs(
	ctx context.Context, request *forwardpb.ListConfigsRequest,
) (*forwardpb.ListConfigsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	configs := make([]string, 0, len(m.configs))
	for name := range m.configs {
		configs = append(configs, name)
	}

	response := &forwardpb.ListConfigsResponse{
		Configs: configs,
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

	rules := make([]cforward.ForwardRule, 0, len(reqRules))
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

		rule := cforward.ForwardRule{
			Target:     reqRule.Action.Target,
			Mode:       cforward.ModeNone,
			Counter:    reqRule.Action.Counter,
			Devices:    devices,
			VlanRanges: vlanRanges,
			Src4s:      src4s,
			Dst4s:      dst4s,
			Src6s:      src6s,
			Dst6s:      dst6s,
		}

		if reqRule.Action.Mode == forwardpb.ForwardMode_IN {
			rule.Mode = cforward.ModeIn
		}
		if reqRule.Action.Mode == forwardpb.ForwardMode_OUT {
			rule.Mode = cforward.ModeOut
		}

		rules = append(rules, rule)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	module, err := m.backend.UpdateModule(name, rules)
	if err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
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

	m.mu.Lock()
	defer m.mu.Unlock()

	config, ok := m.configs[name]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "not found")
	}

	if err := m.backend.DeleteModule(name); err != nil {
		return nil, fmt.Errorf("failed to delete module config %q: %w", name, err)
	}

	if config.module != nil {
		config.module.Free()
	}

	delete(m.configs, name)

	return &forwardpb.DeleteConfigResponse{Deleted: true}, nil
}
