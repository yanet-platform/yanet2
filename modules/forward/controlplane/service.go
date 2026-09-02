package forward

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	filterpbconv "github.com/yanet-platform/yanet2/bindings/go/filterpbconv/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forwardpb "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
)

// ModuleHandle is a handle to a module configuration.
type ModuleHandle interface {
	Free() error
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
func (m *forwardConfig) Free() error {
	if m.Module == nil {
		return nil
	}
	return m.Module.Free()
}

type ForwardService struct {
	forwardpb.UnimplementedForwardServiceServer

	mu sync.RWMutex

	// deferred holds superseded module handles whose free was refused
	// because a live configuration generation still referenced them.
	// This service is their owner: it retries them on its next update,
	// through ReclaimDeferred, and nothing else remembers them.
	deferred []ModuleHandle
	backend  Backend
	configs  map[string]forwardConfig
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
	m.mu.RLock()
	defer m.mu.RUnlock()

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

	m.mu.RLock()
	defer m.mu.RUnlock()

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

// materializeCounter builds the default counter name for a rule that left
// it empty: "to_" plus the target, bounded to the counter-name limit.
//
// The cut lands on a rune boundary because a name split mid-rune is not
// valid UTF-8, and a ShowConfig response carrying it would fail to marshal.
func materializeCounter(target string) string {
	name := "to_" + target
	if len(name) <= cforward.CounterNameMaxLen {
		return name
	}

	cut := cforward.CounterNameMaxLen
	for cut > 0 && !utf8.RuneStart(name[cut]) {
		cut--
	}
	return name[:cut]
}

// UpdateConfig replaces the rule set for a named forward module config and
// publishes it to the dataplane.
//
// A rule that leaves its counter empty is stored with "to_" plus its
// target, bounded to the counter-name limit, so ShowConfig never returns an
// empty name for a rule applied here. A non-empty counter is passed through
// verbatim.
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

		// Assign onto the request's own action message: the ForwardRule
		// built below and the reqRules stored into m.configs after the
		// backend call both read this same action, so one assignment here
		// keeps the shared-memory write and ShowConfig in agreement.
		if action.Counter == "" {
			action.Counter = materializeCounter(action.Target)
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

	m.reclaimDeferred()

	if oldModule, ok := m.configs[name]; ok {
		m.parkOrFree(oldModule.Module)
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

	m.reclaimDeferred()
	m.parkOrFree(config.Module)

	delete(m.configs, name)

	return &forwardpb.DeleteConfigResponse{}, nil
}

// parkOrFree frees the handle when it is dangling and parks it for
// retry when a live generation still references it. The caller must
// hold m.mu.
func (m *ForwardService) parkOrFree(handle ModuleHandle) {
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
func (m *ForwardService) ReclaimDeferred() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reclaimDeferred()
}

// reclaimDeferred is ReclaimDeferred without the lock. The caller must
// hold m.mu.
func (m *ForwardService) reclaimDeferred() {
	kept := m.deferred[:0]
	for _, handle := range m.deferred {
		if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
			kept = append(kept, handle)
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}
