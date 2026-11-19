//go:build linux && (amd64 || arm64)

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
		Name string
		ID   *uint64
	}{
		{"rx", &module.RxCounterId},
		{"tx", &module.TxCounterId},
		{"drop", &module.DropCounterId},
		{"pending_input", &module.PendingInputCounterId},
		{"pending_output", &module.PendingOutputCounterId},
	} {
		id, err := counterRegister(&module.CounterRegistry, counter.Name, 2)
		if err != nil {
			return fmt.Errorf("register %q counter for module %q: %w", counter.Name, name, err)
		}
		*counter.ID = id
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
		if shm.Equal(module.Name[:], name) {
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
			if shm.Equal(device.Name[:], name) {
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

// LinkDevice returns the index of the device of that name among the
// devices a module links, appending it when missing, as
// cp_module_link_device does.
func LinkDevice(module *layout.CpModule, name string) (uint64, error) {
	return linkDevice(module, name)
}

// ModuleCounterRegistry returns the module's counter registry of a tag
// and its index among the module's runtime registries, adding an empty
// one when missing, as cp_module_counter_registry does. A registry holds
// self-relative offsets, so each lives in its own block and growing only
// moves the array of pointers to them.
func ModuleCounterRegistry(module *layout.CpModule, tag string) (*layout.CounterRegistry, uint64, error) {
	entries := runtimeRegistries(module)
	for idx := range entries {
		if entry := entries[idx].Get(); shm.Equal(entry.Tag[:], tag) {
			return &entry.Registry, uint64(idx), nil
		}
	}
	context := &module.Entity.MemoryContext
	entrySize := unsafe.Sizeof(layout.CpModuleCounterRegistry{})
	block := shmalloc.ContextAlloc(context, entrySize)
	if block == nil {
		return nil, 0, fmt.Errorf("allocate counter registry %q of module %q: %w", tag, shm.String(module.Name[:]), ErrNoMemory)
	}
	entry := (*layout.CpModuleCounterRegistry)(block)
	*entry = layout.CpModuleCounterRegistry{}
	shm.CopyString(entry.Tag[:], tag)
	counterRegistryInit(&entry.Registry, context)

	pointerSize := unsafe.Sizeof(shm.Ptr[layout.CpModuleCounterRegistry]{})
	count := len(entries)
	grown := shmalloc.ContextAlloc(context, pointerSize*uintptr(count+1))
	if grown == nil {
		counterRegistryFini(&entry.Registry)
		shmalloc.ContextFree(context, block, entrySize)
		return nil, 0, fmt.Errorf("allocate counter registries of module %q: %w", shm.String(module.Name[:]), ErrNoMemory)
	}
	pointers := unsafe.Slice((*shm.Ptr[layout.CpModuleCounterRegistry])(grown), count+1)
	for idx := range entries {
		pointers[idx].Set(entries[idx].Get())
	}
	pointers[count].Set(entry)
	if count > 0 {
		shmalloc.ContextFree(context, unsafe.Pointer(module.RuntimeCounterRegistries.Get()), pointerSize*uintptr(count))
	}
	module.RuntimeCounterRegistries.Set(&pointers[0])
	module.RuntimeCounterRegistryCount = uint64(count + 1)
	return &entry.Registry, uint64(count), nil
}
