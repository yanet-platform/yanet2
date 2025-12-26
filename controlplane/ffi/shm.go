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

	"github.com/c2h5oh/datasize"
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
func (m *SharedMemory) AgentAttach(name string, instanceIdx uint32, size datasize.ByteSize) (*Agent, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr := C.agent_attach(m.ptr, C.uint32_t(instanceIdx), cName, C.size_t(size))
	if ptr == nil {
		return nil, fmt.Errorf("failed to attach agent: %s", name)
	}

	return &Agent{name: name, ptr: ptr}, nil
}

// AgentsAttach attaches agents to shared memory on the specified list of instances.
func (m *SharedMemory) AgentsAttach(name string, instanceIndices []uint32, size datasize.ByteSize) ([]*Agent, error) {
	agents := make([]*Agent, 0, len(instanceIndices))
	for _, instanceIdx := range instanceIndices {
		agent, err := m.AgentAttach(name, instanceIdx, size)
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
	for i := range instanceCount {
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

func (m *DPConfig) WorkerCount() uint32 {
	return uint32(C.dataplane_instance_worker_count(m.ptr))
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
		moduleInfo := C.yanet_get_cp_module_info(cpModulesListInfo, idx)

		out = append(out, CPConfig{
			Type: C.GoString(&moduleInfo._type[0]),
			Name: C.GoString(&moduleInfo.name[0]),
			Gen:  uint64(moduleInfo.gen),
		})
	}

	return out
}

type ChainModule struct {
	Type string
	Name string
}

type Chain struct {
	Name    string
	Weight  uint64
	Modules []ChainModule
}

type Function struct {
	Name   string
	Chains []Chain
}

// Functions returns all functions configurations from the dataplane.
func (m *DPConfig) Functions() []Function {
	functionListInfo := C.yanet_get_cp_function_list_info(m.ptr)
	defer C.cp_function_list_info_free(functionListInfo)

	out := make([]Function, functionListInfo.function_count)
	for idx := C.uint64_t(0); idx < functionListInfo.function_count; idx++ {
		functionInfo := C.yanet_get_cp_function_info(functionListInfo, idx)

		chains := make([]Chain, functionInfo.chain_count)
		for idx := range functionInfo.chain_count {
			chainInfo := C.yanet_get_cp_function_chain_info(functionInfo, idx)

			modules := make([]ChainModule, chainInfo.length)
			for idx := range chainInfo.length {
				modInfo := C.yanet_get_cp_function_chain_module_info(chainInfo, idx)
				modules[idx] = ChainModule{
					Type: C.GoString(&modInfo._type[0]),
					Name: C.GoString(&modInfo.name[0]),
				}
			}

			chains[idx] = Chain{
				Name:    C.GoString(&chainInfo.name[0]),
				Weight:  uint64(chainInfo.weight),
				Modules: modules,
			}
		}

		out[idx] = Function{
			Name:   C.GoString(&functionInfo.name[0]),
			Chains: chains,
		}
	}

	return out
}

// Pipelines returns all pipeline configurations from the dataplane.
func (m *DPConfig) Pipelines() []Pipeline {
	pipelineListInfo := C.yanet_get_cp_pipeline_list_info(m.ptr)
	defer C.cp_pipeline_list_info_free(pipelineListInfo)

	out := make([]Pipeline, pipelineListInfo.count)
	for idx := C.uint64_t(0); idx < pipelineListInfo.count; idx++ {
		pipelineInfo := C.yanet_get_cp_pipeline_info(pipelineListInfo, idx)

		functions := make([]string, pipelineInfo.length)
		for idx := range pipelineInfo.length {
			function := C.yanet_get_cp_pipeline_function_info_id(pipelineInfo, idx)
			functions[idx] = C.GoString(&function.name[0])
		}

		out[idx] = Pipeline{
			Name:      C.GoString(&pipelineInfo.name[0]),
			Functions: functions,
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
	Type string
	Name string
	Gen  uint64
}

// Pipeline represents a dataplane packet processing pipeline configuration.
type Pipeline struct {
	// Name is the name of the pipeline.
	Name string
	// Functions is the list of functions in the pipeline
	Functions []string
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
	Name   string
	Weight uint64
}

// DeviceInfo represents information about a device.
type DeviceInfo struct {
	Type            string
	Name            string
	InputPipelines  []DevicePipelineInfo
	OutputPipelines []DevicePipelineInfo
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

		inputPipelines := make([]DevicePipelineInfo, deviceInfo.input_count)
		for pipelineIdx := C.uint64_t(0); pipelineIdx < deviceInfo.input_count; pipelineIdx++ {
			pipelineInfo := C.yanet_get_cp_device_input_pipeline_info(deviceInfo, pipelineIdx)
			if pipelineInfo == nil {
				continue
			}

			inputPipelines[pipelineIdx] = DevicePipelineInfo{
				Name:   C.GoString(&pipelineInfo.name[0]),
				Weight: uint64(pipelineInfo.weight),
			}
		}

		outputPipelines := make([]DevicePipelineInfo, deviceInfo.output_count)
		for pipelineIdx := C.uint64_t(0); pipelineIdx < deviceInfo.output_count; pipelineIdx++ {
			pipelineInfo := C.yanet_get_cp_device_output_pipeline_info(deviceInfo, pipelineIdx)
			if pipelineInfo == nil {
				continue
			}

			outputPipelines[pipelineIdx] = DevicePipelineInfo{
				Name:   C.GoString(&pipelineInfo.name[0]),
				Weight: uint64(pipelineInfo.weight),
			}
		}

		out[idx] = DeviceInfo{
			Type:            C.GoString(&deviceInfo._type[0]),
			Name:            C.GoString(&deviceInfo.name[0]),
			InputPipelines:  inputPipelines,
			OutputPipelines: outputPipelines,
		}
	}

	return out
}

type CounterInfo struct {
	Name   string
	Values [][]uint64
}

func (m *DPConfig) encodeCounters(counters *C.struct_counter_handle_list) []CounterInfo {
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

func (m *DPConfig) DeviceCounters(
	deviceName string,
) []CounterInfo {
	cDeviceName := C.CString(deviceName)
	defer C.free(unsafe.Pointer(cDeviceName))
	counters := C.yanet_get_device_counters(m.ptr, cDeviceName)
	defer C.yanet_counter_handle_list_free(counters)

	if counters == nil {
		return nil
	}

	return m.encodeCounters(counters)
}

// PipelineCounters returns pipeline counters
func (m *DPConfig) PipelineCounters(
	deviceName string,
	pipelineName string,
) []CounterInfo {
	cDeviceName := C.CString(deviceName)
	defer C.free(unsafe.Pointer(cDeviceName))
	cPipelineName := C.CString(pipelineName)
	defer C.free(unsafe.Pointer(cPipelineName))
	counters := C.yanet_get_pipeline_counters(m.ptr, cDeviceName, cPipelineName)
	defer C.yanet_counter_handle_list_free(counters)

	if counters == nil {
		return nil
	}

	return m.encodeCounters(counters)
}

func (m *DPConfig) FunctionCounters(
	deviceName string,
	pipelineName string,
	functionName string,
) []CounterInfo {
	cDeviceName := C.CString(deviceName)
	defer C.free(unsafe.Pointer(cDeviceName))
	cPipelineName := C.CString(pipelineName)
	defer C.free(unsafe.Pointer(cPipelineName))
	cFunctionName := C.CString(functionName)
	defer C.free(unsafe.Pointer(cFunctionName))
	counters := C.yanet_get_function_counters(m.ptr, cDeviceName, cPipelineName, cFunctionName)
	defer C.yanet_counter_handle_list_free(counters)

	if counters == nil {
		return nil
	}

	return m.encodeCounters(counters)
}

func (m *DPConfig) ChainCounters(
	deviceName string,
	pipelineName string,
	functionName string,
	chainName string,
) []CounterInfo {
	cDeviceName := C.CString(deviceName)
	defer C.free(unsafe.Pointer(cDeviceName))
	cPipelineName := C.CString(pipelineName)
	defer C.free(unsafe.Pointer(cPipelineName))
	cFunctionName := C.CString(functionName)
	defer C.free(unsafe.Pointer(cFunctionName))
	cChainName := C.CString(chainName)
	defer C.free(unsafe.Pointer(cChainName))
	counters := C.yanet_get_chain_counters(m.ptr, cDeviceName, cPipelineName, cFunctionName, cChainName)
	defer C.yanet_counter_handle_list_free(counters)

	if counters == nil {
		return nil
	}

	return m.encodeCounters(counters)
}

// ModuleCounters returns module counters
func (m *DPConfig) ModuleCounters(
	deviceName string,
	pipelineName string,
	functionName string,
	chainName string,
	moduleType string,
	moduleName string,
) []CounterInfo {
	cDeviceName := C.CString(deviceName)
	defer C.free(unsafe.Pointer(cDeviceName))
	cPipelineName := C.CString(pipelineName)
	defer C.free(unsafe.Pointer(cPipelineName))
	cFunctionName := C.CString(functionName)
	defer C.free(unsafe.Pointer(cFunctionName))
	cChainName := C.CString(chainName)
	defer C.free(unsafe.Pointer(cChainName))
	cModuleType := C.CString(moduleType)
	defer C.free(unsafe.Pointer(cModuleType))
	cModuleName := C.CString(moduleName)
	defer C.free(unsafe.Pointer(cModuleName))
	counters := C.yanet_get_module_counters(
		m.ptr,
		cDeviceName,
		cPipelineName,
		cFunctionName,
		cChainName,
		cModuleType,
		cModuleName,
	)
	defer C.yanet_counter_handle_list_free(counters)

	if counters == nil {
		return nil
	}

	return m.encodeCounters(counters)
}
