//go:build linux && (amd64 || arm64)

package zone

import (
	"fmt"
	"os"
	"sync/atomic"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
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

// DataplaneObject is an object type the dataplane loaded.
type DataplaneObject struct {
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

// Object is an object a generation publishes.
type Object struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Agent string `json:"agent"`
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

// Workers returns the instance's workers (attach: trust).
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
			Generation:  atomic.LoadUint64(&worker.Gen),
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
// the dataplane zone's lock as C reads them (attach: trust).
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

// DataplaneObjects returns the object types the dataplane loaded, in
// load order, under the dataplane zone's lock as module types are read
// (attach: trust).
//
// They are the types dp_config_lookup_object searches.
func (m *Instance) DataplaneObjects() []DataplaneObject {
	lock := shm.LockPid(&m.Dp.ConfigLock, int32(os.Getpid()))
	defer lock.Unlock()

	var objects []DataplaneObject
	for _, object := range registry.Slice(m.Dp.DpObjects.Get(), m.Dp.ObjectCount) {
		objects = append(objects, DataplaneObject{
			Name:         shm.String(object.Name[:]),
			PreparedSize: object.PreparedSize,
		})
	}
	return objects
}

// Modules returns the module configurations the generation publishes, in
// registry order (attach: trust).
func (m *Generation) Modules() []Module {
	if m.Gen == nil {
		return nil
	}
	var modules []Module
	for _, item := range registry.Items(&m.Gen.ModuleRegistry.EntityRegistry.Registry) {
		module := registry.Container[layout.CpModule](item, 0)
		modules = append(modules, Module{
			Type:       shm.String(module.Type[:]),
			Name:       shm.String(module.Name[:]),
			Agent:      ownerName(item),
			Generation: module.Gen,
		})
	}
	return modules
}

// Objects returns the objects the generation publishes, in registry
// order, as cp_config_gen_get_object reads them slot by slot (attach:
// trust).
func (m *Generation) Objects() []Object {
	if m.Gen == nil {
		return nil
	}
	var objects []Object
	for _, item := range registry.Items(&m.Gen.ObjectRegistry.EntityRegistry.Registry) {
		object := registry.Container[layout.CpObject](item, 0)
		objects = append(objects, Object{
			Type:  shm.String(object.Type[:]),
			Name:  shm.String(object.Name[:]),
			Agent: ownerName(item),
		})
	}
	return objects
}

// ownerName returns the name of the agent an entity belongs to, or an
// empty one for an entity of none.
func ownerName(item *layout.RegistryItem) string {
	if owner := registry.Owner(item); owner != nil {
		return shm.String(owner.Name[:])
	}
	return ""
}

// Agents returns the agents registered in the instance, under the agent
// registry lock (attach: trust).
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
			LoadedEntityCount: atomic.LoadUint64(&agent.LoadedEntityCount),
		}
		for previous := agent.Prev.Get(); previous != nil; previous = previous.Prev.Get() {
			entry.Superseded++
		}
		agents = append(agents, entry)
	}
	return agents
}
