package nat64s

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/nat64s/bindings/go/cnat64s"
)

// DefaultStaleLayerSweepInterval is the default period between stale-layer
// sweeps of the session tables.
const DefaultStaleLayerSweepInterval = 30 * time.Second

// ErrMapReserved reports a session map another config already links. It is
// a failed precondition: the request is valid but conflicts with the
// published configuration.
var ErrMapReserved = fmt.Errorf(
	"%w: session map is linked by another config",
	ffi.ErrFailedPrecondition,
)

// ErrMapLinkedByUntrackedConfig reports a session map that a config
// published by an earlier control plane still links.
//
// It is a failed precondition: this control plane cannot see that config,
// so the name is refused until the config is deleted by name.
var ErrMapLinkedByUntrackedConfig = fmt.Errorf(
	"%w: session map is linked by a config this control plane does not track",
	ffi.ErrFailedPrecondition,
)

// MapRegistry owns the nat64state map objects this module created: it
// creates them on first reference, and its sweeper reclaims the layers
// their tables park.
//
// Every operation that changes a map in the dataplane — its creation,
// its deletion and the sweeper's rounds — publishes a new object
// generation under the registry's lock, and that generation waits for
// every dataplane worker to advance, which is the grace period layer
// reclamation requires: no map is released underneath a hand that still
// holds it.
//
// The lock guards the reference records as well, which publish nothing
// on their own: dropping a config's reference only changes who references
// what, and retrying the free of a map the dataplane no longer lists only
// returns its memory.
//
// A session dump reads each of its pages under the same lock, and every
// release of table memory happens under it, so no page walks a layer
// that is being freed. The generation barriers a sweep publishes prove
// only that the dataplane moved on, not that this process did.
type MapRegistry struct {
	agent *ffi.Agent
	mu    sync.Mutex
	maps  map[string]*cnat64s.MapObjectConfig
	// Maps the dataplane no longer lists but whose memory it still
	// holds, a retired generation listing them.
	//
	// Their release is retried every round, until the generations
	// holding them drain.
	deferred []*cnat64s.MapObjectConfig
	// The sizing of the maps the registry creates, its zero fields
	// selecting the defaults of the shared memory layout.
	//
	// A map is created with its first layer only. That layer admits a
	// fixed number of sessions in total, expired ones keeping their key
	// slots, and the registry inserts no further one: once it is used up,
	// the map refuses every new session.
	sizing cnat64s.MapConfig
	// The config each map is reserved for, at most one, so the object is
	// reclaimed when its config lets go: each map owns two large tables,
	// and config churn with fresh names would otherwise exhaust the agent
	// arena. A config moving to another map holds both until the config
	// linking the new one is published.
	users map[string]map[string]struct{}

	log *zap.Logger
}

// RegistryOption configures the MapRegistry constructor.
type RegistryOption func(*MapRegistry)

// WithRegistryLog sets the registry's logger.
func WithRegistryLog(log *zap.Logger) RegistryOption {
	return func(m *MapRegistry) {
		if log != nil {
			m.log = log
		}
	}
}

// WithRegistryMapSizing sets the sizing of the maps the registry creates.
//
// The sizing names the index and the overflow buckets of their first
// layer and the size of each worker's stash; zero fields leave those to
// the defaults of the shared memory layout. Every map is sized for the
// dataplane's workers, whatever worker count the sizing carries.
func WithRegistryMapSizing(sizing cnat64s.MapConfig) RegistryOption {
	return func(m *MapRegistry) {
		m.sizing = sizing
	}
}

// NewMapRegistry creates an empty registry over the agent's shared memory.
func NewMapRegistry(agent *ffi.Agent, options ...RegistryOption) *MapRegistry {
	registry := &MapRegistry{
		agent: agent,
		maps:  map[string]*cnat64s.MapObjectConfig{},
		users: map[string]map[string]struct{}{},
		log:   zap.NewNop(),
	}
	for _, option := range options {
		option(registry)
	}
	return registry
}

// Ensure returns the named map, creating and publishing it on first
// reference and replacing one no config references.
//
// The handle stays valid for as long as a config holds the map, and a
// worker count of zero derives it from the dataplane. A name a config of
// an earlier control plane still links fails with
// ErrMapLinkedByUntrackedConfig.
func (m *MapRegistry) Ensure(name string, workerCount uint16) (*cnat64s.MapObjectConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.ensureLocked(name, workerCount)
}

// ensureLocked is Ensure for a caller holding the lock.
//
// A map no config references is never handed out again. Its tables hold
// the sessions of the config that used it, created under that config's
// prefix and pool, and they would answer the next one: the map is deleted
// first and created anew. A deletion the dataplane refuses fails the call,
// and a later sweep retries it.
//
// A name the dataplane already holds is cleared first, or refused with
// ErrMapLinkedByUntrackedConfig while a config of an earlier control
// plane still links it.
func (m *MapRegistry) ensureLocked(name string, workerCount uint16) (*cnat64s.MapObjectConfig, error) {
	object, known := m.maps[name]
	if known && len(m.users[name]) != 0 {
		return object, nil
	}
	if known {
		if err := m.reclaimLocked(name); err != nil {
			return nil, fmt.Errorf(
				"failed to replace the unreferenced session map %q: %w",
				name,
				err,
			)
		}
	} else if err := m.clearLeftoverLocked(name); err != nil {
		return nil, err
	}
	// The map about to be created takes the room of every map no config
	// references anymore, whatever its name.
	//
	// A release the dataplane refused a moment ago gets another chance
	// here first; a map whose memory a generation still holds stays
	// deferred.
	m.reclaimUnreferencedLocked()

	if workerCount == 0 {
		workerCount = uint16(m.agent.DPConfig().WorkerCount())
	}
	sizing := m.sizing
	sizing.WorkerCount = workerCount

	object, err := cnat64s.NewMapObjectConfig(m.agent, name)
	if err != nil {
		return nil, err
	}
	if err := object.CreateMap(sizing); err != nil {
		_ = object.Free()
		return nil, err
	}
	if err := object.Publish(m.agent); err != nil {
		_ = object.Free()
		return nil, err
	}

	m.maps[name] = object
	return object, nil
}

// clearLeftoverLocked makes sure the dataplane holds no map under a name
// the registry does not know.
//
// Such a map was left by an earlier control plane, whose published
// configs this one cannot see. One that a published config still links is
// refused, as a replacement would be linked by that config just the
// same; one that nothing links is deleted, since nothing reaches its
// sessions anymore.
func (m *MapRegistry) clearLeftoverLocked(name string) error {
	err := cnat64s.DeleteMapObject(m.agent, name)
	switch {
	case err == nil, errors.Is(err, ffi.ErrNotFound):
		return nil
	case errors.Is(err, ffi.ErrBusy):
		return fmt.Errorf(
			"%w: %q, delete that config first: %w",
			ErrMapLinkedByUntrackedConfig,
			name,
			err,
		)
	default:
		return fmt.Errorf(
			"failed to clear a leftover session map %q: %w",
			name,
			err,
		)
	}
}

// sweep reclaims one map's parked layers: a barrier, the unlink, a second
// barrier, then the release. A map with nothing to reclaim answers from
// its own pointers and skips the barriers.
//
// The lock is held from the check that the registry still lists the map
// to the end: a reference frees a replaced map under the same lock, and
// without it this sweep could keep reading a freed map.
func (m *MapRegistry) sweep(name string, object *cnat64s.MapObjectConfig, now uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.maps[name] != object || !object.HasReclaimable(now) {
		return
	}

	if err := object.Publish(m.agent); err != nil {
		m.log.Error("generation barrier failed before stale-layer unlink",
			zap.String("map", name),
			zap.Error(err),
		)
		return
	}
	if err := object.UnlinkStaleLayers(now); err != nil {
		m.log.Error("failed to unlink stale layers",
			zap.String("map", name),
			zap.Error(err),
		)
		return
	}
	if err := object.Publish(m.agent); err != nil {
		m.log.Error("generation barrier failed after stale-layer unlink; parked layers retained",
			zap.String("map", name),
			zap.Error(err),
		)
		return
	}
	object.FreeStaleLayers()
}

// sweepAll reclaims every registered map at the dataplane's clock.
func (m *MapRegistry) sweepAll() {
	m.mu.Lock()
	registered := maps.Clone(m.maps)
	m.mu.Unlock()

	// Without the dataplane's clock no layer can be judged stale; the
	// maps no config references are reclaimed all the same.
	if now, ok := m.agent.DPConfig().CurrentTime(); ok {
		for name, object := range registered {
			m.sweep(name, object, now)
		}
	}

	m.ReclaimUnreferenced()
}

// RunSweeper reclaims stale session layers until the context is
// cancelled.
func (m *MapRegistry) RunSweeper(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultStaleLayerSweepInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sweepAll()
		}
	}
}

// Release drops a config's reference to the named map. The object stays
// until the next reclaim — a sweep's, or the allocation of a map another
// config asks for — deletes it; a config referencing the map again first
// replaces it with a fresh one.
func (m *MapRegistry) Release(config, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.users[name], config)
}

// ReclaimUnreferenced deletes the maps no config references anymore and
// retries the releases the dataplane refused earlier.
//
// The whole round runs under the lock: a map reads as unreferenced only
// while no config can begin referencing it, so an object is never deleted
// underneath a config about to link it.
func (m *MapRegistry) ReclaimUnreferenced() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.reclaimUnreferencedLocked()
}

// reclaimUnreferencedLocked deletes the maps no config references anymore
// and retries the releases the dataplane refused earlier.
//
// A map still listed by the dataplane refuses deletion and stays for a
// later round; one whose memory a generation still holds stays deferred.
func (m *MapRegistry) reclaimUnreferencedLocked() {
	m.releaseDeferredLocked()
	for name, object := range m.maps {
		if object == nil || len(m.users[name]) != 0 {
			continue
		}
		if err := m.reclaimLocked(name); err != nil {
			m.log.Warn("map deletion refused; retrying on later rounds",
				zap.String("map", name),
				zap.Error(err))
		}
	}
}

// reclaimLocked deletes one unreferenced map from the dataplane and from
// the registry.
//
// A map the dataplane no longer knows counts as deleted. Its memory is
// released once the generations listing it have drained; until then its
// handle stays deferred.
func (m *MapRegistry) reclaimLocked(name string) error {
	if err := cnat64s.DeleteMapObject(m.agent, name); err != nil &&
		!errors.Is(err, ffi.ErrNotFound) {
		return err
	}
	if object := m.maps[name]; object != nil {
		m.releaseLocked(object)
	}
	delete(m.maps, name)
	delete(m.users, name)
	return nil
}

// releaseLocked frees a map the dataplane no longer lists, and defers the
// handle when the free is refused.
func (m *MapRegistry) releaseLocked(object *cnat64s.MapObjectConfig) {
	err := object.Free()
	switch {
	case err == nil:
	case errors.Is(err, ffi.ErrStillReferenced):
		m.deferred = append(m.deferred, object)
	default:
		m.log.Error("failed to free a session map", zap.Error(err))
	}
}

// releaseDeferredLocked retries the deferred releases; the handles still
// refused stay deferred for the next round.
func (m *MapRegistry) releaseDeferredLocked() {
	kept := m.deferred[:0]
	for _, object := range m.deferred {
		switch err := object.Free(); {
		case errors.Is(err, ffi.ErrStillReferenced):
			kept = append(kept, object)
		case err != nil:
			m.log.Error("failed to free a session map", zap.Error(err))
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}

// MapOf returns the map name the config last acquired, or "".
func (m *MapRegistry) MapOf(config string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, users := range m.users {
		if _, ok := users[config]; ok {
			return name
		}
	}
	return ""
}

// Acquire reserves the named map for the config, creating the map on
// first reference, and anew when a map no config references is parked
// under the name: its sessions belong to the config that used it. It
// fails with ErrMapReserved while another config holds the map.
//
// A map holds the tables of exactly one instance: its session keys name
// no instance, so two configs on one map would translate through each
// other's pool address and prefix. The record is taken under the lock the
// creation holds, so no sweep can reclaim a freshly created map before
// its first user is written down.
//
// A map left by an earlier control plane and still linked by one of its
// configs fails with ErrMapLinkedByUntrackedConfig.
func (m *MapRegistry) Acquire(config, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for other := range m.users[name] {
		if other != config {
			return fmt.Errorf(
				"%w: %q is linked by config %q",
				ErrMapReserved,
				name,
				other,
			)
		}
	}
	if _, err := m.ensureLocked(name, 0); err != nil {
		return err
	}
	if m.users[name] == nil {
		m.users[name] = map[string]struct{}{}
	}
	m.users[name][config] = struct{}{}
	return nil
}

// ErrMapUnknown reports a map name no config has created.
var ErrMapUnknown = errors.New("nat64state map not found")

// ReadSessions returns one page of the named map's live sessions and the
// cursor to resume from. It creates nothing: an unknown map is
// ErrMapUnknown.
//
// The page is read under the registry's lock, under which every release
// of table memory happens as well, so none can free what the page walks
// while it walks it. The lock is taken per page, so the registry can
// recreate the map between two pages of one dump; the cursor then names
// slots of its new first layer (see cnat64s.MapObjectConfig.ReadSessions).
func (m *MapRegistry) ReadSessions(
	name string,
	cursor int64,
	now uint64,
	count uint32,
) ([]cnat64s.Session, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	object := m.maps[name]
	if object == nil {
		return nil, cursor, ErrMapUnknown
	}
	return object.ReadSessions(cursor, now, count)
}
