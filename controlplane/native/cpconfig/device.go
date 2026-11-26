//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/internal/teardown"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// DevicePipelineConfig is a pipeline a device entry runs, with the weight
// its packets are spread by.
type DevicePipelineConfig struct {
	Name   string
	Weight uint64
}

// DeviceConfig is a device: its name and the pipelines its input and
// output entries run.
type DeviceConfig struct {
	Name   string
	Input  []DevicePipelineConfig
	Output []DevicePipelineConfig
}

// NewPlainDevice builds a plain device in the agent's memory: a device
// header and nothing else, as cp_device_plain_new does.
//
// Names its fields cannot hold whole and pipeline weights past the bound
// are refused before the agent is touched.
func NewPlainDevice(owner *agent.Agent, config DeviceConfig) (*layout.CpDevice, error) {
	if err := checkDevice("plain", config); err != nil {
		return nil, err
	}
	context := &owner.Agent.MemoryContext
	device := registry.AllocNode[layout.CpDevice](context, 0)
	if device == nil {
		return nil, fmt.Errorf("allocate plain device %q: %w", config.Name, errAgentMemory)
	}
	if err := initDevice(device, owner, "plain", config); err != nil {
		registry.FreeNode(context, device, 0)
		return nil, err
	}
	return device, nil
}

// FreePlainDevice frees a plain device no generation holds, as
// cp_device_plain_free does.
//
// One a generation holds is refused as still referenced, and nothing is
// freed. The release of the agent's hold reclaims the agent when a later
// attach superseded it during the free.
func FreePlainDevice(device *layout.CpDevice) error {
	if err := TryDestroyDevice(device); err != nil {
		return err
	}
	owner := device.Entity.Agent.Get()
	FiniDevice(device)
	registry.FreeNode(&owner.MemoryContext, device, 0)
	DestroyReleased(owner)
	return nil
}

// InitDevice prepares the header every device opens with, as
// cp_device_init does.
//
// The header names the dataplane device type it configures and itself,
// and holds its own memory context under the agent's, its input and
// output entries with their pipelines and the counters each entry has.
// Names its fields cannot hold whole and pipeline weights past the bound
// are refused before the header is touched; a failed header is finalised
// again, its memory context unlinked from the agent's. No generation
// references the device until an update publishes it.
func InitDevice(device *layout.CpDevice, owner *agent.Agent, deviceType string, config DeviceConfig) error {
	if err := checkDevice(deviceType, config); err != nil {
		return err
	}
	return initDevice(device, owner, deviceType, config)
}

// checkDevice refuses a device whose names its fields cannot hold whole
// or whose pipeline weighs past the bound, as C's device configuration
// refuses the weight.
func checkDevice(deviceType string, config DeviceConfig) error {
	err := checkNames(
		"device",
		nameField{"type", deviceType, len(layout.CpDevice{}.Type)},
		nameField{"name", config.Name, len(layout.CpDevice{}.Name)},
	)
	if err != nil {
		return err
	}
	for _, pipelines := range [][]DevicePipelineConfig{config.Input, config.Output} {
		for _, pipeline := range pipelines {
			if pipeline.Weight > maxWeightSum {
				return fmt.Errorf(
					"device %q: pipeline %q weighs %d, past %d",
					config.Name, pipeline.Name, pipeline.Weight, maxWeightSum,
				)
			}
			err := checkNames("device pipeline", nameField{"name", pipeline.Name, len(layout.CpDevicePipeline{}.Name)})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// initDevice prepares a device header of a configuration already checked.
func initDevice(device *layout.CpDevice, owner *agent.Agent, deviceType string, config DeviceConfig) error {
	*device = layout.CpDevice{}
	index, ok := lookupDataplaneDevice(owner.Instance.Dp, deviceType)
	if !ok {
		return fmt.Errorf("device type %q not found in dataplane config", deviceType)
	}
	device.DpDeviceIdx = index
	shm.CopyString(device.Type[:], deviceType)
	shm.CopyString(device.Name[:], config.Name)

	device.Entity.Agent.Set(owner.Agent)
	context := &device.Entity.MemoryContext
	shmalloc.ContextInitFrom(context, &owner.Agent.MemoryContext, config.Name)

	input, err := newDeviceEntry(context, config.Input)
	if err != nil {
		FiniDevice(device)
		return fmt.Errorf("input pipelines of device %q: %w", config.Name, err)
	}
	device.InputPipelines.Set(input)
	output, err := newDeviceEntry(context, config.Output)
	if err != nil {
		FiniDevice(device)
		return fmt.Errorf("output pipelines of device %q: %w", config.Name, err)
	}
	device.OutputPipelines.Set(output)

	counterRegistryInit(&device.CounterRegistry, context)
	for _, entry := range []struct {
		Direction string
		Entry     *layout.CpDeviceEntry
	}{{"input", input}, {"output", output}} {
		counters := []struct {
			Name string
			ID   *uint64
		}{
			{"rx", &entry.Entry.CounterPacketRx},
			{"entry", &entry.Entry.CounterPacketEntry},
			{"tx", &entry.Entry.CounterPacketTx},
			{"drop", &entry.Entry.CounterPacketDrop},
			{"recirc_drop", &entry.Entry.CounterPacketRecircDrop},
			{"pending_input", &entry.Entry.CounterPacketPendingInput},
			{"pending_output", &entry.Entry.CounterPacketPendingOutput},
		}
		for _, counter := range counters {
			name := entry.Direction + "_" + counter.Name
			id, err := RegisterCounter(&device.CounterRegistry, name, 2)
			if err != nil {
				FiniDevice(device)
				return fmt.Errorf("register %q counter for device %q: %w", name, config.Name, err)
			}
			*counter.ID = id
		}
	}
	return nil
}

// newDeviceEntry builds a device entry of weighted pipelines in the
// device's memory, as cp_device_entry_new does.
func newDeviceEntry(context *layout.MemoryContext, pipelines []DevicePipelineConfig) (*layout.CpDeviceEntry, error) {
	count := uint64(len(pipelines))
	entry := newNode[layout.CpDeviceEntry](context, unsafe.Sizeof(layout.CpDevicePipeline{})*uintptr(count))
	if entry == nil {
		return nil, fmt.Errorf("allocate device entry: %w", errAgentMemory)
	}
	entry.PipelineCount = count
	named := entry.Pipelines(int(count))
	for idx, pipeline := range pipelines {
		shm.CopyString(named[idx].Name[:], pipeline.Name)
		named[idx].Weight = pipeline.Weight
	}
	return entry, nil
}

// FiniDevice frees what a device header holds and unlinks its memory
// context from the agent's, as cp_device_fini does.
//
// A typed free or a failed typed build runs it before the device's block
// is returned to the agent.
func FiniDevice(device *layout.CpDevice) {
	context := &device.Entity.MemoryContext
	teardown.FiniCounterRegistry(&device.CounterRegistry)
	for _, entry := range []*shm.Ptr[layout.CpDeviceEntry]{&device.OutputPipelines, &device.InputPipelines} {
		if built := entry.Get(); built != nil {
			registry.FreeNode(context, built, unsafe.Sizeof(layout.CpDevicePipeline{})*uintptr(built.PipelineCount))
		}
		entry.Set(nil)
	}
	device.Entity.Agent.Set(nil)
	shmalloc.ContextFini(context)
}

// TryDestroyDevice takes a device no generation holds out of circulation
// for its typed free, as cp_device_try_destroy does.
//
// On success the device's agent is held for the free, and the caller
// returns that hold with DestroyReleased once the device is freed.
func TryDestroyDevice(device *layout.CpDevice) error {
	// The identity is read before the gate, as for a module.
	deviceType, name := shm.String(device.Type[:]), shm.String(device.Name[:])
	if !tryDestroy(&device.Entity) {
		return fmt.Errorf("device %s:%s: %w", deviceType, name, ErrStillReferenced)
	}
	return nil
}

// UpdateDevices publishes a generation in which the devices replace those
// of their types and names, as cp_config_update_devices does.
//
// It returns once every worker runs the generation, with no timeout, as C
// does. A nil device, a device whose agent was superseded and two devices
// of one name are refused, and a device entry running a pipeline the
// generation does not publish fails the contexts' build; nothing is
// published then. Each device continues the counters of the one it
// replaces. The caller passes devices built for the update; one whose
// update failed is freed, not passed again, and only the device currently
// published may be passed again.
func UpdateDevices(instance *zone.Instance, devices []*layout.CpDevice, options ...UpdateOption) error {
	for idx, device := range devices {
		if device == nil {
			return fmt.Errorf("device %d of the update is nil", idx)
		}
	}
	return publish(instance, options, change{
		Kind:  "device",
		Count: len(devices),
		Entity: func(idx int) entityRef {
			device := devices[idx]
			// An update tells its devices apart by name alone, as C
			// compares them.
			return entityRef{Owner: device.Entity.Agent.Get(), Name: shm.String(device.Name[:])}
		},
		Put: func(m *build) error {
			for _, device := range devices {
				if err := m.upsertDevice(device); err != nil {
					return fmt.Errorf("upsert device %q: %w", shm.String(device.Name[:]), err)
				}
			}
			return nil
		},
	})
}

// upsertDevice puts a device into the generation's registry, as
// cp_device_registry_upsert does.
//
// It replaces the device of its type and name, whose counters it
// continues. The caller holds the instance's configuration lock and the
// device's agent current.
func (m *build) upsertDevice(device *layout.CpDevice) error {
	devices := &m.gen.DeviceRegistry.EntityRegistry
	deviceType, name := shm.String(device.Type[:]), shm.String(device.Name[:])
	var oldCounters *layout.CounterRegistry
	if index, found := registry.Lookup(&devices.Registry, deviceKeyHash(deviceType, name), deviceMatch(deviceType, name)); found {
		old := registry.Container[layout.CpDevice](registry.Item(&devices.Registry, index), 0)
		oldCounters = &old.CounterRegistry
	}
	if err := linkCounters(&device.CounterRegistry, oldCounters); err != nil {
		return fmt.Errorf("link counter registry: %w", err)
	}
	if !registry.ReplaceEntity(
		devices, deviceHash, deviceKeyHash(deviceType, name),
		deviceMatch(deviceType, name), &device.Entity.ConfigItem,
	) {
		return fmt.Errorf("replace device in registry: %w", errZoneMemory)
	}
	return nil
}

// deviceKeyHash hashes a device's type and name as the device registry's
// index does.
func deviceKeyHash(deviceType, name string) uint64 {
	return registry.HashNamePair(
		registry.FieldKey(name, len(layout.CpDevice{}.Name)),
		registry.FieldKey(deviceType, len(layout.CpDevice{}.Type)),
	)
}

// deviceMatch matches the device of a type and name.
func deviceMatch(deviceType, name string) registry.Match {
	return func(item *layout.RegistryItem) bool {
		device := registry.Container[layout.CpDevice](item, 0)
		return shm.Equal(device.Name[:], registry.FieldKey(name, len(device.Name))) &&
			shm.Equal(device.Type[:], registry.FieldKey(deviceType, len(device.Type)))
	}
}

// lookupDataplaneDevice returns the index of the dataplane's device type
// of that name.
func lookupDataplaneDevice(dp *layout.DpConfig, name string) (uint64, bool) {
	for idx, device := range registry.Slice(dp.DpDevices.Get(), dp.DeviceCount) {
		if shm.Equal(device.Name[:], name) {
			return uint64(idx), true
		}
	}
	return 0, false
}
