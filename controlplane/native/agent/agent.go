//go:build linux && (amd64 || arm64)

// Package agent attaches a control-plane agent to a dataplane instance
// without cgo, sharing one registry with the C control plane.
//
// An attach allocates as the C one does, in the same order, so the storage
// ends byte-identical; it registers the agent, superseding one of the same
// name, and frees superseded agents no configuration holds. The agent lock
// is always taken outside the allocator's, as in C. An attach checks the
// name, the instance's readiness and that the generation it pins lies in
// the instance; the rest relies on the storage reader's attach assumptions.
package agent

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// NameSize is the capacity of an agent name, counting its terminating
// zero (AGENT_NAME_LEN).
const NameSize = len(layout.Agent{}.Name)

// ErrNoMemory reports that the control-plane zone has no room left.
var ErrNoMemory = fmt.Errorf("control-plane zone: %w", shmalloc.ErrNoMemory)

// Agent is an agent this process attached.
type Agent struct {
	Instance *zone.Instance
	Agent    *layout.Agent
}

// Attach registers an agent of that name in the instance, with arenas
// totalling the memory limit, superseding an agent of the same name.
//
// A failed attach returns what it allocated to the zone, as C does. A
// generation a publication retires while the attach pins it stays
// allocated until native generation teardown lands.
func Attach(instance *zone.Instance, name string, memoryLimit uint64) (*Agent, error) {
	if len(name) >= NameSize {
		return nil, fmt.Errorf("agent name is longer than %d characters", NameSize-1)
	}
	// A NUL would cut the stored name short, so the agent would take the
	// place of another agent whose name is that prefix.
	if strings.IndexByte(name, 0) >= 0 {
		return nil, fmt.Errorf("agent name %q contains a NUL byte", name)
	}
	if atomic.LoadUint64(&instance.Dp.ReadyMagic) != layout.DpConfigReadyMagic {
		return nil, fmt.Errorf("%w: instance %d", zone.ErrNotReady, instance.Index)
	}
	cp := instance.Cp
	defer sweep(cp)

	agent := (*layout.Agent)(zeroed(&cp.MemoryContext, unsafe.Sizeof(layout.Agent{})))
	if agent == nil {
		return nil, fmt.Errorf("allocate agent: %w", ErrNoMemory)
	}
	shm.CopyString(agent.Name[:], name)
	agent.MemoryLimit = memoryLimit
	agent.DpConfig.Set(instance.Dp)
	agent.CpConfig.Set(cp)
	agent.Pid = int32(os.Getpid())
	shmalloc.Init(&agent.BlockAllocator)
	shmalloc.ContextInit(&agent.MemoryContext, name, &agent.BlockAllocator)

	if err := reserveArenas(cp, agent, memoryLimit); err != nil {
		cleanup(agent)
		return nil, err
	}

	// The pin is dropped after the agent lock, as C drops it.
	generation, err := stampAndRegister(instance, agent)
	if generation != nil {
		generation.Release()
	}
	if err != nil {
		cleanup(agent)
		return nil, err
	}
	return &Agent{Instance: instance, Agent: agent}, nil
}

// stampAndRegister stamps the agent with the published generation and
// registers it under the agent lock, returning the generation still pinned.
//
// The caller releases the pin once the lock is dropped. The stamp is read
// under the lock of the splice, so a stalled attach cannot register an
// older stamp above a later one.
func stampAndRegister(instance *zone.Instance, agent *layout.Agent) (*zone.Generation, error) {
	cp := instance.Cp
	shm.LockSpin(&cp.AgentLock.Locked)
	defer shm.UnlockSpin(&cp.AgentLock.Locked)

	generation, err := instance.Pin()
	if err != nil {
		return nil, err
	}
	// A ready instance always publishes a generation; without one the
	// stamp stays zero.
	if generation.Gen != nil {
		agent.Gen = generation.Gen.Gen
	}
	return generation, register(cp, agent)
}

// reserveArenas takes the memory limit from the zone, in blocks of the
// largest size, and gives them to the agent's allocator.
//
// On failure everything it took is back in the zone and the agent holds
// no arenas.
func reserveArenas(cp *layout.CpConfig, agent *layout.Agent, memoryLimit uint64) error {
	count := uintptr(0)
	if memoryLimit != 0 {
		count = uintptr((memoryLimit-1)/shmalloc.MaxSize + 1)
	}
	// A zero limit fails after the agent is allocated, as in C.
	if count == 0 {
		return errors.New("agent memory limit is zero")
	}
	arenaSize := unsafe.Sizeof(layout.AgentArena{})
	table := zeroed(&cp.MemoryContext, arenaSize*count)
	if table == nil {
		return fmt.Errorf("allocate arena table: %w", ErrNoMemory)
	}
	arenas := unsafe.Slice((*layout.AgentArena)(table), count)

	left := memoryLimit
	for idx := range arenas {
		size := min(left, shmalloc.MaxSize)
		data := shmalloc.ContextAlloc(&cp.MemoryContext, uintptr(size))
		if data == nil {
			for reserved := range idx {
				shmalloc.ContextFree(&cp.MemoryContext, unsafe.Pointer(arenas[reserved].Data.Get()), uintptr(arenas[reserved].Size))
			}
			shmalloc.ContextFree(&cp.MemoryContext, table, arenaSize*count)
			return fmt.Errorf("allocate arena %d of %d: %w", idx+1, count, ErrNoMemory)
		}
		arenas[idx].Data.Set((*byte)(data))
		arenas[idx].Size = size
		left -= size
	}

	agent.Arenas.Set(&arenas[0])
	agent.ArenaCount = uint64(count)
	for idx := range arenas {
		shmalloc.PutArena(&agent.BlockAllocator, unsafe.Pointer(arenas[idx].Data.Get()), uintptr(arenas[idx].Size))
	}
	return nil
}

// register puts the agent into the registry, superseding the agent of its
// name or appending it; the agent lock is held.
//
// The dataplane creates the registry with its zone (attach: trust).
func register(cp *layout.CpConfig, agent *layout.Agent) error {
	old := cp.AgentRegistry.Get()
	slots := old.Agents(int(old.Count))
	name := shm.String(agent.Name[:])
	for idx := range slots {
		existing := slots[idx].Get()
		if shm.String(existing.Name[:]) == name {
			slots[idx].Set(agent)
			agent.Prev.Set(existing)
			return nil
		}
	}

	count := len(slots)
	agent.Prev.Set(nil)
	slotSize := unsafe.Sizeof(shm.Ptr[layout.Agent]{})
	headerSize := unsafe.Sizeof(layout.CpAgentRegistry{})
	block := shmalloc.ContextAlloc(&cp.MemoryContext, headerSize+slotSize*uintptr(count+1))
	if block == nil {
		return fmt.Errorf("allocate agent registry: %w", ErrNoMemory)
	}
	registry := (*layout.CpAgentRegistry)(block)
	registry.Count = uint64(count + 1)
	grown := registry.Agents(count + 1)
	for idx := range slots {
		grown[idx].Set(slots[idx].Get())
	}
	grown[count].Set(agent)
	shmalloc.ContextFree(&cp.MemoryContext, unsafe.Pointer(old), headerSize+slotSize*uintptr(count))
	cp.AgentRegistry.Set(registry)
	return nil
}

// sweep frees the superseded agents whose configurations no generation
// holds any more, under the agent lock.
//
// A loaded count read stale and high only postpones a reclamation to a
// later sweep; the load acquires the releasing decrements, so an agent is
// freed after every access that preceded its last release.
func sweep(cp *layout.CpConfig) {
	shm.LockSpin(&cp.AgentLock.Locked)
	defer shm.UnlockSpin(&cp.AgentLock.Locked)

	registry := cp.AgentRegistry.Get()
	if registry == nil {
		return
	}
	slots := registry.Agents(int(registry.Count))
	for idx := range slots {
		agent := slots[idx].Get()
		for agent != nil && !agent.Prev.IsNil() {
			previous := agent.Prev.Get()
			if atomic.LoadUint64(&previous.LoadedEntityCount) == 0 {
				agent.Prev.Set(previous.Prev.Get())
				cleanup(previous)
				continue
			}
			agent = previous
		}
	}
}

// cleanup returns an agent, its arenas and its arena table to the zone.
//
// The agent's memory context is finalised first, since its children may
// live in those arenas.
func cleanup(agent *layout.Agent) {
	cp := agent.CpConfig.Get()
	shmalloc.ContextFini(&agent.MemoryContext)
	if table := agent.Arenas.Get(); table != nil {
		arenas := unsafe.Slice(table, agent.ArenaCount)
		for idx := range arenas {
			shmalloc.ContextFree(&cp.MemoryContext, unsafe.Pointer(arenas[idx].Data.Get()), uintptr(arenas[idx].Size))
		}
		shmalloc.ContextFree(
			&cp.MemoryContext, unsafe.Pointer(table),
			unsafe.Sizeof(layout.AgentArena{})*uintptr(agent.ArenaCount),
		)
	}
	shmalloc.ContextFree(&cp.MemoryContext, unsafe.Pointer(agent), unsafe.Sizeof(layout.Agent{}))
}

// zeroed allocates size bytes from the context and zeroes them, or
// returns nil.
func zeroed(context *layout.MemoryContext, size uintptr) unsafe.Pointer {
	block := shmalloc.ContextAlloc(context, size)
	if block != nil {
		clear(unsafe.Slice((*byte)(block), size))
	}
	return block
}
