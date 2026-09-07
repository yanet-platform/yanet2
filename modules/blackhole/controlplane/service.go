package blackhole

import (
	"errors"

	"github.com/yanet-platform/yanet2/controlplane/ffi"

	"context"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	blackholepb "github.com/yanet-platform/yanet2/modules/blackhole/controlplane/blackholepb/v1"
)

var errConfigNameRequired = status.Error(codes.InvalidArgument, "config name is required")

// ModuleHandle is a handle to a module configuration.
type ModuleHandle interface {
	Free() error
}

// Backend abstracts shared memory operations.
type Backend interface {
	// UpdateModule creates a module config and publishes it to the
	// dataplane.
	UpdateModule(name string) (ModuleHandle, error)
	// DeleteModule removes a module config.
	DeleteModule(name string) error
}

type config struct {
	Module ModuleHandle
}

// Free releases the module handle held by the config.
//
// It is safe to call even when no handle is held.
func (m *config) Free() error {
	if m.Module == nil {
		return nil
	}
	return m.Module.Free()
}

// configEntry is the per-name lock anchor of a blackhole config.
//
// Entries are append-only: deleting a config clears its published slot
// instead of removing the entry, keeping the entry as the lock anchor.
// Acquiring a name's lock is a two-step operation — fetch the entry, then
// lock it — and removing entries would let two goroutines serialize the
// same name on two different entry objects during exactly that window.
type configEntry struct {
	// updateMu serializes mutations of this name for the entry's whole
	// life, across the whole operation including the backend publish.
	updateMu sync.Mutex
	// published is the config currently active for this name, or nil
	// when the name is absent. It is written only while holding both the
	// entry's update lock and the service write lock; an update-lock
	// holder may read it without the service lock.
	published *config
}

func (m *configEntry) LockUpdate() {
	m.updateMu.Lock()
}

func (m *configEntry) UnlockUpdate() {
	m.updateMu.Unlock()
}

func (m *configEntry) Published() *config {
	return m.published
}

func (m *configEntry) Publish(config *config) {
	m.published = config
}

// BlackholeService implements the BlackholeService gRPC server.
type BlackholeService struct {
	blackholepb.UnimplementedBlackholeServiceServer

	// mu guards configs and deferred. Critical sections are short map
	// and slice work only; the backend publish runs under the target
	// entry's update lock, outside any service-lock section, so a
	// stalled publish never blocks a read.
	mu sync.RWMutex
	// reclaimMu serializes handle reclamation — draining the deferred
	// list and parking a superseded handle — across its free attempts,
	// so a handle refused by a draining generation is retried before the
	// drain that could miss it completes. It is never taken by a read
	// path, and a handle's destruction under it may block on the shared
	// C-side configuration lock, which only stalls other reclamations.
	reclaimMu sync.Mutex

	// deferred holds superseded module handles whose free was refused
	// because a live configuration generation still referenced them.
	// This service is their owner: it retries them on its next update,
	// through ReclaimDeferred, and nothing else remembers them.
	deferred []ModuleHandle
	backend  Backend
	// configs maps a name to its append-only entry. See configEntry.
	configs map[string]*configEntry
}

// NewBlackholeService constructs a BlackholeService backed by the given Backend.
func NewBlackholeService(backend Backend) *BlackholeService {
	return &BlackholeService{
		backend: backend,
		configs: map[string]*configEntry{},
	}
}

// ListConfigs returns all known config names across all dataplane instances.
func (m *BlackholeService) ListConfigs(
	ctx context.Context,
	req *blackholepb.ListConfigsRequest,
) (*blackholepb.ListConfigsResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.configs))
	for name, entry := range m.configs {
		if entry.Published() == nil {
			continue
		}
		names = append(names, name)
	}

	return &blackholepb.ListConfigsResponse{Configs: names}, nil
}

// ShowConfig returns the named config when it exists.
func (m *BlackholeService) ShowConfig(
	ctx context.Context,
	req *blackholepb.ShowConfigRequest,
) (*blackholepb.ShowConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.configs[name]
	if !ok || entry.Published() == nil {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	return &blackholepb.ShowConfigResponse{Name: name}, nil
}

// UpdateConfig creates or replaces the named config and publishes it to the
// dataplane.
func (m *BlackholeService) UpdateConfig(
	ctx context.Context,
	req *blackholepb.UpdateConfigRequest,
) (*blackholepb.UpdateConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	err := m.withEntry(name, func(entry *configEntry) error {
		mod, err := m.backend.UpdateModule(name)
		if err != nil {
			return status.Errorf(
				codes.Internal,
				"failed to update module config %q: %v", name, err,
			)
		}

		oldConfig := entry.Published()

		m.setPublished(entry, &config{Module: mod})

		// The publish retired the generation holding the published
		// module; retry the deferred ones, then retire the displaced one.
		m.ReclaimDeferred()
		if oldConfig != nil {
			m.parkOrFree(oldConfig.Module)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &blackholepb.UpdateConfigResponse{}, nil
}

// DeleteConfig removes the named config if it is not referenced by any
// pipeline.
func (m *BlackholeService) DeleteConfig(
	ctx context.Context,
	req *blackholepb.DeleteConfigRequest,
) (*blackholepb.DeleteConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, errConfigNameRequired
	}

	// Rejecting an unknown name here keeps the locked path from interning
	// an entry for a name that never existed; the locked re-check below
	// stays authoritative for the name that waited on an in-flight update.
	if !m.hasEntry(name) {
		return nil, status.Error(codes.NotFound, "no config found")
	}

	err := m.withEntry(name, func(entry *configEntry) error {
		oldConfig := entry.Published()
		if oldConfig == nil {
			return status.Error(codes.NotFound, "no config found")
		}

		if err := m.backend.DeleteModule(name); err != nil {
			return status.Errorf(
				codes.Internal,
				"failed to delete module config %q: %v", name, err,
			)
		}

		m.setPublished(entry, nil)

		// The delete retired the generation holding the published
		// module; retry the deferred ones, then retire this one.
		m.ReclaimDeferred()
		m.parkOrFree(oldConfig.Module)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &blackholepb.DeleteConfigResponse{}, nil
}

// entry fetches or creates the lock anchor of the named config. The caller
// must not hold the service lock.
func (m *BlackholeService) entry(name string) *configEntry {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, ok := m.configs[name]; ok {
		return entry
	}
	entry := &configEntry{}
	m.configs[name] = entry
	return entry
}

// hasEntry reports whether the named config already has an entry, live or
// tombstoned. It is the read-only pre-check that keeps the deleting path
// from interning an entry for a name that never existed.
func (m *BlackholeService) hasEntry(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.configs[name]
	return ok
}

// withEntry fetches or creates the entry for the name, holds its update
// lock for the duration of the call, then returns the call's error. The
// backend publish runs here, under the entry lock only.
func (m *BlackholeService) withEntry(name string, fn func(*configEntry) error) error {
	entry := m.entry(name)
	entry.LockUpdate()
	defer entry.UnlockUpdate()

	return fn(entry)
}

// setPublished swaps the entry's published config under the service lock.
func (m *BlackholeService) setPublished(entry *configEntry, config *config) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry.Publish(config)
}

// parkOrFree frees the handle when it is dangling and parks it for
// retry when a live generation still references it. The whole cycle runs
// under the reclamation lock so it cannot interleave with a concurrent
// drain that would miss the survivor.
func (m *BlackholeService) parkOrFree(handle ModuleHandle) {
	m.reclaimMu.Lock()
	defer m.reclaimMu.Unlock()

	if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
		m.park(handle)
	}
}

func (m *BlackholeService) park(handle ModuleHandle) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.deferred = append(m.deferred, handle)
}

// ReclaimDeferred retries every deferred handle, dropping the ones whose
// generations have drained and keeping the rest deferred. It is the
// reclamation handler for this module's superseded configs; the service
// itself runs it after each successful publish, and anything else may
// call it at any time.
//
// The frees run without the service lock: a handle's destruction takes
// the shared C-side configuration lock, which an in-flight publish of
// another name may hold, so freeing under the service lock would let
// that publish stall every read again. The reclamation lock still
// serializes the whole cycle against a concurrent park, so a survivor
// can never be missed by the drain that precedes its park.
func (m *BlackholeService) ReclaimDeferred() {
	m.reclaimMu.Lock()
	defer m.reclaimMu.Unlock()

	handles := m.drainDeferred()
	for _, handle := range handles {
		if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
			m.park(handle)
		}
	}
}

func (m *BlackholeService) drainDeferred() []ModuleHandle {
	m.mu.Lock()
	defer m.mu.Unlock()

	handles := m.deferred
	m.deferred = nil
	return handles
}
