package ffi

//#cgo CFLAGS: -I../../
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent_counters
//#cgo LDFLAGS: -L../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../build/lib/counters -lcounters
//#cgo LDFLAGS: -L../../build/lib/dataplane/config -lconfig_dp
//#cgo LDFLAGS: -L../../build/lib/errors -lerrors
//#cgo LDFLAGS: -L../../build/lib/counters -lcounter_pattern
//#cgo LDFLAGS: -L../../build/subprojects/regex -lrure
//#include "api/agent.h"
//#include "api/counter.h"
import "C"

import (
	"fmt"
	"iter"
	"unsafe"

	"github.com/c2h5oh/datasize"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
)

// SharedMemory represents a handle to YANET shared memory segment.
type SharedMemory struct {
	ptr *C.struct_yanet_shm
}

// NewSharedMemoryFromRaw wraps an opaque C shared-memory handle.
//
// The pointer is the process-local handle the C API hands out, not the
// segment itself; it must only be used through the C API.
func NewSharedMemoryFromRaw(ptr unsafe.Pointer) *SharedMemory {
	return &SharedMemory{ptr: (*C.struct_yanet_shm)(ptr)}
}

// AttachSharedMemory attaches to YANET shared memory segment.
func AttachSharedMemory(path string) (*SharedMemory, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	ptr, err := C.yanet_shm_attach(cPath)
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to attach to shared memory %q: %w",
			path,
			err,
		)
	}

	return &SharedMemory{ptr: ptr}, nil
}

// Detach detaches from YANET shared memory segment.
//
// Every agent attached to the segment must be closed before detaching,
// because the mapping is unmapped and a later agent operation would fault.
// The mapping length travels inside the handle, so detaching works at any
// point after a successful attach, including before the dataplane has
// written the segment header. The handle is consumed whatever the outcome,
// so a second call is a no-op.
func (m *SharedMemory) Detach() error {
	if m.ptr == nil {
		return nil
	}

	ptr := m.ptr
	m.ptr = nil
	if rc, err := C.yanet_shm_detach(ptr); rc != 0 {
		return fmt.Errorf("failed to detach shared memory: %w", err)
	}

	return nil
}

// AsRawPtr returns the opaque C shared-memory handle.
//
// The pointer is the process-local handle, not the segment itself; it must
// only be used through the C API.
func (m *SharedMemory) AsRawPtr() unsafe.Pointer {
	return unsafe.Pointer(m.ptr)
}

// DPConfig gets configuration of the dataplane instance from shared memory.
func (m *SharedMemory) DPConfig(instanceIdx uint32) *DPConfig {
	ptr := C.yanet_shm_dp_config(m.ptr, C.uint32_t(instanceIdx))

	return &DPConfig{ptr: ptr}
}

// DataplaneReady reports whether the dataplane instance has finished
// initializing its shared memory.
func (m *SharedMemory) DataplaneReady(instanceIdx uint32) bool {
	return C.agent_dp_config_ready(m.ptr, C.uint32_t(instanceIdx)) != 0
}

// AgentAttach attaches a module agent to shared memory on the dataplane instance.
func (m *SharedMemory) AgentAttach(
	name string,
	instanceIdx uint32,
	size datasize.ByteSize,
) (*Agent, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.agent_attach(m.ptr, C.uint32_t(instanceIdx), cName, C.size_t(size), &cErr)
	if ptr == nil {
		return nil, fmt.Errorf("failed to attach agent %q: %w", name, cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return &Agent{name: name, ptr: ptr}, nil
}

// DPConfig represents a handle to dataplane configuration.
type DPConfig struct {
	ptr *C.struct_dp_config
}

func NewDPConfigFromRaw(ptr unsafe.Pointer) *DPConfig {
	return &DPConfig{ptr: (*C.struct_dp_config)(ptr)}
}

func (m *DPConfig) NumaIdx() uint32 {
	return uint32(C.dataplane_instance_numa_idx(m.ptr))
}

func (m *DPConfig) WorkerCount() uint32 {
	return uint32(C.dataplane_instance_worker_count(m.ptr))
}

func (m *DPConfig) CurrentTime() (uint64, bool) {
	var timeNS C.uint64_t
	if C.dataplane_instance_current_time(m.ptr, &timeNS) != 0 {
		return 0, false
	}

	return uint64(timeNS), true
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
				modInfo := C.yanet_get_cp_function_chain_module_info(
					chainInfo,
					idx,
				)
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
			function := C.yanet_get_cp_pipeline_function_info_id(
				pipelineInfo,
				idx,
			)
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
	if agentListInfo == nil {
		return nil
	}
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
			rc := C.yanet_get_cp_agent_instance_info(
				agentInfo,
				instIdx,
				&instanceInfo,
			)
			if rc != 0 {
				panic("FFI corruption: agent instance index became invalid")
			}

			nodeCount := uint64(instanceInfo.memory_node_count)
			memoryTree := make([]AgentMemoryNode, nodeCount)
			if nodeCount > 0 {
				cNodes := unsafe.Slice(
					(*C.struct_cp_memory_node_info)(
						unsafe.Add(
							unsafe.Pointer(instanceInfo),
							unsafe.Sizeof(*instanceInfo),
						),
					),
					nodeCount,
				)
				for nodeIdx := range memoryTree {
					n := &cNodes[nodeIdx]
					memoryTree[nodeIdx] = AgentMemoryNode{
						Name:        C.GoString(&n.name[0]),
						ParentIdx:   uint32(n.parent_idx),
						BAllocCount: uint64(n.balloc_count),
						BFreeCount:  uint64(n.bfree_count),
						BAllocSize:  uint64(n.balloc_size),
						BFreeSize:   uint64(n.bfree_size),
					}
				}
			}

			instances[instIdx] = AgentInstanceInfo{
				PID:         uint32(instanceInfo.pid),
				MemoryLimit: uint64(instanceInfo.memory_limit),
				FreeBytes:   uint64(instanceInfo.free_bytes),
				Gen:         uint64(instanceInfo.gen),
				MemoryTree:  memoryTree,
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

// AgentMemoryNode is one entry in a flat depth-first snapshot of an agent's
// memory-context tree.
//
// Copied from shared memory by the C info API; the caller never accesses
// shared memory directly through this type.
type AgentMemoryNode struct {
	Name        string
	ParentIdx   uint32
	BAllocCount uint64
	BFreeCount  uint64
	BAllocSize  uint64
	BFreeSize   uint64
}

// AgentInstanceInfo contains details about a specific agent instance.
type AgentInstanceInfo struct {
	PID         uint32
	MemoryLimit uint64
	FreeBytes   uint64
	Gen         uint64
	// MemoryTree is a flat depth-first snapshot of the agent's
	// memory-context tree.
	//
	// Index 0 is the root; each later node references its parent via
	// ParentIdx. Best-effort: a concurrent update may make a node appear or
	// vanish between traversal passes.
	MemoryTree []AgentMemoryNode
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
	Index           uint32
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
			pipelineInfo := C.yanet_get_cp_device_input_pipeline_info(
				deviceInfo,
				pipelineIdx,
			)
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
			pipelineInfo := C.yanet_get_cp_device_output_pipeline_info(
				deviceInfo,
				pipelineIdx,
			)
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
			Index:           uint32(deviceInfo.index),
			InputPipelines:  inputPipelines,
			OutputPipelines: outputPipelines,
		}
	}

	return out
}

type ModuleReference struct {
	Device     string
	Pipeline   string
	Function   string
	Chain      string
	ModuleType string
	ModuleName string
}

func (m *DPConfig) AllModulePositions(moduleType string) iter.Seq[ModuleReference] {
	deviceList := m.Devices()

	pipelineList := m.Pipelines()
	pipelineFunctions := make(map[string][]string)
	for _, pipeline := range pipelineList {
		pipelineFunctions[pipeline.Name] = pipeline.Functions
	}

	functionList := m.Functions()
	functions := make(map[string][]Chain)
	for _, function := range functionList {
		functions[function.Name] = function.Chains
	}

	return func(yield func(ModuleReference) bool) {
		for _, device := range deviceList {
			pipelineVariants := [][]DevicePipelineInfo{
				device.InputPipelines,
				device.OutputPipelines,
			}
			for _, pipelines := range pipelineVariants {
				for _, pipeline := range pipelines {
					for _, function := range pipelineFunctions[pipeline.Name] {
						for _, chain := range functions[function] {
							for _, module := range chain.Modules {
								if module.Type == moduleType {
									if !yield(ModuleReference{
										Device:     device.Name,
										Pipeline:   pipeline.Name,
										Function:   function,
										Chain:      chain.Name,
										ModuleType: module.Type,
										ModuleName: module.Name,
									}) {
										return
									}
								}
							}
						}
					}
				}
			}
		}
	}
}
