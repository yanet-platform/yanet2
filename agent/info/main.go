package main

//#cgo CFLAGS: -I../../ -I../../lib
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#cgo LDFLAGS: -L../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../build/lib/dataplane/config -lconfig_dp
//#include "api/agent.h"
//
import "C"

import (
	"fmt"
	"unsafe"
)

type ControlModule struct {
	ModuleName string
	ConfigName string
}

func main() {
	cPath := C.CString("/dev/hugepages/yanet")
	defer C.free(unsafe.Pointer(cPath))

	shm, err := C.yanet_shm_attach(cPath)
	if err != nil {
		panic(err)
	}
	defer C.yanet_shm_detach(shm)

	fmt.Printf("Numa %x\n", C.yanet_shm_numa_map(shm))

	dpConfig := C.yanet_shm_dp_config(shm, 0)

	dp_modules := C.yanet_get_dp_module_list_info(dpConfig)
	defer C.dp_module_list_info_free(dp_modules)

	agents := C.yanet_get_cp_agent_list_info(dpConfig)
	defer C.cp_agent_list_info_free(agents)

	for agentIdx := C.uint64_t(0); agentIdx < agents.count; agentIdx++ {
		agent := (*C.struct_cp_agent_info)(nil)
		C.yanet_get_cp_agent_info(agents, agentIdx, &agent)
		fmt.Printf("Agent %s\n", C.GoString(&agent.name[0]))
		for instanceIdx := C.uint64_t(0); instanceIdx < agent.instance_count; instanceIdx++ {
			instance := (*C.struct_cp_agent_instance_info)(nil)
			C.yanet_get_cp_agent_instance_info(agent, instanceIdx, &instance)
			fmt.Printf("  Pid %v Limit %d Allocated %d Freed %d Gen %d\n",
				instance.pid,
				instance.memory_limit,
				instance.allocated,
				instance.freed,
				instance.gen,
			)
		}
	}

	cp_modules := C.yanet_get_cp_module_list_info(dpConfig)
	defer C.cp_module_list_info_free(cp_modules)

	dataModules := make([]string, 0)

	for idx := C.uint64_t(0); idx < dp_modules.module_count; idx++ {
		var dp_module C.struct_dp_module_info
		C.yanet_get_dp_module_info(dp_modules, idx, &dp_module)
		dataModules = append(dataModules, C.GoString(&dp_module.name[0]))
	}
	fmt.Printf("%s\n", "Dataplane Modules")
	for _, module := range dataModules {
		fmt.Printf("  %s\n", module)
	}

	controlModules := make([]ControlModule, 0)

	for idx := C.uint64_t(0); idx < cp_modules.module_count; idx++ {
		var cp_module C.struct_cp_module_info
		C.yanet_get_cp_module_info(cp_modules, idx, &cp_module)
		controlModules = append(controlModules, ControlModule{
			dataModules[cp_module.index],
			C.GoString(&cp_module.config_name[0]),
		})
	}
	fmt.Printf("%s\n", "Controlplane Configs")
	for _, config := range controlModules {
		fmt.Printf("  %s:%s\n", config.ModuleName, config.ConfigName)
	}

	cp_pipelines := C.yanet_get_cp_pipeline_list_info(dpConfig)
	defer C.cp_pipeline_list_info_free(cp_pipelines)

	pipelines := make([][]ControlModule, 0)
	for idx := C.uint64_t(0); idx < cp_pipelines.count; idx++ {
		pipeline := make([]ControlModule, 0)
		var pipeline_info *C.struct_cp_pipeline_info
		C.yanet_get_cp_pipeline_info(cp_pipelines, idx, &pipeline_info)
		for idx := C.uint64_t(0); idx < pipeline_info.length; idx++ {
			var config_index C.uint64_t
			C.yanet_get_cp_pipeline_module_info(pipeline_info, idx, &config_index)
			pipeline = append(pipeline, controlModules[config_index])
		}
		pipelines = append(pipelines, pipeline)
	}
	fmt.Printf("%s\n", "Pipelines")
	for _, pipeline := range pipelines {
		fmt.Printf("rx -> ")
		for _, config := range pipeline {
			fmt.Printf("%s:%s -> ", config.ModuleName, config.ConfigName)
		}
		fmt.Printf("tx\n")
	}
}
