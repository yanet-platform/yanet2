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
	"net/netip"
	"os"
	"unsafe"

	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/xnetip"
)

type V4ForwardConfig struct {
	Network  string `yaml:"network"`
	DeviceID uint16 `yaml:"device_id"`
}

type V6ForwardConfig struct {
	Network  string `yaml:"network"`
	DeviceID uint16 `yaml:"device_id"`
}

type ForwardDeviceConfig struct {
	L2ForwardDeviceID uint16            `yaml:"l2_forward_device_id"`
	V4Forwards        []V4ForwardConfig `yaml:"v4_forwards"`
	V6Forwards        []V6ForwardConfig `yaml:"v6_forwards"`
}

type ForwardConfig struct {
	ConfigName     string                `yaml:"config_name"`
	DeviceForwards []ForwardDeviceConfig `yaml:"devices"`
}

type PipelineModuleConfig struct {
	ModuleName string `yaml:"module_name"`
	ConfigName string `yaml:"config_name"`
}

type PipelineConfig struct {
	Name  string                 `yaml:"name"`
	Chain []PipelineModuleConfig `yaml:"chain"`
}

type DevicePipeline struct {
	Name   string `yaml:"name"`
	Weight uint   `yaml:"weight"`
}

type ControlplaneConfig struct {
	NumaCount   int    `yaml:"numa_count"`
	Storage     string `yaml:"storage"`
	AgentName   string `yaml:"agent_name"`
	MemoryLimit uint64 `yaml:"memory_limit"`

	Pipelines       []PipelineConfig         `yaml:"pipelines"`
	DevicePipelines map[string][]DevicePipeline `yaml:"device_pipelines"`

	Forward ForwardConfig `yaml:"forward"`
}

func configureDevices(
	agent *C.struct_agent,
	devices map[string][]DevicePipeline,
) {
	if devices == nil {
		return
	}

	configs := make([]*C.struct_cp_device_config, 0)

	for id, pipelines := range devices {
		deviceConfig := C.cp_device_config_create(C.CString(id), C.uint64_t(len(pipelines)))
		for _, pipeline := range pipelines {
			C.cp_device_config_add_pipeline(
				deviceConfig,
				C.CString(pipeline.Name),
				C.uint64_t(pipeline.Weight),
			)
		}
		configs = append(configs, deviceConfig)
	}

	C.agent_update_devices(
		agent,
		C.uint64_t(len(configs)),
		&configs[0],
	)

	for _, config := range configs {
		C.cp_device_config_free(config)
	}
}

func configureForward(
	agent *C.struct_agent,
	config ForwardConfig,
) {
	forward := C.forward_module_config_init(
		agent,
		C.CString(config.ConfigName),
	)

	for devIdx, device := range config.DeviceForwards {
		C.forward_module_config_enable_l2(
			forward,
			C.uint16_t(devIdx),
			C.uint16_t(device.L2ForwardDeviceID),
			C.CString(fmt.Sprintf("l2-%v->%v", devIdx, device.L2ForwardDeviceID)),
		)

		for _, forwardConfig := range device.V4Forwards {
			network, err := netip.ParsePrefix(forwardConfig.Network)
			if err != nil {
				fmt.Printf("invalid CIDR")
				os.Exit(-1)
			}
			from := network.Masked().Addr().As4()
			to := xnetip.LastAddr(network).As4()

			C.forward_module_config_enable_v4(
				forward,
				(*C.uint8_t)(&from[0]),
				(*C.uint8_t)(&to[0]),
				C.uint16_t(devIdx),
				C.uint16_t(forwardConfig.DeviceID),
			C.CString(fmt.Sprintf("v4-%v-%v", devIdx, forwardConfig.DeviceID)),
			)
		}

		for _, forwardConfig := range device.V6Forwards {
			network, err := netip.ParsePrefix(forwardConfig.Network)
			if err != nil {
				fmt.Printf("invalid CIDR")
				os.Exit(-1)
			}
			from := network.Masked().Addr().As16()
			to := xnetip.LastAddr(network).As16()

			C.forward_module_config_enable_v6(
				forward,
				(*C.uint8_t)(&from[0]),
				(*C.uint8_t)(&to[0]),
				C.uint16_t(devIdx),
				C.uint16_t(forwardConfig.DeviceID),
				C.CString(fmt.Sprintf("v6-%v-%v", devIdx, forwardConfig.DeviceID)),
			)
		}
	}

	C.agent_update_modules(
		agent,
		1,
		&forward,
	)

}

func configurePipelines(
	agent *C.struct_agent,
	pipelineConfigs []PipelineConfig,
) {
	if pipelineConfigs == nil {
		return
	}

	pipelines := make(
		[]*C.struct_pipeline_config,
		0,
		len(pipelineConfigs),
	)

	for _, pipelineConfig := range pipelineConfigs {

		pipeline := C.pipeline_config_create(
			C.CString(pipelineConfig.Name),
			C.uint64_t(len(pipelineConfig.Chain)),
		)
		defer C.pipeline_config_free(pipeline)

		for idx, item := range pipelineConfig.Chain {
			C.pipeline_config_set_module(
				pipeline,
				C.uint64_t(idx),
				C.CString(item.ModuleName),
				C.CString(item.ConfigName),
			)
		}

		pipelines = append(pipelines, pipeline)
	}

	C.agent_update_pipelines(
		agent,
		C.uint64_t(len(pipelineConfigs)),
		&pipelines[0],
	)
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
		agent := C.agent_attach(
			shm,
			C.uint32_t(numaIdx),
			C.CString(config.AgentName),
			C.uint64_t(config.MemoryLimit),
		)

		configureForward(agent, config.Forward)

		configurePipelines(agent, config.Pipelines)

		configureDevices(agent, config.DevicePipelines)

		for pIdx := range config.Pipelines {
			counters := C.yanet_get_pm_counters(C.yanet_shm_dp_config(shm, C.uint32_t(numaIdx)), C.CString("forward"), C.CString("forward0"), C.CString(config.Pipelines[pIdx].Name))
			for idx := C.uint64_t(0); idx < counters.count; idx++ {
				counter := C.yanet_get_counter(counters, idx)
				fmt.Printf("Counter forward forward0 %s %s", config.Pipelines[pIdx].Name, C.GoString(&counter.name[0]))

				for idx := 0; idx < 2; idx++ {
					fmt.Printf("%20d", C.yanet_get_counter_value(counter.value_handle, 0, C.uint64_t(idx)))
				}
				fmt.Printf("\n")
			}
		}

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
	}

}
