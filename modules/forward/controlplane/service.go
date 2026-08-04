package forward

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forwardpb "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
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
	// ModuleCounters returns selected dataplane counters collected for a module config.
	// If counterNames is nil or empty, it returns all counters for the module config.
	ModuleCounters(name string, counterNames []string) []CounterView
}

type forwardConfig struct {
	Rules  []*forwardpb.Rule
	Module ModuleHandle
}

// Free releases the module handle held by the config.
//
// It is safe to call even when no handle is held.
func (m *forwardConfig) Free() {
	if m.Module != nil {
		m.Module.Free()
	}
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
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	response := &forwardpb.ShowConfigResponse{
		Name:  req.Name,
		Rules: config.Rules,
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
		action := reqRule.GetAction()
		if action == nil {
			return nil, status.Error(codes.InvalidArgument, "rule action is required")
		}

		devices, err := filterpb.ToDevices(reqRule.Devices)
		if err != nil {
			return nil, err
		}
		vlanRanges, err := filterpb.ToVlanRanges(reqRule.VlanRanges)
		if err != nil {
			return nil, err
		}
		src4s, err := filterpb.ToNet4s(reqRule.Srcs)
		if err != nil {
			return nil, err
		}
		dst4s, err := filterpb.ToNet4s(reqRule.Dsts)
		if err != nil {
			return nil, err
		}
		src6s, err := filterpb.ToNet6s(reqRule.Srcs)
		if err != nil {
			return nil, err
		}
		dst6s, err := filterpb.ToNet6s(reqRule.Dsts)
		if err != nil {
			return nil, err
		}

		rule := cforward.ForwardRule{
			Target:     action.Target,
			Mode:       cforward.ModeNone,
			Counter:    action.Counter,
			Devices:    devices,
			VlanRanges: vlanRanges,
			Src4s:      src4s,
			Dst4s:      dst4s,
			Src6s:      src6s,
			Dst6s:      dst6s,
		}

		if action.Mode == forwardpb.ForwardMode_IN {
			rule.Mode = cforward.ModeIn
		}
		if action.Mode == forwardpb.ForwardMode_OUT {
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
		oldModule.Free()
	}

	m.configs[name] = forwardConfig{
		Rules:  reqRules,
		Module: module,
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
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	if err := m.backend.DeleteModule(name); err != nil {
		return nil, fmt.Errorf("failed to delete module config %q: %w", name, err)
	}

	config.Free()

	delete(m.configs, name)

	return &forwardpb.DeleteConfigResponse{Deleted: true}, nil
}
