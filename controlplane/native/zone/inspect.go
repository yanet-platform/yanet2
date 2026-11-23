//go:build linux && (amd64 || arm64)

package zone

import (
	"fmt"
	"os"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// Worker is one dataplane worker of an instance.
type Worker struct {
	Index       uint64 `json:"index"`
	CoreID      uint32 `json:"core_id"`
	RxBurstSize uint32 `json:"rx_burst_size"`
	// Generation is the configuration generation the worker last
	// acknowledged.
	Generation  uint64       `json:"generation"`
	Assignments []Assignment `json:"assignments"`
}

// Assignment is a device queue a worker polls.
type Assignment struct {
	DeviceID uint32 `json:"device_id"`
	QueueID  uint32 `json:"queue_id"`
	Weight   uint32 `json:"weight"`
}

// DataplaneModule is a module type the dataplane loaded.
type DataplaneModule struct {
	Name         string `json:"name"`
	PreparedSize uint64 `json:"prepared_size"`
}

// Module is a module configuration a generation publishes.
type Module struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Agent string `json:"agent"`
	// Generation is the configuration generation the module was
	// published in.
	Generation uint64 `json:"generation"`
}

// Agent is a control-plane agent registered in an instance.
type Agent struct {
	Name              string `json:"name"`
	Pid               int32  `json:"pid"`
	MemoryLimit       uint64 `json:"memory_limit"`
	Generation        uint64 `json:"generation"`
	LoadedEntityCount uint64 `json:"loaded_entity_count"`
	// Superseded counts the earlier agents of the same name still
	// holding memory.
	Superseded int `json:"superseded"`
}

// Workers returns the instance's workers.
func (m *Instance) Workers() ([]Worker, error) {
	first := m.Dp.Workers.Get()
	count := m.Dp.WorkerCount
	if count == 0 {
		return nil, nil
	}
	if first == nil {
		return nil, fmt.Errorf("instance %d: %d workers but no worker table", m.Index, count)
	}
	pointers := unsafe.Slice(first, count)
	workers := make([]Worker, 0, count)
	for idx := range pointers {
		worker := pointers[idx].Get()
		if worker == nil {
			return nil, fmt.Errorf("instance %d: worker %d missing", m.Index, idx)
		}
		entry := Worker{
			Index:       worker.Idx,
			CoreID:      worker.CoreId,
			RxBurstSize: worker.RxBurstSize,
			Generation:  worker.Gen,
		}
		if assignments := worker.Assignments.Get(); assignments != nil {
			for _, assignment := range unsafe.Slice(assignments, worker.AssignmentCount) {
				entry.Assignments = append(entry.Assignments, Assignment{
					DeviceID: assignment.DeviceId,
					QueueID:  assignment.QueueId,
					Weight:   assignment.Weight,
				})
			}
		}
		workers = append(workers, entry)
	}
	return workers, nil
}

// DataplaneModules returns the module types the dataplane loaded, under
// the dataplane zone's lock as C reads them.
func (m *Instance) DataplaneModules() []DataplaneModule {
	lock := shm.LockPid(&m.Dp.ConfigLock, int32(os.Getpid()))
	defer lock.Unlock()

	first := m.Dp.DpModules.Get()
	if first == nil {
		return nil
	}
	modules := make([]DataplaneModule, 0, m.Dp.ModuleCount)
	for _, module := range unsafe.Slice(first, m.Dp.ModuleCount) {
		modules = append(modules, DataplaneModule{
			Name:         shm.String(module.Name[:]),
			PreparedSize: module.PreparedSize,
		})
	}
	return modules
}

// Modules returns the module configurations the generation publishes, in
// registry order.
func (m *Generation) Modules() []Module {
	if m.Gen == nil {
		return nil
	}
	registry := &m.Gen.ModuleRegistry.EntityRegistry.Registry
	items := registry.Items.Get()
	if items == nil {
		return nil
	}
	slots := unsafe.Slice(items, registry.Capacity)
	// A free slot links to the next free one inside this array; a live
	// item never lies inside it.
	start := uintptr(unsafe.Pointer(items))
	end := start + uintptr(registry.Capacity)*unsafe.Sizeof(slots[0])

	var modules []Module
	for idx := range slots {
		item := slots[idx].Get()
		address := uintptr(unsafe.Pointer(item))
		if item == nil || (address >= start && address < end) {
			continue
		}
		// The registry item opens the entity, which opens the module.
		module := (*layout.CpModule)(unsafe.Pointer(item))
		entry := Module{
			Type:       shm.String(module.Type[:]),
			Name:       shm.String(module.Name[:]),
			Generation: module.Gen,
		}
		if agent := module.Entity.Agent.Get(); agent != nil {
			entry.Agent = shm.String(agent.Name[:])
		}
		modules = append(modules, entry)
	}
	return modules
}

// Agents returns the agents registered in the instance, under the agent
// registry lock.
func (m *Instance) Agents() []Agent {
	shm.LockSpin(&m.Cp.AgentLock.Locked)
	defer shm.UnlockSpin(&m.Cp.AgentLock.Locked)

	registry := m.Cp.AgentRegistry.Get()
	if registry == nil {
		return nil
	}
	pointers := registry.Agents(int(registry.Count))
	agents := make([]Agent, 0, len(pointers))
	for idx := range pointers {
		agent := pointers[idx].Get()
		if agent == nil {
			continue
		}
		entry := Agent{
			Name:              shm.String(agent.Name[:]),
			Pid:               agent.Pid,
			MemoryLimit:       agent.MemoryLimit,
			Generation:        agent.Gen,
			LoadedEntityCount: agent.LoadedEntityCount,
		}
		for previous := agent.Prev.Get(); previous != nil; previous = previous.Prev.Get() {
			entry.Superseded++
		}
		agents = append(agents, entry)
	}
	return agents
}
