package ffi

//#cgo CFLAGS: -I../../
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#cgo LDFLAGS: -L../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../build/lib/dataplane/config -lconfig_dp
//#include "api/agent.h"
import "C"
import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/bitset"
)

// SharedMemory represents a handle to YANET shared memory segment.
type SharedMemory struct {
	ptr *C.struct_yanet_shm
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

// DPConfig gets dataplane configuration from shared memory.
func (m *SharedMemory) DPConfig(numaIdx uint32) *DPConfig {
	ptr := C.yanet_shm_dp_config(m.ptr, C.uint32_t(numaIdx))

	return &DPConfig{ptr: ptr}
}

// AgentAttach attaches a module agent to shared memory on the specified NUMA node.
func (m *SharedMemory) AgentAttach(name string, numaIdx uint32, size uint) (*Agent, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr := C.agent_attach(m.ptr, C.uint32_t(numaIdx), cName, C.size_t(size))
	if ptr == nil {
		return nil, fmt.Errorf("failed to attach agent: %s", name)
	}

	return &Agent{ptr: ptr}, nil
}

// AgentsAttach attaches agents to shared memory on the specified list of NUMA nodes.
func (m *SharedMemory) AgentsAttach(name string, numaIndices []uint32, size uint) ([]*Agent, error) {
	agents := make([]*Agent, 0, len(numaIndices))
	for _, numaIdx := range numaIndices {
		agent, err := m.AgentAttach(name, numaIdx, uint(size))
		if err != nil {
			return nil, fmt.Errorf("failed to connect to shared memory on NUMA %d: %w", numaIdx, err)
		}

		agents = append(agents, agent)
	}
	return agents, nil
}

// NumaMap returns the NUMA node mapping as a bitmap.
//
// The returned value is a bitmap where each bit represents a NUMA node that
// is available in the system.
//
// Bit 0 represents NUMA node 0, bit 1 represents NUMA node 1, and so on.
// A bit set to 1 indicates that the corresponding NUMA node is available for
// use by YANET.
func (m *SharedMemory) NumaMap() uint32 {
	return uint32(C.yanet_shm_numa_map(m.ptr))
}

// NumaIndices returns a list of indices available NUMA nodes.
func (m *SharedMemory) NumaIndices() []uint32 {
	numaIndices := make([]uint32, 0)
	bitset.NewBitsTraverser(uint64(m.NumaMap())).Traverse(func(numaIdx int) {
		numaIndices = append(numaIndices, uint32(numaIdx))
	})
	return numaIndices
}

// DPConfig represents a handle to dataplane configuration.
type DPConfig struct {
	ptr *C.struct_dp_config
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
