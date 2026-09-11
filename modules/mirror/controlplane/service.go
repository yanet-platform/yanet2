package mirror

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	filterpbconv "github.com/yanet-platform/yanet2/bindings/go/filterpbconv/v1"
	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/modules/mirror/bindings/go/cmirror"
	mirrorpb "github.com/yanet-platform/yanet2/modules/mirror/controlplane/mirrorpb/v1"
)

// ModuleHandle is a handle to a module configuration.
type ModuleHandle interface {
	Free() error
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
	Rules  []*mirrorpb.Rule
	Module ModuleHandle
}

// Free releases the module handle held by the config.
//
// It is safe to call even when no handle is held.
func (m *mirrorConfig) Free() error {
	if m.Module == nil {
		return nil
	}
	return m.Module.Free()
}

type MirrorService struct {
	mirrorpb.UnimplementedMirrorServiceServer

	backend Backend
	configs *configstore.Store[*mirrorConfig]
}

func NewMirrorService(backend Backend) *MirrorService {
	return &MirrorService{
		backend: backend,
		configs: configstore.NewStore[*mirrorConfig](),
	}
}

func (m *MirrorService) ListConfigs(
	ctx context.Context, request *mirrorpb.ListConfigsRequest,
) (*mirrorpb.ListConfigsResponse, error) {
	response := &mirrorpb.ListConfigsResponse{
		Configs: m.configs.Names(),
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

	config, ok := m.configs.Get(name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	response := &mirrorpb.ShowConfigResponse{
		Name:  req.Name,
		Rules: config.Rules,
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

	reqRules := req.GetRules()
	if len(reqRules) == 0 {
		return nil, status.Error(
			codes.InvalidArgument,
			"mirror config must contain at least one rule",
		)
	}

	rules := make([]cmirror.MirrorRule, 0, len(reqRules))
	for _, reqRule := range reqRules {
		action := reqRule.GetAction()
		if action == nil {
			return nil, status.Error(codes.InvalidArgument, "rule action is required")
		}

		devices, err := filterpbconv.ToDevices(reqRule.Devices)
		if err != nil {
			return nil, err
		}
		vlanRanges, err := filterpbconv.ToVlanRanges(reqRule.VlanRanges)
		if err != nil {
			return nil, err
		}
		src4s, err := filterpbconv.ToNet4sFromNetworks(reqRule.Sources4)
		if err != nil {
			return nil, err
		}
		dst4s, err := filterpbconv.ToNet4sFromNetworks(reqRule.Destinations4)
		if err != nil {
			return nil, err
		}
		src6s, err := filterpbconv.ToNet6sFromNetworks(reqRule.Sources6)
		if err != nil {
			return nil, err
		}
		dst6s, err := filterpbconv.ToNet6sFromNetworks(reqRule.Destinations6)
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

	err := m.configs.Update(name, func(*mirrorConfig, bool) (*mirrorConfig, error) {
		module, err := m.backend.UpdateModule(name, rules)
		if err != nil {
			return nil, err
		}
		return &mirrorConfig{Rules: reqRules, Module: module}, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
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

	err := m.configs.Delete(name, func(*mirrorConfig) error {
		return m.backend.DeleteModule(name)
	})
	if errors.Is(err, configstore.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to delete module config %q: %w", name, err)
	}

	return &mirrorpb.DeleteConfigResponse{}, nil
}

// ReclaimDeferred retries every superseded config whose free was refused,
// releasing the ones whose generations have drained.
//
// The service runs it after each successful publish, and anything else
// may call it at any time.
func (m *MirrorService) ReclaimDeferred() {
	m.configs.ReclaimDeferred()
}
