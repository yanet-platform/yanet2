package unrdup

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/unrdup/bindings/go/cunrdup"
	"github.com/yanet-platform/yanet2/modules/unrdup/controlplane/unrduppb/v1"
)

var errConfigNameRequired = status.Error(
	codes.InvalidArgument,
	"config name is required",
)

func validateConfigName(name string) error {
	if name == "" {
		return errConfigNameRequired
	}
	if strings.IndexByte(name, 0) >= 0 {
		return status.Error(
			codes.InvalidArgument, "config name must not contain a NUL byte",
		)
	}
	if len(name) > cunrdup.ModuleNameMaxLen {
		return status.Errorf(
			codes.InvalidArgument,
			"config name is %d bytes, the dataplane keeps at most %d",
			len(name),
			cunrdup.ModuleNameMaxLen,
		)
	}

	return nil
}

// ModuleHandle is a handle to a module configuration.
type ModuleHandle interface {
	Free() error
}

// Backend abstracts shared memory operations.
type Backend interface {
	// UpdateModule publishes a module config to the dataplane.
	UpdateModule(
		name string,
		sources []xnetip.Network,
		services []cunrdup.Service,
	) (ModuleHandle, error)
}

type UnrdupService struct {
	unrduppb.UnimplementedUnrdupServiceServer

	mu       sync.RWMutex
	backend  Backend
	configs  map[string]*config
	deferred []ModuleHandle
}

// parkOrFree frees a superseded handle, keeping it for a later retry while a
// live generation still references it. The caller must hold m.mu.
func (m *UnrdupService) parkOrFree(handle ModuleHandle) {
	if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
		m.deferred = append(m.deferred, handle)
	}
}

// ReclaimDeferred retries every parked handle, dropping the ones whose
// generations have drained and keeping the rest parked.
func (m *UnrdupService) ReclaimDeferred() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reclaimDeferred()
}

// reclaimDeferred is ReclaimDeferred without the lock. The caller must hold
// m.mu.
func (m *UnrdupService) reclaimDeferred() {
	kept := m.deferred[:0]
	for _, handle := range m.deferred {
		if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
			kept = append(kept, handle)
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}

type config struct {
	SourceV4 xnetip.Network
	SourceV6 xnetip.Network
	Services []cunrdup.Service
	Module   ModuleHandle
}

func (m *config) Sources() []xnetip.Network {
	sources := make([]xnetip.Network, 0, 2)
	if sourceIsSet(m.SourceV4) {
		sources = append(sources, m.SourceV4)
	}
	if sourceIsSet(m.SourceV6) {
		sources = append(sources, m.SourceV6)
	}

	return sources
}

func NewUnrdupService(backend Backend) *UnrdupService {
	return &UnrdupService{
		backend: backend,
		configs: map[string]*config{},
	}
}

func (m *UnrdupService) ListConfigs(
	ctx context.Context,
	request *unrduppb.ListConfigsRequest,
) (*unrduppb.ListConfigsResponse, error) {
	response := &unrduppb.ListConfigsResponse{
		Configs: make([]string, 0),
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for name := range m.configs {
		response.Configs = append(response.Configs, name)
	}
	slices.Sort(response.Configs)

	return response, nil
}

func (m *UnrdupService) ShowConfig(
	ctx context.Context,
	request *unrduppb.ShowConfigRequest,
) (*unrduppb.ShowConfigResponse, error) {
	name := request.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	current, ok := m.configs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q is not found", name)
	}

	return &unrduppb.ShowConfigResponse{
		Name:   name,
		Config: current.ToProto(),
	}, nil
}

func (m *UnrdupService) UpdateConfig(
	ctx context.Context,
	request *unrduppb.UpdateConfigRequest,
) (*unrduppb.UpdateConfigResponse, error) {
	name := request.GetName()
	if err := validateConfigName(name); err != nil {
		return nil, err
	}

	updated, err := configFromProto(request.GetConfig())
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	module, err := m.backend.UpdateModule(name, updated.Sources(), updated.Services)
	if err != nil {
		return nil, status.Errorf(
			codes.Internal, "failed to update config %q: %s", name, err,
		)
	}

	m.reclaimDeferred()

	if previous, ok := m.configs[name]; ok && previous.Module != nil {
		m.parkOrFree(previous.Module)
	}

	updated.Module = module
	m.configs[name] = updated

	return &unrduppb.UpdateConfigResponse{}, nil
}
