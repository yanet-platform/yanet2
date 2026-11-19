//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// Deliver hands a published generation's contexts to the workers of a
// dataplane that runs worker rounds on request, under its round lock.
type Deliver func(dp *layout.DpConfig, gen *layout.CpConfigGen)

// UpdateOption configures UpdateModules and UpdateObjects.
type UpdateOption func(*updateOptions)

type updateOptions struct {
	Deliver Deliver
}

// WithDelivery makes an update deliver the workers' contexts itself, as C
// does for an in-process dataplane that runs worker rounds on request,
// instead of waiting for the dataplane's assigner to deliver them.
func WithDelivery(deliver Deliver) UpdateOption {
	return func(m *updateOptions) {
		m.Deliver = deliver
	}
}

// ErrNotFound marks a delete of a module or an object the published
// generation does not hold.
var ErrNotFound = errors.New("not found")

// UpdateModules publishes a generation in which the module
// configurations replace those of their types and names, as
// cp_config_update_modules does, and returns once every worker runs it.
func UpdateModules(instance *zone.Instance, modules []*layout.CpModule, options ...UpdateOption) error {
	return publishGeneration(instance, options, func(cp *layout.CpConfig, dp *layout.DpConfig, old *layout.CpConfigGen) (*layout.CpConfigGen, error) {
		owners, err := holdEntities(cp, "module", len(modules), func(idx int) entityRef {
			module := modules[idx]
			return entityRef{Owner: module.Entity.Agent.Get(), Type: shm.String(module.Type[:]), Name: shm.String(module.Name[:])}
		})
		if err != nil {
			return nil, err
		}
		defer releaseOwners(owners)
		return buildFrom(cp, dp, old, func(gen *layout.CpConfigGen) error {
			return putModules(gen, modules)
		})
	})
}

// DeleteModule publishes a generation without the module configuration
// of a type and name, as cp_config_delete_module does, and returns once
// every worker runs it.
func DeleteModule(instance *zone.Instance, moduleType, name string, options ...UpdateOption) error {
	return publishGeneration(instance, options, func(cp *layout.CpConfig, dp *layout.DpConfig, old *layout.CpConfigGen) (*layout.CpConfigGen, error) {
		return buildFrom(cp, dp, old, func(gen *layout.CpConfigGen) error {
			if !entityRegistryReplace(&gen.ModuleRegistry.EntityRegistry, moduleKeys, moduleKeyHash(moduleType, name),
				moduleEqual(moduleType, name), nil,
			) {
				return fmt.Errorf("module %s:%s: %w", moduleType, name, ErrNotFound)
			}
			return nil
		})
	})
}

// publishGeneration builds a generation from the published one under the
// configuration lock, publishes it, and retires the one it replaces; like
// every hold of the lock in C, it sweeps the superseded agents on leaving.
func publishGeneration(instance *zone.Instance, options []UpdateOption,
	build func(cp *layout.CpConfig, dp *layout.DpConfig, old *layout.CpConfigGen) (*layout.CpConfigGen, error),
) error {
	opts := &updateOptions{}
	for _, option := range options {
		option(opts)
	}
	cp, dp := instance.Cp, instance.Dp
	lock := shm.LockPid(&cp.ConfigLock, int32(os.Getpid()))
	defer lock.Unlock()
	defer func() {
		shm.LockByte(&cp.AgentLock.Locked)
		agent.SweepLocked(cp)
		shm.UnlockByte(&cp.AgentLock.Locked)
	}()

	old := cp.CpConfigGen.Get()
	gen, err := build(cp, dp, old)
	if err != nil {
		return err
	}
	install(cp, dp, gen, old, opts.Deliver)
	return nil
}

// buildFrom copies a generation, changes the copy and builds its workers'
// contexts, or frees what it built.
//
// It holds the instance's allocator lock once for all of it, rather than
// once per block as each allocation would.
func buildFrom(cp *layout.CpConfig, dp *layout.DpConfig, old *layout.CpConfigGen, change func(gen *layout.CpConfigGen) error) (*layout.CpConfigGen, error) {
	shmalloc.Lock(&cp.BlockAllocator)
	defer shmalloc.Unlock(&cp.BlockAllocator)
	gen, err := newGeneration(cp, old)
	if err != nil {
		return nil, err
	}
	if err := change(gen); err != nil {
		freeGeneration(cp, gen)
		return nil, err
	}
	if err := attachWorkerContexts(gen, old, dp); err != nil {
		freeGeneration(cp, gen)
		return nil, err
	}
	return gen, nil
}

// putModules puts module configurations into a generation and checks
// that it holds every object they link.
func putModules(gen *layout.CpConfigGen, modules []*layout.CpModule) error {
	for _, module := range modules {
		if err := upsertModule(&gen.ModuleRegistry, module); err != nil {
			return fmt.Errorf("upsert module %s:%s: %w", shm.String(module.Type[:]), shm.String(module.Name[:]), err)
		}
	}
	for _, module := range modules {
		if module.ObjectCount == 0 {
			continue
		}
		for _, linked := range unsafe.Slice(module.Objects.Get(), module.ObjectCount) {
			if _, ok := lookupObject(gen, shm.String(linked.Type[:]), shm.String(linked.Name[:])); !ok {
				return fmt.Errorf("linked object %s:%s not found", shm.String(linked.Type[:]), shm.String(linked.Name[:]))
			}
		}
	}
	return nil
}

// attachWorkerContexts builds the workers' contexts of a generation and
// attaches them to it.
func attachWorkerContexts(gen, old *layout.CpConfigGen, dp *layout.DpConfig) error {
	roots, err := buildWorkerContexts(gen, old, dp.WorkerCount)
	if err != nil {
		return err
	}
	gen.ConfigGenEctxs.Set(roots)
	gen.ConfigGenEctxCount = dp.WorkerCount
	return nil
}

// entityRef is a module or an object an update publishes: its owning
// agent and its identity.
type entityRef struct {
	Owner *layout.Agent
	Type  string
	Name  string
}

// holdEntities checks that every one of count entities of a kind has a
// current agent and holds it against reclamation until the upserts count
// it; it refuses an entity that appears twice.
//
// entityOf reads an entity's owner and identity, and runs under the
// agent lock only: a sweep frees a superseded agent's memory, the
// entities in it included, under that lock alone.
func holdEntities(cp *layout.CpConfig, kind string, count int, entityOf func(idx int) entityRef) ([]*layout.Agent, error) {
	shm.LockByte(&cp.AgentLock.Locked)
	defer shm.UnlockByte(&cp.AgentLock.Locked)
	entities := make([]entityRef, count)
	for idx := range entities {
		entities[idx] = entityOf(idx)
	}
	owners := make([]*layout.Agent, 0, len(entities))
	for _, entity := range entities {
		if entity.Owner != nil && !agent.IsCurrent(cp, entity.Owner) {
			releaseOwners(owners)
			return nil, fmt.Errorf("%s %s:%s is owned by a superseded agent", kind, entity.Type, entity.Name)
		}
		if entity.Owner != nil {
			atomic.AddUint64(&entity.Owner.LoadedEntityCount, 1)
		}
		owners = append(owners, entity.Owner)
	}
	for idx, entity := range entities {
		for _, previous := range entities[:idx] {
			if entity.Type == previous.Type && entity.Name == previous.Name {
				releaseOwners(owners)
				return nil, fmt.Errorf("%s %s:%s appears more than once in one update", kind, entity.Type, entity.Name)
			}
		}
	}
	return owners, nil
}

func releaseOwners(owners []*layout.Agent) {
	for _, owner := range owners {
		if owner != nil {
			atomic.AddUint64(&owner.LoadedEntityCount, ^uint64(0))
		}
	}
}

// install publishes a built generation, waits until every worker runs it
// and retires the one it replaces.
func install(cp *layout.CpConfig, dp *layout.DpConfig, gen, old *layout.CpConfigGen, deliver Deliver) {
	shm.LockByte(&cp.GenLock.Locked)
	cp.CpConfigGen.Store(gen)
	shm.UnlockByte(&cp.GenLock.Locked)

	if deliver != nil {
		deliver(dp, gen)
	}
	waitForWorkers(dp, gen)
	release(cp, old)
}

// release retires a replaced generation under one hold of the instance's
// allocator lock.
func release(cp *layout.CpConfig, old *layout.CpConfigGen) {
	shmalloc.Lock(&cp.BlockAllocator)
	defer shmalloc.Unlock(&cp.BlockAllocator)
	retire(cp, old)
}

// waitForWorkers returns once every worker took its context of the
// generation and acknowledged it. In-process dataplanes that run worker
// rounds on request deliver contexts under their round lock and need no
// acknowledgement; there only the delivery is awaited.
func waitForWorkers(dp *layout.DpConfig, gen *layout.CpConfigGen) {
	workers := unsafe.Slice(dp.Workers.Get(), dp.WorkerCount)
	roots := unsafe.Slice(gen.ConfigGenEctxs.Get(), gen.ConfigGenEctxCount)
	for idx := range workers {
		worker := workers[idx].Get()
		expected := roots[idx].Get()
		for attempt := 0; ; attempt++ {
			delivered := worker.ConfigGenEctx.Load() == expected
			if delivered && (dp.ExternalWorkerRounds || atomic.LoadUint64(&worker.Gen) >= gen.Gen) {
				break
			}
			if attempt < 1024 {
				runtime.Gosched()
				continue
			}
			time.Sleep(time.Millisecond)
		}
	}
}
