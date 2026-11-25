//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/teardown"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// NewBlackhole builds a blackhole module configuration in the agent's
// memory: a module header and nothing else, as the C blackhole api does.
func NewBlackhole(owner *agent.Agent, name string) (*layout.CpModule, error) {
	if err := CheckModuleNames("blackhole", name); err != nil {
		return nil, err
	}
	context := &owner.Agent.MemoryContext
	module := registry.AllocNode[layout.CpModule](context, 0)
	if module == nil {
		return nil, fmt.Errorf("allocate blackhole module %q: %w", name, errAgentMemory)
	}
	if err := initModule(module, owner, "blackhole", name); err != nil {
		registry.FreeNode(context, module, 0)
		return nil, err
	}
	return module, nil
}

// FreeBlackhole frees a blackhole module configuration no generation
// holds, as blackhole_module_config_free does.
//
// One a generation holds is refused as still referenced, and nothing is
// freed. The release of the agent's hold reclaims the agent when a later
// attach superseded it during the free.
func FreeBlackhole(module *layout.CpModule) error {
	if err := TryDestroyModule(module); err != nil {
		return err
	}
	owner := module.Entity.Agent.Get()
	FiniModule(module)
	registry.FreeNode(&owner.MemoryContext, module, 0)
	DestroyReleased(owner)
	return nil
}

// InitModule prepares the header every module configuration opens with,
// as cp_module_init does.
//
// The header names the dataplane module it configures and itself, and
// holds its own memory context under the agent's, the counters every
// module has and the link to the device named "". A type or name its
// field cannot hold whole is refused before the header is touched; a
// failed header is finalised again, its memory context unlinked from the
// agent's.
func InitModule(module *layout.CpModule, owner *agent.Agent, moduleType, name string) error {
	if err := CheckModuleNames(moduleType, name); err != nil {
		return err
	}
	return initModule(module, owner, moduleType, name)
}

// initModule prepares a module header of a type and name already
// checked.
func initModule(module *layout.CpModule, owner *agent.Agent, moduleType, name string) error {
	*module = layout.CpModule{}
	index, ok := lookupDataplaneModule(owner.Instance.Dp, moduleType)
	if !ok {
		return fmt.Errorf("module type %q not found in dataplane config", moduleType)
	}
	module.DpModuleIdx = index
	shm.CopyString(module.Type[:], moduleType)
	shm.CopyString(module.Name[:], name)

	module.Entity.Agent.Set(owner.Agent)
	shmalloc.ContextInitFrom(&module.Entity.MemoryContext, &owner.Agent.MemoryContext, name)

	if err := initModuleCounters(module); err != nil {
		FiniModule(module)
		return err
	}
	return nil
}

// ErrInvalidName marks a type or name its field cannot hold whole.
var ErrInvalidName = errors.New("invalid name")

// nameField is a type or name given for a C character field of that
// size.
type nameField struct {
	Kind  string
	Value string
	Size  int
}

// checkNames refuses a type or name its field cannot hold whole.
//
// C would cut it short, and a NUL would end it early, so the entity would
// answer to another identity and a link would not find its own copy.
func checkNames(entity string, fields ...nameField) error {
	for _, field := range fields {
		if len(field.Value) >= field.Size {
			return fmt.Errorf(
				"%w: %s %s %q is longer than %d bytes",
				ErrInvalidName, entity, field.Kind, field.Value, field.Size-1,
			)
		}
		if strings.IndexByte(field.Value, 0) >= 0 {
			return fmt.Errorf("%w: %s %s %q contains a NUL byte", ErrInvalidName, entity, field.Kind, field.Value)
		}
	}
	return nil
}

// CheckModuleNames refuses a module type or name its field cannot hold
// whole.
//
// A typed build checks them before it allocates its configuration, so a
// refused name leaves the agent untouched.
func CheckModuleNames(moduleType, name string) error {
	return checkNames(
		"module",
		nameField{"type", moduleType, len(layout.CpModule{}.Type)},
		nameField{"name", name, len(layout.CpModule{}.Name)},
	)
}

// initModuleCounters registers the counters every module has and links
// the device named "".
func initModuleCounters(module *layout.CpModule) error {
	counterRegistryInit(&module.CounterRegistry, &module.Entity.MemoryContext)
	counters := []struct {
		Name string
		ID   *uint64
	}{
		{"rx", &module.RxCounterId},
		{"tx", &module.TxCounterId},
		{"drop", &module.DropCounterId},
		{"pending_input", &module.PendingInputCounterId},
		{"pending_output", &module.PendingOutputCounterId},
	}
	for _, counter := range counters {
		id, err := RegisterCounter(&module.CounterRegistry, counter.Name, 2)
		*counter.ID = id
		if err != nil {
			return fmt.Errorf(
				"register %q counter for module %q: %w",
				counter.Name, shm.String(module.Name[:]), err,
			)
		}
	}
	_, err := linkDevice(module, "")
	return err
}

// FiniModule frees what a module header holds and unlinks its memory
// context from the agent's, as cp_module_fini does.
//
// A typed free or a failed typed build runs it once the module's own
// data is gone and before the module's block is returned to the agent.
func FiniModule(module *layout.CpModule) {
	context := &module.Entity.MemoryContext
	teardown.FiniCounterRegistry(&module.CounterRegistry)
	registries := runtimeRegistries(module)
	for idx := range registries {
		entry := registries[idx].Get()
		teardown.FiniCounterRegistry(&entry.Registry)
		registry.FreeNode(context, entry, 0)
	}
	registry.FreeArray(context, registries)
	module.RuntimeCounterRegistries.Set(nil)
	module.RuntimeCounterRegistryCount = 0

	registry.FreeArray(context, registry.Slice(module.Devices.Get(), module.DeviceCount))
	module.Devices.Set(nil)
	module.DeviceCount = 0

	registry.FreeArray(context, registry.Slice(module.Objects.Get(), module.ObjectCount))
	module.Objects.Set(nil)
	module.ObjectCount = 0

	module.Entity.Agent.Set(nil)
	shmalloc.ContextFini(context)
}

// lookupDataplaneModule returns the index of the dataplane's module type
// of that name.
func lookupDataplaneModule(dp *layout.DpConfig, name string) (uint64, bool) {
	for idx, module := range registry.Slice(dp.DpModules.Get(), dp.ModuleCount) {
		if shm.Equal(module.Name[:], name) {
			return uint64(idx), true
		}
	}
	return 0, false
}

// LinkDevice returns the index of the named device among the module's
// devices, appending it when missing, as cp_module_link_device does.
//
// The module's handler finds the device's targets at that index in its
// context. A name its field cannot hold whole is refused before the
// module is touched.
func LinkDevice(module *layout.CpModule, name string) (uint64, error) {
	if err := checkNames("device", nameField{"name", name, len(layout.CpModuleDevice{}.Name)}); err != nil {
		return 0, err
	}
	return linkDevice(module, name)
}

// linkDevice is LinkDevice for a name already checked.
func linkDevice(module *layout.CpModule, name string) (uint64, error) {
	for idx, device := range registry.Slice(module.Devices.Get(), module.DeviceCount) {
		if shm.Equal(device.Name[:], registry.FieldKey(name, len(device.Name))) {
			return uint64(idx), nil
		}
	}
	context := &module.Entity.MemoryContext
	devices := registry.Slice(module.Devices.Get(), module.DeviceCount)
	grown, ok := registry.AllocArray[layout.CpModuleDevice](context, module.DeviceCount+1)
	if !ok {
		return 0, fmt.Errorf("grow devices of module %q: %w", shm.String(module.Name[:]), errAgentMemory)
	}
	copy(grown, devices)
	registry.FreeArray(context, devices)
	index := module.DeviceCount
	shm.CopyString(grown[index].Name[:], name)
	module.Devices.Set(&grown[0])
	module.DeviceCount++
	return index, nil
}
