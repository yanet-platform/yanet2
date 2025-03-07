package main

//#cgo CFLAGS: -I../../ -I../../lib
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#include "api/agent.h"
//
import "C"

import "fmt"

type ControlModule struct {
	ModuleName string
	ConfigName string
}

func main() {
	yanet := C.yanet_attach(
		C.CString("/dev/hugepages/yanet-0"),
	)

	dp_modules := C.yanet_get_dp_module_list_info(yanet)
	defer C.dp_module_list_info_free(dp_modules)

	cp_modules := C.yanet_get_cp_module_list_info(yanet)
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

	cp_pipelines := C.yanet_get_cp_pipeline_list_info(yanet)
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
