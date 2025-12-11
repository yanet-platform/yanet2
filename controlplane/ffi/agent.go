package ffi

//#cgo CFLAGS: -I../../ -I../../lib
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#cgo LDFLAGS: -L../../build/lib/controlplane/diag -ldiag
//#cgo LDFLAGS: -L../../build/common/tls_stack -ltls_stack
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

func NewAgent(ptr unsafe.Pointer) Agent {
	return Agent{ptr: (*C.struct_agent)(ptr)}
}

func (m *Agent) Close() error {
	_, err := C.agent_detach(m.ptr)
	return err
}

func (m *Agent) CleanUp() error {
	_, err := C.agent_cleanup(m.ptr)
	return err
}

// TakeError retrieves and clears the last error from the agent's diagnostic system.
// It takes ownership of the error message from the C layer and returns it as a Go error.
//
// Returns:
//   - nil if there is no error
//   - An error containing the diagnostic message if an error occurred
//   - An ENOMEM error if memory allocation failed while capturing the error
func (m *Agent) TakeError() error {
	cMsg, err := C.agent_take_error(m.ptr)
	if cMsg == nil && err == nil {
		return nil
	}
	if err != nil {
		// then, it is enomem
		return err
	}

	// Copy the C string to Go string before freeing
	goMsg := C.GoString(cMsg)

	// Free the C string - agent_take_error transfers ownership to the caller
	C.free(unsafe.Pointer(cMsg))

	return fmt.Errorf("%s", goMsg)
}

// CleanError clears any error stored in the agent's diagnostic system without
// retrieving it. This is useful when you want to discard an error without
// processing it.
//
// Unlike TakeError, this method does not return the error message and simply
// resets the diagnostic state.
func (m *Agent) CleanError() {
	C.agent_clean_error(m.ptr)
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
	if rc != 0 {
		return m.TakeError()
	}
	if err != nil {
		return fmt.Errorf("failed to update modules: %w", err)
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

	rc, err := C.agent_update_functions(
		m.ptr,
		1,
		&functions[0],
	)
	if rc != 0 {
		return m.TakeError()
	}
	if err != nil {
		return fmt.Errorf("failed to update function: %w", err)
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

	rc, err := C.agent_update_pipelines(
		m.ptr,
		1,
		&pipelines[0],
	)
	if rc != 0 {
		return m.TakeError()
	}
	if err != nil {
		return fmt.Errorf("failed to update pipelines: %w", err)
	}

	return nil
}

func (agent *Agent) UpdatePlainDevices(devices []DeviceConfig) error {
	configs := make([]ShmDeviceConfig, 0, len(devices))

	for idx := range devices {
		device := &devices[idx]

		name := device.Name
		input := device.Input
		output := device.Output

		cName := C.CString(name)
		defer C.free(unsafe.Pointer(cName))

		cCfg := C.cp_device_plain_config_create(cName, C.uint64_t(len(input)), C.uint64_t(len(output)))

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

		ptr, err := C.cp_device_plain_create((*C.struct_agent)(agent.AsRawPtr()), cCfg)
		if err != nil {
			return fmt.Errorf("failed to initialize plain device config: %w", err)
		}
		if ptr == nil {
			return fmt.Errorf("failed to initialize plain device config: device %q not found", name)
		}

		configs = append(configs, NewShmDeviceConfig(unsafe.Pointer(ptr)))
	}

	return agent.UpdateDevices(configs)
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

	rc, err := C.agent_update_devices(
		(*C.struct_agent)(m.AsRawPtr()),
		C.size_t(len(devices)),
		&configs[0],
	)
	if rc != 0 {
		return m.TakeError()
	}
	if err != nil {
		return fmt.Errorf("failed to update devices: %w", err)
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
	if rc != 0 {
		return m.TakeError()
	}
	if err != nil {
		return err
	}

	return nil
}

func (m *Agent) DeletePipeline(name string) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	rc, err := C.agent_delete_pipeline(m.ptr, cName)
	if rc != 0 {
		return m.TakeError()
	}
	if err != nil {
		return err
	}

	return nil
}
