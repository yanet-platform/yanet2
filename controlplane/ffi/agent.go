package ffi

//#cgo CFLAGS: -I../../ -I../../lib
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#cgo LDFLAGS: -L../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../build/lib/counters/ -lcounters
//#cgo LDFLAGS: -L../../build/lib/dataplane/config -lconfig_dp
//
//#define _GNU_SOURCE
//#include "api/agent.h"
//#include "controlplane/agent/agent.h"
import "C"
import (
	"fmt"
	"unsafe"
)

type ModuleConfig struct {
	ptr *C.struct_cp_module
}

func NewModuleConfig(ptr unsafe.Pointer) ModuleConfig {
	return ModuleConfig{
		ptr: (*C.struct_cp_module)(ptr),
	}
}

type ShmDeviceConfig struct {
	ptr *C.struct_cp_device
}

func NewShmDeviceConfig(ptr unsafe.Pointer) ShmDeviceConfig {
	return ShmDeviceConfig{
		ptr: (*C.struct_cp_device)(ptr),
	}
}

func (m *ShmDeviceConfig) AsRawPtr() unsafe.Pointer {
	return unsafe.Pointer(m.ptr)
}

func (m *ModuleConfig) AsRawPtr() unsafe.Pointer {
	return unsafe.Pointer(m.ptr)
}

type Agent struct {
	ptr *C.struct_agent
}

func (m *Agent) Close() error {
	_, err := C.agent_detach(m.ptr)
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

	rc, err := C.agent_update_modules(
		(*C.struct_agent)(m.AsRawPtr()),
		C.size_t(len(modules)),
		&configs[0],
	)
	if err != nil {
		return fmt.Errorf("failed to update modules: %w", err)
	}
	if rc != 0 {
		return fmt.Errorf("failed to update modules: %d code", rc)
	}

	return nil
}

func (m *Agent) DPConfig() *DPConfig {
	return &DPConfig{
		ptr: m.ptr.dp_config,
	}
}

func (m *Agent) UpdateFunctions(functionConfigs []FunctionConfig) error {
	functions := make([]*C.struct_cp_function_config, 0, len(functionConfigs))

	for _, cfg := range functionConfigs {
		function, err := newFunctionConfig(cfg)
		if err != nil {
			return fmt.Errorf("failed to create pipeline config: %w", err)
		}
		defer function.Free()

		functions = append(functions, function.AsRawPtr())
	}

	rc, err := C.agent_update_functions(
		m.ptr,
		C.uint64_t(len(functionConfigs)),
		&functions[0],
	)
	if err != nil {
		return fmt.Errorf("failed to update pipelines: %w", err)
	}
	if rc != 0 {
		return fmt.Errorf("failed to update pipelines: %d code", rc)
	}

	return nil
}

func (m *Agent) UpdatePipelines(pipelinesConfigs []PipelineConfig) error {
	pipelines := make([]*C.struct_cp_pipeline_config, 0, len(pipelinesConfigs))

	for _, cfg := range pipelinesConfigs {
		pipeline, err := newPipelineConfig(cfg)
		if err != nil {
			return fmt.Errorf("failed to create pipeline config: %w", err)
		}
		defer pipeline.Free()

		pipelines = append(pipelines, pipeline.AsRawPtr())
	}

	rc, err := C.agent_update_pipelines(
		m.ptr,
		C.uint64_t(len(pipelinesConfigs)),
		&pipelines[0],
	)
	if err != nil {
		return fmt.Errorf("failed to update pipelines: %w", err)
	}
	if rc != 0 {
		return fmt.Errorf("failed to update pipelines: %d code", rc)
	}

	return nil
}

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

	rc, err := C.agent_update_devices(
		(*C.struct_agent)(m.AsRawPtr()),
		C.size_t(len(devices)),
		&configs[0],
	)
	if err != nil {
		return fmt.Errorf("failed to update devices: %w", err)
	}
	if rc != 0 {
		return fmt.Errorf("failed to update devices: %d code", rc)
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

	rc, err := C.agent_delete_function(m.ptr, cName)
	if err != nil {
		return err
	}
	if rc != 0 {
		return fmt.Errorf("error code: %d", rc)
	}

	return nil
}

func (m *Agent) DeletePipeline(name string) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	rc, err := C.agent_delete_pipeline(m.ptr, cName)
	if err != nil {
		return err
	}
	if rc != 0 {
		return fmt.Errorf("error code: %d", rc)
	}

	return nil
}
