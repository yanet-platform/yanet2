package nat64s

import (
	"context"
	"fmt"
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

// MapRegistry owns the nat64state map objects this module created: it
// creates them on first reference, and its sweeper reclaims the layers
// their tables park.
//
// Every operation that changes the registry or a map publishes a new
// object generation, which waits for every dataplane worker to advance —
// the RCU grace period layer reclamation requires.
//
// The registry creates each map with its first layer only. That layer
// admits a fixed number of sessions in total, expired ones keeping their
// key slots, and the registry inserts no further one: once it is used up,
// the map refuses every new session.
type MapRegistry struct {
	agent *ffi.Agent
	mu    sync.Mutex
	maps  map[string]*cnat64s.MapObjectConfig
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
// reference. A worker count of zero derives it from the dataplane.
func (m *MapRegistry) Ensure(name string, workerCount uint16) (*cnat64s.MapObjectConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.ensureLocked(name, workerCount)
}

// ensureLocked is Ensure for a caller holding the lock.
func (m *MapRegistry) ensureLocked(name string, workerCount uint16) (*cnat64s.MapObjectConfig, error) {
	if object, ok := m.maps[name]; ok {
		return object, nil
	}

	if workerCount == 0 {
		workerCount = uint16(m.agent.DPConfig().WorkerCount())
	}

	object, err := cnat64s.NewMapObjectConfig(m.agent, name)
	if err != nil {
		return nil, err
	}
	if err := object.CreateMap(cnat64s.MapConfig{WorkerCount: workerCount}); err != nil {
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

// sweep reclaims one map's parked layers: a barrier, the unlink, a second
// barrier, then the release. A map with nothing to reclaim answers from
// its own pointers and skips the barriers.
func (m *MapRegistry) sweep(object *cnat64s.MapObjectConfig, now uint64) {
	if !object.HasReclaimable(now) {
		return
	}

	if err := object.Publish(m.agent); err != nil {
		m.log.Error("generation barrier failed before stale-layer unlink",
			zap.String("map", "nat64state_map"),
			zap.Error(err),
		)
		return
	}
	if err := object.UnlinkStaleLayers(now); err != nil {
		m.log.Error("failed to unlink stale layers",
			zap.String("map", "nat64state_map"),
			zap.Error(err),
		)
		return
	}
	if err := object.Publish(m.agent); err != nil {
		m.log.Error("generation barrier failed after stale-layer unlink; parked layers retained",
			zap.String("map", "nat64state_map"),
			zap.Error(err),
		)
		return
	}
	object.FreeStaleLayers()
}

// sweepAll reclaims every registered map at the dataplane's clock.
func (m *MapRegistry) sweepAll() {
	m.mu.Lock()
	objects := make([]*cnat64s.MapObjectConfig, 0, len(m.maps))
	for _, object := range m.maps {
		objects = append(objects, object)
	}
	m.mu.Unlock()

	now, ok := m.agent.DPConfig().CurrentTime()
	if !ok {
		return
	}
	for _, object := range objects {
		m.sweep(object, now)
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

// Release drops a config's reference to the named map; the object stays
// parked for the sweeper, which reclaims it once no published config
// references it anymore.
func (m *MapRegistry) Release(config, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.users[name], config)
}

// ReclaimUnreferenced deletes the objects no config references anymore. A map
// still referenced by a published module config refuses deletion; the
// next sweep retries.
//
// The whole round runs under the lock: a map reads as unreferenced only
// while no config can begin referencing it, so an object is never deleted
// underneath a config about to link it.
func (m *MapRegistry) ReclaimUnreferenced() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, object := range m.maps {
		if object == nil || len(m.users[name]) != 0 {
			continue
		}
		if err := cnat64s.DeleteMapObject(m.agent, name); err != nil {
			m.log.Warn("map deletion refused; retrying on later sweeps",
				zap.String("map", name),
				zap.Error(err))
			continue
		}
		_ = object.Free()
		delete(m.maps, name)
		delete(m.users, name)
	}
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
// first reference. It fails with ErrMapReserved while another config
// holds the map.
//
// A map holds the tables of exactly one instance: its session keys name
// no instance, so two configs on one map would translate through each
// other's pool address and prefix. The record is taken under the lock the
// creation holds, so no sweep can reclaim a freshly created map before
// its first user is written down.
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
