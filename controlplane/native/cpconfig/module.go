package cpconfig

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// NewBlackhole builds a blackhole module configuration in the agent's
// memory: a module header and nothing else, as the C blackhole api does.
func NewBlackhole(owner *agent.Agent, name string) (*layout.CpModule, error) {
	block := shmalloc.ContextAlloc(&owner.Agent.MemoryContext, unsafe.Sizeof(layout.CpModule{}))
	if block == nil {
		return nil, fmt.Errorf("allocate config: %w", ErrNoMemory)
	}
	module := (*layout.CpModule)(block)
	if err := InitModule(module, owner, "blackhole", name); err != nil {
		shmalloc.ContextFree(&owner.Agent.MemoryContext, block, unsafe.Sizeof(layout.CpModule{}))
		return nil, err
	}
	return module, nil
}

// InitModule prepares the header every module configuration opens with,
// as cp_module_init does: the dataplane module it configures, its names,
// its own memory context under the agent's, the counters every module
// has and the link to the device named "".
func InitModule(module *layout.CpModule, owner *agent.Agent, moduleType, name string) error {
	*module = layout.CpModule{}
	index, ok := lookupDataplaneModule(owner.Instance.Dp, moduleType)
	if !ok {
		return fmt.Errorf("module type %q not found in dataplane config", moduleType)
	}
	module.DpModuleIdx = index
	shm.CopyString(module.Type[:], moduleType)
	shm.CopyString(module.Name[:], name)

	module.Entity.ConfigItem.Refcnt = 0
	module.Entity.Agent.Set(owner.Agent)
	shmalloc.ContextInitFrom(&module.Entity.MemoryContext, &owner.Agent.MemoryContext, name)

	counterRegistryInit(&module.CounterRegistry, &module.Entity.MemoryContext)
	for _, counter := range []struct {
		name string
		id   *uint64
	}{
		{"rx", &module.RxCounterId},
		{"tx", &module.TxCounterId},
		{"drop", &module.DropCounterId},
		{"pending_input", &module.PendingInputCounterId},
		{"pending_output", &module.PendingOutputCounterId},
	} {
		id, err := counterRegister(&module.CounterRegistry, counter.name, 2)
		if err != nil {
			return fmt.Errorf("register %q counter for module %q: %w", counter.name, name, err)
		}
		*counter.id = id
	}
	if _, err := linkDevice(module, ""); err != nil {
		return err
	}
	return nil
}

// lookupDataplaneModule returns the index of the dataplane's module type
// of that name.
func lookupDataplaneModule(dp *layout.DpConfig, name string) (uint64, bool) {
	first := dp.DpModules.Get()
	if first == nil {
		return 0, false
	}
	for idx, module := range unsafe.Slice(first, dp.ModuleCount) {
		if shm.String(module.Name[:]) == name {
			return uint64(idx), true
		}
	}
	return 0, false
}

// linkDevice returns the index of the device of that name among the
// module's devices, appending it when missing.
func linkDevice(module *layout.CpModule, name string) (uint64, error) {
	deviceSize := unsafe.Sizeof(layout.CpModuleDevice{})
	if first := module.Devices.Get(); first != nil {
		for idx, device := range unsafe.Slice(first, module.DeviceCount) {
			if shm.String(device.Name[:]) == name {
				return uint64(idx), nil
			}
		}
	}
	grown := shmalloc.ContextRealloc(&module.Entity.MemoryContext,
		unsafe.Pointer(module.Devices.Get()),
		deviceSize*uintptr(module.DeviceCount), deviceSize*uintptr(module.DeviceCount+1),
	)
	if grown == nil {
		return 0, fmt.Errorf("reallocate devices of module %q: %w", shm.String(module.Name[:]), ErrNoMemory)
	}
	devices := unsafe.Slice((*layout.CpModuleDevice)(grown), module.DeviceCount+1)
	shm.CopyString(devices[module.DeviceCount].Name[:], name)
	module.Devices.Set(&devices[0])
	index := module.DeviceCount
	module.DeviceCount++
	return index, nil
}
