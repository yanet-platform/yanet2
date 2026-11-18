//go:build linux && amd64

// Package agent attaches a control-plane agent to a dataplane instance
// without cgo, as lib/controlplane/agent does: the agent and its arenas
// come from the control-plane zone, and the agent registry and the chain
// of superseded agents change under the agent lock, so C and Go agents
// live in one registry.
package agent

import (
	"errors"
	"fmt"
	"os"
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
var ErrNoMemory = errors.New("control-plane zone exhausted")

// Agent is an agent this process attached.
type Agent struct {
	Instance *zone.Instance
	Agent    *layout.Agent
}

// Attach registers an agent of that name in the instance, with
// memoryLimit bytes of arenas, superseding an agent of the same name.
func Attach(instance *zone.Instance, name string, memoryLimit uint64) (*Agent, error) {
	if len(name) >= NameSize {
		return nil, fmt.Errorf("agent name is longer than %d characters", NameSize-1)
	}
	if atomic.LoadUint64(&instance.Dp.ReadyMagic) != zone.ReadyMagic {
		return nil, zone.ErrNotReady
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

	shm.LockByte(&cp.AgentLock.Locked)
	// The stamp is taken under the lock of the splice; the pin is
	// dropped after it.
	generation := instance.Pin()
	agent.Gen = generation.Gen.Gen
	err := register(cp, agent)
	shm.UnlockByte(&cp.AgentLock.Locked)
	generation.Release()
	if err != nil {
		cleanup(agent)
		return nil, err
	}
	return &Agent{Instance: instance, Agent: agent}, nil
}

// reserveArenas takes memoryLimit bytes from the zone, in blocks of the
// largest size, and gives them to the agent's allocator.
func reserveArenas(cp *layout.CpConfig, agent *layout.Agent, memoryLimit uint64) error {
	count := uintptr(0)
	if memoryLimit != 0 {
		count = uintptr((memoryLimit-1)/shmalloc.MaxSize + 1)
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
			return fmt.Errorf("allocate arena: %w", ErrNoMemory)
		}
		arenas[idx].Data.Set((*byte)(data))
		arenas[idx].Size = size
		left -= size
	}

	if count != 0 {
		agent.Arenas.Set(&arenas[0])
	}
	agent.ArenaCount = uint64(count)
	for idx := range arenas {
		shmalloc.PutArena(&agent.BlockAllocator, unsafe.Pointer(arenas[idx].Data.Get()), uintptr(arenas[idx].Size))
	}
	return nil
}

// register puts the agent into the registry, superseding the agent of its
// name or appending it. The agent lock is held.
func register(cp *layout.CpConfig, agent *layout.Agent) error {
	old := cp.AgentRegistry.Get()
	count := int(old.Count)
	slots := old.Agents(count)
	for idx := range slots {
		existing := slots[idx].Get()
		if shm.String(existing.Name[:]) == shm.String(agent.Name[:]) {
			slots[idx].Set(agent)
			agent.Prev.Set(existing)
			return nil
		}
	}

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
// holds any more.
func sweep(cp *layout.CpConfig) {
	shm.LockByte(&cp.AgentLock.Locked)
	defer shm.UnlockByte(&cp.AgentLock.Locked)
	SweepLocked(cp)
}

// SweepLocked frees the superseded agents whose configurations no
// generation holds any more; the agent lock is held.
func SweepLocked(cp *layout.CpConfig) {

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

// cleanup returns an agent, its arenas and its table to the zone.
func cleanup(agent *layout.Agent) {
	cp := agent.CpConfig.Get()
	shmalloc.ContextFini(&agent.MemoryContext)
	if table := agent.Arenas.Get(); table != nil {
		arenas := unsafe.Slice(table, agent.ArenaCount)
		for idx := range arenas {
			shmalloc.ContextFree(&cp.MemoryContext, unsafe.Pointer(arenas[idx].Data.Get()), uintptr(arenas[idx].Size))
		}
		shmalloc.ContextFree(&cp.MemoryContext, unsafe.Pointer(table),
			unsafe.Sizeof(layout.AgentArena{})*uintptr(agent.ArenaCount),
		)
	}
	shmalloc.ContextFree(&cp.MemoryContext, unsafe.Pointer(agent), unsafe.Sizeof(layout.Agent{}))
}

// IsCurrent reports whether agent is the one its name resolves to in the
// registry; the agent lock is held.
func IsCurrent(cp *layout.CpConfig, agent *layout.Agent) bool {
	registry := cp.AgentRegistry.Get()
	if registry == nil {
		return false
	}
	slots := registry.Agents(int(registry.Count))
	name := shm.String(agent.Name[:])
	for idx := range slots {
		if head := slots[idx].Get(); shm.String(head.Name[:]) == name {
			return head == agent
		}
	}
	return false
}

// Find returns the current agent of that name in the instance, whichever
// process attached it.
func Find(instance *zone.Instance, name string) (*Agent, bool) {
	cp := instance.Cp
	shm.LockByte(&cp.AgentLock.Locked)
	defer shm.UnlockByte(&cp.AgentLock.Locked)
	registry := cp.AgentRegistry.Get()
	if registry == nil {
		return nil, false
	}
	slots := registry.Agents(int(registry.Count))
	for idx := range slots {
		if found := slots[idx].Get(); found != nil && shm.String(found.Name[:]) == name {
			return &Agent{Instance: instance, Agent: found}, true
		}
	}
	return nil, false
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
