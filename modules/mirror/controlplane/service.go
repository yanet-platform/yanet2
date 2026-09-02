package mirror

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	filterpbconv "github.com/yanet-platform/yanet2/bindings/go/filterpbconv/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
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

	mu sync.Mutex

	// deferred holds superseded module handles whose free was refused
	// because a live configuration generation still referenced them.
	// This service is their owner: it retries them on its next update,
	// through ReclaimDeferred, and nothing else remembers them.
	deferred []ModuleHandle
	backend  Backend
	configs  map[string]mirrorConfig
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

	reqRules := req.Rules

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

	m.mu.Lock()
	defer m.mu.Unlock()

	module, err := m.backend.UpdateModule(name, rules)
	if err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	m.reclaimDeferred()

	if oldModule, ok := m.configs[name]; ok {
		m.parkOrFree(oldModule.Module)
	}

	m.configs[name] = mirrorConfig{
		Rules:  reqRules,
		Module: module,
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
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	if err := m.backend.DeleteModule(name); err != nil {
		return nil, fmt.Errorf("failed to delete module config %q: %w", name, err)
	}

	m.reclaimDeferred()
	m.parkOrFree(config.Module)

	delete(m.configs, name)

	return &mirrorpb.DeleteConfigResponse{}, nil
}

// parkOrFree frees the handle when it is dangling and parks it for
// retry when a live generation still references it. The caller must
// hold m.mu.
func (m *MirrorService) parkOrFree(handle ModuleHandle) {
	if handle == nil {
		return
	}
	if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
		m.deferred = append(m.deferred, handle)
	}
}

// ReclaimDeferred retries every deferred handle, dropping the ones whose
// generations have drained and keeping the rest deferred. It is the
// reclamation handler for this module's superseded configs; the service
// itself runs it after each successful publish, and anything else may
// call it at any time.
func (m *MirrorService) ReclaimDeferred() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reclaimDeferred()
}

// reclaimDeferred is ReclaimDeferred without the lock. The caller must
// hold m.mu.
func (m *MirrorService) reclaimDeferred() {
	kept := m.deferred[:0]
	for _, handle := range m.deferred {
		if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
			kept = append(kept, handle)
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}
