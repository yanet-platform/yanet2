package main

//#cgo CFLAGS: -I../../ -I../../lib
//#cgo LDFLAGS: -L../../build/modules/forward/api -lforward_cp
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#cgo LDFLAGS: -L../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../build/lib/dataplane/config -lconfig_dp
//#cgo LDFLAGS: -L../../build/lib/counters -lcounters
//
//#include "api/agent.h"
//#include "modules/forward/api/controlplane.h"
//
import "C"

import (
	"fmt"
	"os"
	"unsafe"

	"gopkg.in/yaml.v3"
)


type ControlplaneConfig struct {
	NumaCount   int    `yaml:"numa_count"`
	Storage     string `yaml:"storage"`
	AgentName   string `yaml:"agent_name"`
	MemoryLimit uint64 `yaml:"memory_limit"`
}

func main() {
	config := ControlplaneConfig{}

	yamlFile, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Printf("could not read config  #%v ", err)
		os.Exit(-1)
	}
	err = yaml.Unmarshal(yamlFile, &config)
	if err != nil {
		fmt.Printf("could not read config: %v", err)
		os.Exit(-1)
	}

	cPath := C.CString(config.Storage)
	defer C.free(unsafe.Pointer(cPath))

	shm, err := C.yanet_shm_attach(cPath)
	if err != nil {
		panic(err)
	}
	defer C.yanet_shm_detach(shm)

	for numaIdx := 0; numaIdx < config.NumaCount; numaIdx++ {
		counters := C.yanet_get_worker_counters(
			C.yanet_shm_dp_config(shm, C.uint32_t(numaIdx)),
		)
		{
			for idx := C.uint64_t(0); idx < counters.count; idx++ {
				counter := C.yanet_get_counter(counters, idx)
				fmt.Printf("Counter %v %s", idx, C.GoString(&counter.name[0]))

				for idx := 0; idx < 2; idx++ {
					fmt.Printf("%20d", C.yanet_get_counter_value(counter.value_handle, 0, C.uint64_t(idx)))
				}
				fmt.Printf("\n")
			}

		}
	
	for _, pName := range []string{"phy", "virt"} {
		counters := C.yanet_get_pm_counters(C.yanet_shm_dp_config(shm, C.uint32_t(numaIdx)), C.CString("forward"), C.CString("forward0"), C.CString(pName))
		for idx := C.uint64_t(0); idx < counters.count; idx++ {
			counter := C.yanet_get_counter(counters, idx)
			fmt.Printf("Counter forward forward0 %s %s\n", pName, C.GoString(&counter.name[0]))

			for wrk_idx := 0; wrk_idx < 2; wrk_idx++ {
				fmt.Printf("wrk %d: \n", wrk_idx) 
				for idx := 0; idx < int(counter.size); idx++ {
					fmt.Printf("%16d", C.yanet_get_counter_value(counter.value_handle, C.uint64_t(idx), C.uint64_t(wrk_idx)))
				}
			fmt.Printf("\n")
			}
		}
	}
	for _, pName := range []string{"phy", "virt"} {
		counters := C.yanet_get_pipeline_counters(C.yanet_shm_dp_config(shm, C.uint32_t(numaIdx)), C.CString(pName))
		for idx := C.uint64_t(0); idx < counters.count; idx++ {
			counter := C.yanet_get_counter(counters, idx)
			fmt.Printf("Counter %s %s\n", pName, C.GoString(&counter.name[0]))

for wrk_idx := 0; wrk_idx < 2; wrk_idx++ {
				fmt.Printf("wrk %d: \n", wrk_idx) 

			for idx := 0; idx < int(counter.size); idx++ {
				fmt.Printf("%16d", C.yanet_get_counter_value(counter.value_handle, C.uint64_t(idx), C.uint64_t(wrk_idx)))
			}
			fmt.Printf("\n")
			}
		}
	}
}

}
