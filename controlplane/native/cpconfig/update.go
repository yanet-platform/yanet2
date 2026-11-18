package cpconfig

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// UpdateModules publishes a generation in which the module
// configurations replace those of their types and names, as
// cp_config_update_modules does, and returns once every worker runs it.
func UpdateModules(instance *zone.Instance, modules []*layout.CpModule) error {
	cp, dp := instance.Cp, instance.Dp
	lock := shm.LockPid(&cp.ConfigLock, int32(os.Getpid()))
	defer lock.Unlock()
	defer func() {
		shm.LockByte(&cp.AgentLock.Locked)
		agent.SweepLocked(cp)
		shm.UnlockByte(&cp.AgentLock.Locked)
	}()

	owners, err := holdOwners(cp, modules)
	if err != nil {
		return err
	}
	old := cp.CpConfigGen.Get()
	gen, err := NewGeneration(cp, old)
	if err != nil {
		releaseOwners(owners)
		return err
	}
	for _, module := range modules {
		if err := UpsertModule(&gen.ModuleRegistry, module); err != nil {
			releaseOwners(owners)
			return fmt.Errorf("upsert module %s:%s: %w", shm.String(module.Type[:]), shm.String(module.Name[:]), err)
		}
	}
	for _, module := range modules {
		if module.ObjectCount == 0 {
			continue
		}
		for _, linked := range unsafe.Slice(module.Objects.Get(), module.ObjectCount) {
			if _, ok := lookupObject(gen, shm.String(linked.Type[:]), shm.String(linked.Name[:])); !ok {
				releaseOwners(owners)
				return fmt.Errorf("linked object %s:%s not found", shm.String(linked.Type[:]), shm.String(linked.Name[:]))
			}
		}
	}
	releaseOwners(owners)
	return install(cp, dp, gen, old)
}

// holdOwners checks that every module's agent is still current and holds
// it against reclamation until the upserts count it; it refuses a module
// that appears twice.
func holdOwners(cp *layout.CpConfig, modules []*layout.CpModule) ([]*layout.Agent, error) {
	shm.LockByte(&cp.AgentLock.Locked)
	defer shm.UnlockByte(&cp.AgentLock.Locked)
	owners := make([]*layout.Agent, 0, len(modules))
	for _, module := range modules {
		owner := module.Entity.Agent.Get()
		if owner != nil && !agent.IsCurrent(cp, owner) {
			releaseOwners(owners)
			return nil, fmt.Errorf("module %s:%s is owned by a superseded agent",
				shm.String(module.Type[:]), shm.String(module.Name[:]),
			)
		}
		if owner != nil {
			atomic.AddUint64(&owner.LoadedEntityCount, 1)
		}
		owners = append(owners, owner)
	}
	for idx, module := range modules {
		for _, previous := range modules[:idx] {
			if shm.String(module.Type[:]) == shm.String(previous.Type[:]) &&
				shm.String(module.Name[:]) == shm.String(previous.Name[:]) {
				releaseOwners(owners)
				return nil, fmt.Errorf("module %s:%s appears more than once in one update",
					shm.String(module.Type[:]), shm.String(module.Name[:]),
				)
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

// install builds the workers' contexts of a generation, publishes it,
// waits until every worker runs it and retires the one it replaces.
func install(cp *layout.CpConfig, dp *layout.DpConfig, gen, old *layout.CpConfigGen) error {
	roots, err := BuildWorkerContexts(gen, old, dp.WorkerCount)
	if err != nil {
		return err
	}
	gen.ConfigGenEctxs.Set(roots)
	gen.ConfigGenEctxCount = dp.WorkerCount

	shm.LockByte(&cp.GenLock.Locked)
	cp.CpConfigGen.Store(gen)
	shm.UnlockByte(&cp.GenLock.Locked)

	waitForWorkers(dp, gen)
	retire(cp, old)
	return nil
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
