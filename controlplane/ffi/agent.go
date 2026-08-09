package ffi

//#cgo CFLAGS: -I../../ -I../../lib
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#cgo LDFLAGS: -L../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../build/lib/counters/ -lcounters
//#cgo LDFLAGS: -L../../build/lib/dataplane/config -lconfig_dp
//#cgo LDFLAGS: -L../../build/devices/plain/api -ldev_plain_api
//#cgo LDFLAGS: -L../../build/devices/vlan/api -ldev_vlan_api
//#cgo LDFLAGS: -L../../build/lib/logging/ -llogging
//
//#include "api/agent.h"
//#include "api/config.h"
//#include "devices/plain/api/controlplane.h"
//#include "devices/vlan/api/controlplane.h"
//
//#define _GNU_SOURCE
//#include "api/agent.h"
//#include "controlplane/agent/agent.h"
import "C"
import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
)

// ModuleConfig is a Go wrapper around a C cp_module pointer, representing a
// module's shared memory configuration.
//
// Each module package creates its own typed wrapper embedding ModuleConfig,
// using unsafe.Pointer as a bridge between CGo contexts of different packages.
type ModuleConfig struct {
	ptr *C.struct_cp_module
}

// NewModuleConfig wraps a raw C pointer into a ModuleConfig.
//
// The pointer must originate from a module-specific C constructor that returns
// a valid cp_module pointer.
// The caller is responsible for ensuring the pointer's validity and lifetime.
func NewModuleConfig(ptr unsafe.Pointer) ModuleConfig {
	return ModuleConfig{
		ptr: (*C.struct_cp_module)(ptr),
	}
}

// AsRawPtr returns the underlying C pointer as unsafe.Pointer for passing
// across CGo package boundaries.
func (m ModuleConfig) AsRawPtr() unsafe.Pointer {
	return unsafe.Pointer(m.ptr)
}

// ObjectConfig is a Go wrapper around a C cp_object pointer, representing a
// named shared-memory object accounted in a control-plane configuration
// generation but carrying no dataplane handler of its own.
//
// Like ModuleConfig it is an opaque bridge between CGo contexts; each object
// package creates its own typed wrapper that returns an ObjectConfig via
// AsFFIObject for agent.UpdateObjects.
type ObjectConfig struct {
	ptr *C.struct_cp_object
}

// NewObjectConfig wraps a raw C pointer into an ObjectConfig.
//
// The pointer must originate from an object-specific C constructor that
// returns a valid cp_object pointer. The caller is responsible for ensuring
// the pointer's validity and lifetime.
func NewObjectConfig(ptr unsafe.Pointer) ObjectConfig {
	return ObjectConfig{
		ptr: (*C.struct_cp_object)(ptr),
	}
}

// AsRawPtr returns the underlying C pointer as unsafe.Pointer for passing
// across CGo package boundaries.
func (m ObjectConfig) AsRawPtr() unsafe.Pointer {
	return unsafe.Pointer(m.ptr)
}

// ShmDeviceConfig is a Go wrapper around a C cp_device pointer, representing
// a device's shared memory configuration.
//
// Device packages (plain, vlan) create their own typed wrappers embedding
// ShmDeviceConfig.
type ShmDeviceConfig struct {
	ptr *C.struct_cp_device
}

// NewShmDeviceConfig wraps a raw C pointer into a ShmDeviceConfig.
//
// The pointer must originate from a device-specific C constructor (e.g.
// cp_device_plain_new).
// The caller is responsible for ensuring the pointer's validity and lifetime.
func NewShmDeviceConfig(ptr unsafe.Pointer) ShmDeviceConfig {
	return ShmDeviceConfig{
		ptr: (*C.struct_cp_device)(ptr),
	}
}

// AsRawPtr returns the underlying C pointer as unsafe.Pointer for passing
// across CGo package boundaries.
func (m ShmDeviceConfig) AsRawPtr() unsafe.Pointer {
	return unsafe.Pointer(m.ptr)
}

type Agent struct {
	name string
	ptr  *C.struct_agent
}

// BlockAllocatorFreeSize returns the total free memory in the agent's block
// allocator.
func (m *Agent) BlockAllocatorFreeSize() uint64 {
	return uint64(C.block_allocator_free_size(&m.ptr.block_allocator))
}

func (m *Agent) Close() error {
	_, err := C.agent_detach(m.ptr)
	return err
}

func (m *Agent) CleanUp() error {
	_, err := C.agent_cleanup(m.ptr)
	return err
}

func (m *Agent) AsRawPtr() unsafe.Pointer {
	return unsafe.Pointer(m.ptr)
}

func (m *Agent) UpdateModules(modules []ModuleConfig) error {
	if len(modules) == 0 {
		return fmt.Errorf("no modules provided")
	}

	configs := make([]*C.struct_cp_module, len(modules))
	for i, module := range modules {
		if module.ptr == nil {
			return fmt.Errorf("module config at index %d is nil", i)
		}
		configs[i] = (*C.struct_cp_module)(module.AsRawPtr())
	}

	if len(configs) == 0 {
		return fmt.Errorf("no module configs to update")
	}

	var cErr *C.yanet_error
	rc := C.agent_update_modules(
		(*C.struct_agent)(m.AsRawPtr()),
		C.size_t(len(modules)),
		&configs[0],
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf("failed to update modules: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return nil
}

// UpdateObjects atomically upserts the given shared-memory objects into a new
// control-plane configuration generation and blocks until every dataplane
// worker has advanced to it.
//
// Mirrors UpdateModules for cp_object-registered entities (e.g. standalone
// fwstate-map tables) that carry no dataplane handler of their own.
func (m *Agent) UpdateObjects(objects []ObjectConfig) error {
	if len(objects) == 0 {
		return fmt.Errorf("no objects provided")
	}

	configs := make([]*C.struct_cp_object, len(objects))
	for idx, object := range objects {
		if object.ptr == nil {
			return fmt.Errorf("object config at index %d is nil", idx)
		}
		configs[idx] = (*C.struct_cp_object)(object.AsRawPtr())
	}

	var cErr *C.yanet_error
	rc := C.agent_update_objects(
		(*C.struct_agent)(m.AsRawPtr()),
		C.size_t(len(objects)),
		&configs[0],
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf("failed to update objects: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return nil
}

func (m *Agent) DPConfig() *DPConfig {
	return &DPConfig{
		ptr: C.agent_dp_config(m.ptr),
	}
}

func (m *Agent) UpdateFunction(functionConfig FunctionConfig) error {
	function, err := newFunctionConfig(functionConfig)
	if err != nil {
		return fmt.Errorf("failed to create pipeline config: %w", err)
	}
	defer function.Free()

	functions := make([]*C.struct_cp_function_config, 1)
	functions[0] = function.AsRawPtr()

	var cErr *C.yanet_error
	rc := C.agent_update_functions(
		m.ptr,
		1,
		&functions[0],
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf("failed to update functions: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return nil
}

func (m *Agent) UpdatePipeline(pipelineConfig PipelineConfig) error {
	pipelines := make([]*C.struct_cp_pipeline_config, 0, 1)

	pipeline, err := newPipelineConfig(pipelineConfig)
	if err != nil {
		return fmt.Errorf("failed to create pipeline config: %w", err)
	}
	defer pipeline.Free()

	pipelines = append(pipelines, pipeline.AsRawPtr())

	var cErr *C.yanet_error
	rc := C.agent_update_pipelines(
		m.ptr,
		1,
		&pipelines[0],
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf("failed to update pipelines: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return nil
}

func (m *Agent) UpdatePlainDevices(devices []DeviceConfig) error {
	configs := make([]ShmDeviceConfig, 0, len(devices))

	for idx := range devices {
		device := &devices[idx]

		name := device.Name
		input := device.Input
		output := device.Output

		cName := C.CString(name)
		defer C.free(unsafe.Pointer(cName))

		var cErr *C.struct_yanet_error
		cCfg := C.cp_device_plain_config_new(
			cName,
			C.uint64_t(len(input)),
			C.uint64_t(len(output)),
			&cErr,
		)
		if cerr := cerrors.FromC(unsafe.Pointer(cErr)); cerr != nil {
			return fmt.Errorf("failed to initialize plain device config: %w", cerr)
		}
		defer C.cp_device_plain_config_free(cCfg)

		for idx := range input {
			pipeline := &input[idx]
			cName := C.CString(pipeline.Name)
			defer C.free(unsafe.Pointer(cName))
			C.cp_device_plain_config_set_input_pipeline(
				cCfg,
				C.uint64_t(idx),
				cName,
				C.uint64_t(pipeline.Weight),
			)
		}

		for idx := range output {
			pipeline := &output[idx]
			cName := C.CString(pipeline.Name)
			defer C.free(unsafe.Pointer(cName))
			C.cp_device_plain_config_set_output_pipeline(
				cCfg,
				C.uint64_t(idx),
				cName,
				C.uint64_t(pipeline.Weight),
			)
		}

		ptr := C.cp_device_plain_new(
			(*C.struct_agent)(m.AsRawPtr()),
			cCfg,
			&cErr,
		)
		if ptr == nil {
			return fmt.Errorf(
				"failed to create plain device: %w",
				cerrors.FromC(unsafe.Pointer(cErr)),
			)
		}

		configs = append(configs, NewShmDeviceConfig(unsafe.Pointer(ptr)))
	}

	return m.UpdateDevices(configs)
}

// TODO: (*Agent).UpdateVlanDevices

// UpdateDevices attaches the given pipelines to the given device IDs.
func (m *Agent) UpdateDevices(devices []ShmDeviceConfig) error {
	if len(devices) == 0 {
		return nil
	}

	configs := make([]*C.struct_cp_device, len(devices))
	for i, device := range devices {
		if device.ptr == nil {
			return fmt.Errorf("device config at index %d is nil", i)
		}
		configs[i] = (*C.struct_cp_device)(device.AsRawPtr())
	}

	var cErr *C.yanet_error
	rc := C.agent_update_devices(
		(*C.struct_agent)(m.AsRawPtr()),
		C.size_t(len(devices)),
		&configs[0],
		&cErr,
	)
	if rc != 0 {
		return cerrors.FromC(unsafe.Pointer(cErr))
	}

	return nil
}

type DevicePipeline struct {
	Name   string
	Weight uint64
}

func (m *Agent) DeleteFunction(name string) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	rc := C.agent_delete_function(m.ptr, cName, &cErr)
	if rc != 0 {
		return fmt.Errorf("failed to delete function %q: %w", name, cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return nil
}

func (m *Agent) DeletePipeline(name string) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	rc := C.agent_delete_pipeline(m.ptr, cName, &cErr)
	if rc != 0 {
		return fmt.Errorf("failed to delete pipeline %q: %w", name, cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return nil
}

// DeleteModule removes a module config from the dataplane by type name and
// config name. This is the general form of DeleteModuleConfig that supports
// type strings different from the agent's own name (e.g. "fwstate-map").
func (m *Agent) DeleteModule(typeName string, configName string) error {
	cTypeName := C.CString(typeName)
	defer C.free(unsafe.Pointer(cTypeName))

	cConfigName := C.CString(configName)
	defer C.free(unsafe.Pointer(cConfigName))

	var cErr *C.yanet_error
	result := C.agent_delete_module(
		(*C.struct_agent)(m.AsRawPtr()),
		cTypeName,
		cConfigName,
		&cErr,
	)
	if result != 0 {
		return fmt.Errorf(
			"failed to delete module config type %q name %q: %w",
			typeName,
			configName,
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	return nil
}

func (m *Agent) DeleteModuleConfig(configName string) error {
	return m.DeleteModule(m.name, configName)
}

// DeleteObject removes a named shared-memory object from the dataplane by
// type and name (e.g. ("fwstate_map_v4", "default")).
func (m *Agent) DeleteObject(objectType, objectName string) error {
	cObjectType := C.CString(objectType)
	defer C.free(unsafe.Pointer(cObjectType))

	cObjectName := C.CString(objectName)
	defer C.free(unsafe.Pointer(cObjectName))

	var cErr *C.yanet_error
	result := C.agent_delete_object(
		(*C.struct_agent)(m.AsRawPtr()),
		cObjectType,
		cObjectName,
		&cErr,
	)
	if result != 0 {
		return fmt.Errorf(
			"failed to delete object type %q name %q: %w",
			objectType,
			objectName,
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	return nil
}
