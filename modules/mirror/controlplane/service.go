package mirror

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/modules/mirror/bindings/go/cmirror"
	mirrorpb "github.com/yanet-platform/yanet2/modules/mirror/controlplane/mirrorpb/v1"
)

// ModuleHandle is a handle to a module configuration.
type ModuleHandle interface {
	Free()
}

// Backend abstracts shared memory operations.
type Backend interface {
	// UpdateModule creates a module config, writes rules, and publishes
	// it to the dataplane.
	UpdateModule(name string, rules []cmirror.MirrorRule) (ModuleHandle, error)
	// DeleteModule removes a module config.
	DeleteModule(name string) error
}

type mirrorConfig struct {
	rules  []*mirrorpb.Rule
	module ModuleHandle
}

type MirrorService struct {
	mirrorpb.UnimplementedMirrorServiceServer

	mu      sync.Mutex
	backend Backend
	configs map[string]mirrorConfig
}

func NewMirrorService(backend Backend) *MirrorService {
	return &MirrorService{
		backend: backend,
		configs: map[string]mirrorConfig{},
	}
}

func (m *MirrorService) ListConfigs(
	ctx context.Context, request *mirrorpb.ListConfigsRequest,
) (*mirrorpb.ListConfigsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	configs := make([]string, 0, len(m.configs))
	for name := range m.configs {
		configs = append(configs, name)
	}

	response := &mirrorpb.ListConfigsResponse{
		Configs: configs,
	}

	return response, nil
}

func (m *MirrorService) ShowConfig(
	ctx context.Context,
	req *mirrorpb.ShowConfigRequest,
) (*mirrorpb.ShowConfigResponse, error) {
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

	response := &mirrorpb.ShowConfigResponse{
		Name:  req.Name,
		Rules: config.rules,
	}

	return response, nil
}

func (m *MirrorService) UpdateConfig(
	ctx context.Context,
	req *mirrorpb.UpdateConfigRequest,
) (*mirrorpb.UpdateConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	reqRules := req.Rules

	rules := make([]cmirror.MirrorRule, 0, len(reqRules))
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

		rule := cmirror.MirrorRule{
			Target:     action.Target,
			Mode:       cmirror.ModeNone,
			Counter:    action.Counter,
			Devices:    devices,
			VlanRanges: vlanRanges,
			Src4s:      src4s,
			Dst4s:      dst4s,
			Src6s:      src6s,
			Dst6s:      dst6s,
		}

		if action.Mode == mirrorpb.MirrorMode_IN {
			rule.Mode = cmirror.ModeIn
		}
		if action.Mode == mirrorpb.MirrorMode_OUT {
			rule.Mode = cmirror.ModeOut
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

	m.configs[name] = mirrorConfig{
		rules:  reqRules,
		module: module,
	}

	return &mirrorpb.UpdateConfigResponse{}, nil
}

func (m *MirrorService) DeleteConfig(
	ctx context.Context,
	req *mirrorpb.DeleteConfigRequest,
) (*mirrorpb.DeleteConfigResponse, error) {
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

	return &mirrorpb.DeleteConfigResponse{Deleted: true}, nil
}
