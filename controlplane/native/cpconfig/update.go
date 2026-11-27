//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/teardown"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

var (
	// ErrSupersededOwner marks a configuration whose agent a later attach
	// of the same name superseded; C refuses it as a failed precondition.
	ErrSupersededOwner = errors.New("owned by a superseded agent")
	// ErrRepeated marks an update that names one configuration twice; C
	// refuses it as a failed precondition.
	ErrRepeated = errors.New("appears more than once in one update")
	// ErrMissingObject marks a module linking an object the generation
	// does not publish; C refuses it as a failed precondition.
	ErrMissingObject = errors.New("linked object not found")
	// ErrMissingModule marks a chain running a module the generation does
	// not publish; C refuses it as a failed precondition.
	ErrMissingModule = errors.New("chained module not found")
	// ErrMissingFunction marks a pipeline running a function the
	// generation does not publish; C refuses it as a failed precondition.
	ErrMissingFunction = errors.New("pipeline function not found")
	// ErrMissingPipeline marks a device entry running a pipeline the
	// generation does not publish; C refuses it as a failed precondition.
	ErrMissingPipeline = errors.New("device pipeline not found")
	// ErrNeedsDelivery marks an update to a dataplane that runs worker
	// rounds on request made without a delivery.
	ErrNeedsDelivery = errors.New("update needs a delivery")
	// ErrNotFound marks a delete of a configuration the published
	// generation does not hold; C refuses it as not found.
	ErrNotFound = errors.New("not found")
	// ErrObjectLinked marks a delete of an object a module of the
	// published generation links; C refuses it as busy.
	ErrObjectLinked = errors.New("linked by a published module")
)

// Deliver hands a published generation's contexts to the workers of a
// dataplane that runs worker rounds on request, under its round lock.
type Deliver func(dp *layout.DpConfig, gen *layout.CpConfigGen)

// UpdateOption configures an update or a delete.
type UpdateOption func(*updateOptions)

type updateOptions struct {
	Deliver Deliver
}

// WithDelivery hands the workers' contexts to a dataplane that runs
// worker rounds on request, as C's publication does for it.
//
// Such a dataplane, an in-process harness, has no assigner of its own to
// deliver them, and Go cannot run its commit handlers, so an update to it
// needs this option. A dataplane with workers of its own ignores it.
func WithDelivery(deliver Deliver) UpdateOption {
	return func(m *updateOptions) {
		m.Deliver = deliver
	}
}

// UpdateModules publishes a generation in which the module configurations
// replace those of their types and names, as cp_config_update_modules does.
//
// It returns once every worker runs the generation, with no timeout, as C
// does. A nil module, a module whose agent was superseded, a module named
// twice and one linking an unpublished object are refused, and nothing is
// published. The caller passes configurations built for the update; one
// whose update failed is freed, not passed again, and only the
// configuration currently published may be passed again.
func UpdateModules(instance *zone.Instance, modules []*layout.CpModule, options ...UpdateOption) error {
	for idx, module := range modules {
		if module == nil {
			return fmt.Errorf("module %d of the update is nil", idx)
		}
	}
	return publish(instance, options, change{
		Kind:  "module",
		Count: len(modules),
		Entity: func(idx int) entityRef {
			module := modules[idx]
			return entityRef{
				Owner: module.Entity.Agent.Get(),
				Type:  shm.String(module.Type[:]),
				Name:  shm.String(module.Name[:]),
			}
		},
		Put: func(m *build) error {
			return m.putModules(modules)
		},
	})
}

// DeleteModule publishes a generation without the module configuration
// of a type and name, as cp_config_delete_module does.
//
// It returns once every worker runs the generation. A type or name its
// field cannot hold whole is refused before the instance is touched. A
// module the published generation does not hold is refused as not found
// after the copy, which is freed, and one a chain still runs fails the
// contexts' build; neither publishes anything.
func DeleteModule(instance *zone.Instance, moduleType, name string, options ...UpdateOption) error {
	if err := CheckModuleNames(moduleType, name); err != nil {
		return err
	}
	return publish(instance, options, change{
		Put: func(m *build) error {
			if !registry.ReplaceEntity(
				&m.gen.ModuleRegistry.EntityRegistry, moduleHash, registry.ModuleKeyHash(moduleType, name),
				registry.ModuleMatch(moduleType, name), nil,
			) {
				return fmt.Errorf("delete module %s:%s: %w", moduleType, name, ErrNotFound)
			}
			return nil
		},
	})
}

// change is what an update or a delete publishes and how it goes into
// the generation.
type change struct {
	// The configurations an update publishes are all of one kind; a delete
	// publishes none.
	Kind  string
	Count int
	// Entity reads a configuration's owner and identity, under the agent
	// lock or with the owner held.
	Entity func(idx int) entityRef
	// Check, when set, refuses the change against the published
	// generation before it is copied.
	Check func(old *layout.CpConfigGen) error
	// Put puts the change into the copy of the published generation.
	Put func(m *build) error
}

// publish publishes a generation that carries a change, in C's lock
// order, and retires the generation it replaces.
//
// Under the configuration lock it holds the agents of an update's
// configurations, checks the change, copies the published generation,
// puts the change into the copy, builds the workers' contexts and
// installs the result. A failed step frees the generation it built and
// publishes nothing. Like every hold of the configuration lock in C, it
// sweeps the superseded agents before leaving, failed builds included.
func publish(instance *zone.Instance, options []UpdateOption, update change) error {
	var opts updateOptions
	for _, option := range options {
		option(&opts)
	}
	cp, dp := instance.Cp, instance.Dp
	if dp.ExternalWorkerRounds && opts.Deliver == nil {
		return fmt.Errorf("instance %d runs worker rounds on request: %w", instance.Index, ErrNeedsDelivery)
	}

	lock := shm.LockPid(&cp.ConfigLock, int32(os.Getpid()))
	defer lock.Unlock()
	defer sweepAgents(cp)

	var owners []*layout.Agent
	if update.Entity != nil {
		var err error
		if owners, err = holdEntities(cp, update.Kind, update.Count, update.Entity); err != nil {
			return err
		}
	}

	old := cp.CpConfigGen.Get()
	if update.Check != nil {
		if err := update.Check(old); err != nil {
			releaseOwners(owners)
			return err
		}
	}
	m := newBuild(cp, old)
	if err := m.generation(update.Put); err != nil {
		releaseOwners(owners)
		return err
	}
	// The holds outlive the build's own counts but not the install, as in
	// C: a displaced owner whose hold was its last reference drains here,
	// and the sweep after the install reclaims it at once.
	releaseOwners(owners)
	install(cp, dp, m.gen, old, opts.Deliver)
	return nil
}

// generation copies the published generation, puts a change into the
// copy and builds the copy's workers' contexts.
//
// A failed step frees the copy, as cp_config_update_modules frees it.
func (m *build) generation(put func(m *build) error) error {
	if err := m.copyGeneration(); err != nil {
		return err
	}
	if err := put(m); err != nil {
		teardown.FreeGeneration(m.cp, m.gen)
		return err
	}
	roots, err := m.contexts(m.dp.WorkerCount)
	if err != nil {
		teardown.FreeGeneration(m.cp, m.gen)
		return err
	}
	m.gen.ConfigGenEctxs.Set(roots)
	m.gen.ConfigGenEctxCount = m.dp.WorkerCount
	return nil
}

// sweepAgents reclaims the superseded agents no generation counts any
// longer.
func sweepAgents(cp *layout.CpConfig) {
	shm.LockSpin(&cp.AgentLock.Locked)
	defer shm.UnlockSpin(&cp.AgentLock.Locked)
	teardown.SweepAgentsLocked(cp)
}

// putModules upserts module configurations into the generation and
// checks that it publishes every object they link, as C's update does.
func (m *build) putModules(modules []*layout.CpModule) error {
	for _, module := range modules {
		if err := m.upsertModule(module); err != nil {
			return fmt.Errorf("upsert module %s:%s: %w", shm.String(module.Type[:]), shm.String(module.Name[:]), err)
		}
	}
	for _, module := range modules {
		for _, linked := range registry.Slice(module.Objects.Get(), module.ObjectCount) {
			objectType, objectName := shm.String(linked.Type[:]), shm.String(linked.Name[:])
			if _, ok := registry.LookupObjectIndex(&m.gen.ObjectRegistry, objectType, objectName); !ok {
				return fmt.Errorf(
					"module %s:%s: %w: %s:%s",
					shm.String(module.Type[:]), shm.String(module.Name[:]),
					ErrMissingObject, objectType, objectName,
				)
			}
		}
	}
	return nil
}

// entityRef is a configuration an update publishes: its owning agent and
// its identity, of a name alone when the type is empty.
type entityRef struct {
	Owner *layout.Agent
	Type  string
	Name  string
}

// String names the configuration as its type and name, or its name.
func (m entityRef) String() string {
	if m.Type == "" {
		return m.Name
	}
	return m.Type + ":" + m.Name
}

// holdEntities holds the current agent of every configuration of an
// update against reclamation until the update counts it.
//
// It refuses a configuration of a superseded agent and one named twice.
// The configurations are read under the agent lock: a sweep frees a
// superseded agent's memory, the configurations in it included, under
// that lock alone.
func holdEntities(cp *layout.CpConfig, kind string, count int, entityOf func(idx int) entityRef) ([]*layout.Agent, error) {
	shm.LockSpin(&cp.AgentLock.Locked)
	defer shm.UnlockSpin(&cp.AgentLock.Locked)
	entities := make([]entityRef, count)
	for idx := range entities {
		entities[idx] = entityOf(idx)
	}
	owners := make([]*layout.Agent, 0, count)
	for _, entity := range entities {
		if entity.Owner != nil && !agent.IsCurrent(cp, entity.Owner) {
			releaseOwners(owners)
			return nil, fmt.Errorf("%s %s: %w", kind, entity, ErrSupersededOwner)
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
				return nil, fmt.Errorf("%s %s: %w", kind, entity, ErrRepeated)
			}
		}
	}
	return owners, nil
}

// releaseOwners drops the holds an update took on its configurations'
// agents.
func releaseOwners(owners []*layout.Agent) {
	for _, owner := range owners {
		if owner != nil {
			atomic.AddUint64(&owner.LoadedEntityCount, ^uint64(0))
		}
	}
}

// install publishes a built generation, returns once every worker runs
// it and retires the one it replaces, as cp_config_gen_install does.
func install(cp *layout.CpConfig, dp *layout.DpConfig, gen, old *layout.CpConfigGen, deliver Deliver) {
	shm.LockSpin(&cp.GenLock.Locked)
	cp.CpConfigGen.Store(gen)
	shm.UnlockSpin(&cp.GenLock.Locked)

	if dp.ExternalWorkerRounds {
		deliver(dp, gen)
	} else {
		waitForWorkers(dp, gen)
	}
	retire(cp, old)
}

// retire drops the publication's reference to a replaced generation and
// frees it unless a reader still pins it.
//
// A pinning reader's release frees it then. The agents the free leaves
// unreferenced are reclaimed by the sweep the configuration lock's holder
// makes before leaving.
func retire(cp *layout.CpConfig, old *layout.CpConfigGen) {
	if atomic.AddUint64(&old.Refcnt, ^uint64(0)) == 0 {
		teardown.FreeGeneration(cp, old)
	}
}

// waitForWorkers returns once every worker took its context of the
// generation and acknowledged it, as dp_config_wait_for_gen waits.
//
// It yields first, then sleeps a millisecond between checks.
func waitForWorkers(dp *layout.DpConfig, gen *layout.CpConfigGen) {
	workers := registry.Slice(dp.Workers.Get(), dp.WorkerCount)
	roots := registry.Slice(gen.ConfigGenEctxs.Get(), gen.ConfigGenEctxCount)
	for idx := range workers {
		worker := workers[idx].Get()
		expected := roots[idx].Get()
		for attempt := 0; ; attempt++ {
			if worker.ConfigGenEctx.Load() == expected && atomic.LoadUint64(&worker.Gen) >= gen.Gen {
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
