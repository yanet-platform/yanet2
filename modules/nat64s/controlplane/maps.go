package nat64s

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/nat64s/bindings/go/cnat64s"
)

// DefaultStaleLayerSweepInterval is the default period between stale-layer
// sweeps of the session tables.
const DefaultStaleLayerSweepInterval = 30 * time.Second

// mapRegistry owns the nat64state map objects this module created: it
// creates them on first reference, and its sweeper reclaims the layers
// their tables park.
//
// Every operation that changes the registry or a map publishes a new
// object generation, which waits for every dataplane worker to advance —
// the RCU grace period layer reclamation requires.
type mapRegistry struct {
	agent *ffi.Agent
	mu    sync.Mutex
	maps  map[string]*cnat64s.MapObjectConfig
	// The configs referencing each map, so the object is reclaimed
	// when its last user goes away: each map owns two large tables,
	// and config churn with fresh names would otherwise exhaust the
	// agent arena.
	users map[string]map[string]struct{}

	log *zap.Logger
}

// registryOption configures the mapRegistry constructor.
type registryOption func(*mapRegistry)

// withRegistryLog sets the registry's logger.
func withRegistryLog(log *zap.Logger) registryOption {
	return func(m *mapRegistry) {
		if log != nil {
			m.log = log
		}
	}
}

func newMapRegistry(agent *ffi.Agent, options ...registryOption) *mapRegistry {
	registry := &mapRegistry{
		agent: agent,
		maps:  map[string]*cnat64s.MapObjectConfig{},
		log:   zap.NewNop(),
	}
	for _, option := range options {
		option(registry)
	}
	return registry
}

// Ensure returns the named map, creating and publishing it on first
// reference. A worker count of zero derives it from the dataplane.
func (m *mapRegistry) Ensure(name string, workerCount uint16) (*cnat64s.MapObjectConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

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
func (m *mapRegistry) sweep(object *cnat64s.MapObjectConfig, now uint64) {
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
func (m *mapRegistry) sweepAll() {
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

	m.reclaimFree()
}

// RunSweeper reclaims stale session layers until the context is
// cancelled.
func (m *mapRegistry) RunSweeper(ctx context.Context, interval time.Duration) {
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
func (m *mapRegistry) Release(config, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.users[name], config)
}

// reclaimFree deletes the objects no config references anymore. A map
// still referenced by a published module config refuses deletion; the
// next sweep retries.
func (m *mapRegistry) reclaimFree() {
	m.mu.Lock()
	type pending struct {
		MapName string
		Map     *cnat64s.MapObjectConfig
	}
	var work []pending
	for name, object := range m.maps {
		if object != nil && len(m.users[name]) == 0 {
			work = append(work, pending{MapName: name, Map: object})
		}
	}
	m.mu.Unlock()

	for _, item := range work {
		if err := cnat64s.DeleteMapObject(m.agent, item.MapName); err != nil {
			m.log.Warn("map deletion refused; retrying on later sweeps",
				zap.String("map", item.MapName), zap.Error(err))
			continue
		}
		_ = item.Map.Free()
		m.mu.Lock()
		delete(m.maps, item.MapName)
		delete(m.users, item.MapName)
		m.mu.Unlock()
	}
}

// MapOf returns the map name the config last acquired, or "".
func (m *mapRegistry) MapOf(config string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, users := range m.users {
		if _, ok := users[config]; ok {
			return name
		}
	}
	return ""
}

// Acquire records a config as a user of the named map, creating the map
// on first reference.
func (m *mapRegistry) Acquire(config, name string) error {
	if _, err := m.Ensure(name, 0); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.users[name] == nil {
		m.users[name] = map[string]struct{}{}
	}
	m.users[name][config] = struct{}{}
	return nil
}
