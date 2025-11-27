package ffi

//#cgo CFLAGS: -I../../
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#cgo LDFLAGS: -L../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../build/lib/counters -lcounters
//#cgo LDFLAGS: -L../../build/lib/dataplane/config -lconfig_dp
//#include "api/agent.h"
import "C"
import (
	"fmt"
	"unsafe"
)

// SharedMemory represents a handle to YANET shared memory segment.
type SharedMemory struct {
	ptr *C.struct_yanet_shm
}

func NewSharedMemoryFromRaw(ptr unsafe.Pointer) *SharedMemory {
	return &SharedMemory{ptr: (*C.struct_yanet_shm)(ptr)}
}

// AttachSharedMemory attaches to YANET shared memory segment.
func AttachSharedMemory(path string) (*SharedMemory, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	ptr, err := C.yanet_shm_attach(cPath)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to shared memory %q: %w", path, err)
	}

	return &SharedMemory{ptr: ptr}, nil
}

// Detach detaches from YANET shared memory segment.
func (m *SharedMemory) Detach() error {
	if m.ptr != nil {
		if _, err := C.yanet_shm_detach(m.ptr); err != nil {
			return err
		}

		m.ptr = nil
	}

	return nil
}

// DPConfig gets configuration of the dataplane instance from shared memory.
func (m *SharedMemory) DPConfig(instanceIdx uint32) *DPConfig {
	ptr := C.yanet_shm_dp_config(m.ptr, C.uint32_t(instanceIdx))

	return &DPConfig{ptr: ptr}
}

// AgentAttach attaches a module agent to shared memory on the dataplane instance.
func (m *SharedMemory) AgentAttach(name string, instanceIdx uint32, size uint) (*Agent, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr := C.agent_attach(m.ptr, C.uint32_t(instanceIdx), cName, C.size_t(size))
	if ptr == nil {
		return nil, fmt.Errorf("failed to attach agent: %s", name)
	}

	return &Agent{ptr: ptr}, nil
}

// AgentsAttach attaches agents to shared memory on the specified list of instances.
func (m *SharedMemory) AgentsAttach(name string, instanceIndices []uint32, size uint) ([]*Agent, error) {
	agents := make([]*Agent, 0, len(instanceIndices))
	for _, instanceIdx := range instanceIndices {
		agent, err := m.AgentAttach(name, instanceIdx, uint(size))
		if err != nil {
			return nil, fmt.Errorf("failed to connect to shared memory on instance %d: %w", instanceIdx, err)
		}

		agents = append(agents, agent)
	}
	return agents, nil
}

// InstanceIndices returns a list of indices of available dataplane instances.
func (m *SharedMemory) InstanceIndices() []uint32 {
	instanceCount := uint32(C.yanet_shm_instance_count(m.ptr))
	instances := make([]uint32, instanceCount)
	for i := uint32(0); i < instanceCount; i++ {
		instances[i] = i
	}
	return instances
}

// DPConfig represents a handle to dataplane configuration.
type DPConfig struct {
	ptr *C.struct_dp_config
}

func (m *DPConfig) NumaIdx() uint32 {
	return uint32(C.dataplane_instance_numa_idx(m.ptr))
}

// Modules returns a list of dataplane modules available.
func (m *DPConfig) Modules() []DPModule {
	ptr := C.yanet_get_dp_module_list_info(m.ptr)
	defer C.dp_module_list_info_free(ptr)

	out := make([]DPModule, ptr.module_count)
	for idx := C.uint64_t(0); idx < ptr.module_count; idx++ {
		mod := C.struct_dp_module_info{}

		rc := C.yanet_get_dp_module_info(ptr, idx, &mod)
		if rc != 0 {
			panic("FFI corruption: module index became invalid")
		}

		out[idx] = DPModule{
			name: C.GoString(&mod.name[0]),
		}
	}

	return out
}

func (m *DPConfig) CPConfigs() []CPConfig {
	cpModulesListInfo := C.yanet_get_cp_module_list_info(m.ptr)
	defer C.cp_module_list_info_free(cpModulesListInfo)

	out := make([]CPConfig, 0, cpModulesListInfo.module_count)

	for idx := C.uint64_t(0); idx < cpModulesListInfo.module_count; idx++ {
		cpModuleInfo := C.struct_cp_module_info{}

		// SAFETY: safe, because we query using index within valid range.
		rc := C.yanet_get_cp_module_info(cpModulesListInfo, idx, &cpModuleInfo)
		if rc != 0 {
			panic("FFI corruption: module index became invalid")
		}

		configName := C.GoString(&cpModuleInfo.config_name[0])

		out = append(out, CPConfig{
			ModuleIndex: uint32(cpModuleInfo.index),
			ConfigName:  configName,
			Gen:         uint64(cpModuleInfo.gen),
		})
	}

	return out
}

// Pipelines returns all pipeline configurations from the dataplane.
func (m *DPConfig) Pipelines() []Pipeline {
	pipelineListInfo := C.yanet_get_cp_pipeline_list_info(m.ptr)
	defer C.cp_pipeline_list_info_free(pipelineListInfo)

	out := make([]Pipeline, pipelineListInfo.count)
	for idx := C.uint64_t(0); idx < pipelineListInfo.count; idx++ {
		var pipelineInfo *C.struct_cp_pipeline_info
		rc := C.yanet_get_cp_pipeline_info(pipelineListInfo, idx, &pipelineInfo)
		if rc != 0 {
			panic("FFI corruption: pipeline index became invalid")
		}

		moduleConfigs := make([]uint64, pipelineInfo.length)
		for moduleIdx := C.uint64_t(0); moduleIdx < pipelineInfo.length; moduleIdx++ {
			var configIndex C.uint64_t
			rc := C.yanet_get_cp_pipeline_module_info(pipelineInfo, moduleIdx, &configIndex)
			if rc != 0 {
				panic("FFI corruption: pipeline module index became invalid")
			}
			moduleConfigs[moduleIdx] = uint64(configIndex)
		}

		out[idx] = Pipeline{
			Name:          C.GoString(&pipelineInfo.name[0]),
			ModuleConfigs: moduleConfigs,
		}
	}

	return out
}

// Agents returns all agent information from the dataplane.
func (m *DPConfig) Agents() []AgentInfo {
	agentListInfo := C.yanet_get_cp_agent_list_info(m.ptr)
	defer C.cp_agent_list_info_free(agentListInfo)

	out := make([]AgentInfo, agentListInfo.count)
	for idx := C.uint64_t(0); idx < agentListInfo.count; idx++ {
		var agentInfo *C.struct_cp_agent_info
		rc := C.yanet_get_cp_agent_info(agentListInfo, idx, &agentInfo)
		if rc != 0 {
			panic("FFI corruption: agent index became invalid")
		}

		instances := make([]AgentInstanceInfo, agentInfo.instance_count)
		for instIdx := C.uint64_t(0); instIdx < agentInfo.instance_count; instIdx++ {
			var instanceInfo *C.struct_cp_agent_instance_info
			rc := C.yanet_get_cp_agent_instance_info(agentInfo, instIdx, &instanceInfo)
			if rc != 0 {
				panic("FFI corruption: agent instance index became invalid")
			}

			instances[instIdx] = AgentInstanceInfo{
				PID:         uint32(instanceInfo.pid),
				MemoryLimit: uint64(instanceInfo.memory_limit),
				Allocated:   uint64(instanceInfo.allocated),
				Freed:       uint64(instanceInfo.freed),
				Gen:         uint64(instanceInfo.gen),
			}
		}

		out[idx] = AgentInfo{
			Name:      C.GoString(&agentInfo.name[0]),
			Instances: instances,
		}
	}

	return out
}

// DPModule represents a dataplane module in the YANET configuration.
type DPModule struct {
	name string
}

// CPConfig represents a control plane configuration associated with a module.
type CPConfig struct {
	ModuleIndex uint32
	ConfigName  string
	Gen         uint64
}

// Pipeline represents a dataplane packet processing pipeline configuration.
type Pipeline struct {
	// Name is the name of the pipeline.
	Name string
	// ModuleConfigs is a list of module configurations indices.
	//
	// The index is the index of the module configuration in the dataplane
	// configuration.
	ModuleConfigs []uint64
}

// AgentInfo represents information about a control plane agent.
type AgentInfo struct {
	Name      string
	Instances []AgentInstanceInfo
}

// AgentInstanceInfo contains details about a specific agent instance.
type AgentInstanceInfo struct {
	PID         uint32
	MemoryLimit uint64
	Allocated   uint64
	Freed       uint64
	Gen         uint64
}

// Name returns the name of the dataplane module.
//
// The module name uniquely identifies the module in the dataplane.
func (m *DPModule) Name() string {
	return m.name
}

// String implements the Stringer interface for DPModule.
//
// Used in "zap".
func (m DPModule) String() string {
	return m.Name()
}

// DevicePipelineInfo represents information about a pipeline in a device.
type DevicePipelineInfo struct {
	PipelineIndex uint32
	Weight        uint64
}

// DeviceInfo represents information about a device.
type DeviceInfo struct {
	DeviceID  uint16
	Name      string
	Pipelines []DevicePipelineInfo
}

// Devices returns all device information from the dataplane.
func (m *DPConfig) Devices() []DeviceInfo {
	deviceListInfo := C.yanet_get_cp_device_list_info(m.ptr)
	if deviceListInfo == nil {
		return nil
	}
	defer C.cp_device_list_info_free(deviceListInfo)

	out := make([]DeviceInfo, deviceListInfo.device_count)
	for idx := C.uint64_t(0); idx < deviceListInfo.device_count; idx++ {
		deviceInfo := C.yanet_get_cp_device_info(deviceListInfo, idx)
		if deviceInfo == nil {
			continue
		}

		pipelines := make([]DevicePipelineInfo, deviceInfo.pipeline_count)
		for pipelineIdx := C.uint64_t(0); pipelineIdx < deviceInfo.pipeline_count; pipelineIdx++ {
			pipelineInfo := C.yanet_get_cp_device_pipeline_info(deviceInfo, pipelineIdx)
			if pipelineInfo == nil {
				continue
			}

			pipelines[pipelineIdx] = DevicePipelineInfo{
				PipelineIndex: uint32(pipelineInfo.pipeline_idx),
				Weight:        uint64(pipelineInfo.weight),
			}
		}

		out[idx] = DeviceInfo{
			DeviceID:  uint16(idx),
			Name:      C.GoString(&deviceInfo.name[0]),
			Pipelines: pipelines,
		}
	}

	return out
}

type CounterInfo struct {
	Name   string
	Values [][]uint64
}

func (M *DPConfig) encodeCounters(counters *C.struct_counter_handle_list) []CounterInfo {
	res := make([]CounterInfo, 0)

	for cidx := C.uint64_t(0); cidx < counters.count; cidx++ {
		handle := C.yanet_get_counter(counters, cidx)
		counterInfo := CounterInfo{
			Name:   C.GoString(&handle.name[0]),
			Values: make([][]uint64, 0, counters.instance_count),
		}

		for iidx := C.uint64_t(0); iidx < counters.instance_count; iidx++ {
			counterInfo.Values = append(
				counterInfo.Values,
				make([]uint64, 0, int(handle.size)),
			)
			for vidx := C.uint64_t(0); vidx < handle.size; vidx++ {
				counterInfo.Values[iidx] = append(
					counterInfo.Values[iidx],
					uint64(C.yanet_get_counter_value(
						handle.value_handle,
						vidx,
						iidx,
					)),
				)
			}
		}

		res = append(res, counterInfo)
	}

	return res
}

// PipelineCounters returns pipeline counters
func (m *DPConfig) PipelineCounters(
	device_name string,
	pipeline_name string,
) []CounterInfo {
	c_device_name := C.CString(device_name)
	defer C.free(unsafe.Pointer(c_device_name))
	c_pipeline_name := C.CString(pipeline_name)
	defer C.free(unsafe.Pointer(c_pipeline_name))
	counters := C.yanet_get_pipeline_counters(m.ptr, c_device_name, c_pipeline_name)
	defer C.yanet_counter_handle_list_free(counters)

	if counters == nil {
		return nil
	}

	return m.encodeCounters(counters)
}

// PipelineModuleCounters returns pipeline module counters
func (m *DPConfig) ModuleCounters(
	device_name string,
	pipeline_name string,
	function_name string,
	chain_name string,
	module_type string,
	module_name string,
) []CounterInfo {
	c_device_name := C.CString(device_name)
	defer C.free(unsafe.Pointer(c_device_name))
	c_pipeline_name := C.CString(pipeline_name)
	defer C.free(unsafe.Pointer(c_pipeline_name))
	c_function_name := C.CString(function_name)
	defer C.free(unsafe.Pointer(c_function_name))
	c_chain_name := C.CString(chain_name)
	defer C.free(unsafe.Pointer(c_chain_name))
	c_module_type := C.CString(module_type)
	defer C.free(unsafe.Pointer(c_module_type))
	c_module_name := C.CString(module_name)
	defer C.free(unsafe.Pointer(c_module_name))
	counters := C.yanet_get_module_counters(
		m.ptr,
		c_device_name,
		c_pipeline_name,
		c_function_name,
		c_chain_name,
		c_module_type,
		c_module_name,
	)
	defer C.yanet_counter_handle_list_free(counters)

	if counters == nil {
		return nil
	}

	return m.encodeCounters(counters)
}
