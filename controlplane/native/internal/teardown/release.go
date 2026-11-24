//go:build linux && (amd64 || arm64)

package teardown

import (
	"sync/atomic"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// ReleaseGeneration drops a reference to a generation, as
// cp_config_gen_release does outside the configuration lock.
//
// The last reference frees the generation and then reclaims the
// superseded agents the free left unreferenced: a retired generation is
// published nowhere, so no later holder may come to do it. The caller
// holds neither the agent lock nor an allocator lock.
func ReleaseGeneration(cp *layout.CpConfig, gen *layout.CpConfigGen) {
	if atomic.AddUint64(&gen.Refcnt, ^uint64(0)) != 0 {
		return
	}
	FreeGeneration(cp, gen)

	shm.LockSpin(&cp.AgentLock.Locked)
	defer shm.UnlockSpin(&cp.AgentLock.Locked)
	SweepAgentsLocked(cp)
}

// SweepAgentsLocked frees the superseded agents no generation holds any
// more, as cp_config_reclaim_unused_agents_locked does.
//
// The caller holds the agent lock. A loaded count read stale and high
// only postpones a reclamation to a later sweep; the load acquires the
// releasing decrements, so an agent is freed after every access that
// preceded its last release.
func SweepAgentsLocked(cp *layout.CpConfig) {
	agents := cp.AgentRegistry.Get()
	if agents == nil {
		return
	}
	for agent := range registry.Pointees(agents.Agents(int(agents.Count))) {
		for !agent.Prev.IsNil() {
			previous := agent.Prev.Get()
			if atomic.LoadUint64(&previous.LoadedEntityCount) == 0 {
				agent.Prev.Set(previous.Prev.Get())
				FreeAgent(previous)
				continue
			}
			agent = previous
		}
	}
}

// FreeAgent returns an agent, its arenas and its arena table to the zone,
// as agent_cleanup does.
//
// The agent's memory context is finalised first, since its children may
// live in those arenas. Its frees take their allocators' locks.
func FreeAgent(agent *layout.Agent) {
	cp := agent.CpConfig.Get()
	shmalloc.ContextFini(&agent.MemoryContext)
	arenas := registry.Slice(agent.Arenas.Get(), agent.ArenaCount)
	for idx := range arenas {
		registry.FreeArray(&cp.MemoryContext, registry.Slice(arenas[idx].Data.Get(), arenas[idx].Size))
	}
	registry.FreeArray(&cp.MemoryContext, arenas)
	registry.FreeNode(&cp.MemoryContext, agent, 0)
}
