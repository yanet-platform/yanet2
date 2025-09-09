package main

//#cgo CFLAGS: -I../../ -I../../lib
//#cgo LDFLAGS: -L../../build/modules/forward/api -lforward_cp
//#cgo LDFLAGS: -L../../build/filter -lfilter
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

type ChainConfig struct {
	Weight  uint                   `yaml:"weight"`
	Modules []PipelineModuleConfig `yaml:"modules"`
}

type FunctionConfig struct {
	Chains map[string]ChainConfig `yaml:"chains"`
}

type PipelineConfig struct {
	Functions []string `yaml:"functions"`
}

type DevicePipeline struct {
	Weight uint `yaml:"weight"`
}

type ACLConfig struct {
	ConfigName  string `yaml:"config_name"`
	RulesetPath string `yaml:"ruleset_path"`
}

type ControlplaneConfig struct {
	InstanceCount int    `yaml:"instance_count"`
	Storage       string `yaml:"storage"`
	AgentName     string `yaml:"agent_name"`
	MemoryLimit   uint64 `yaml:"memory_limit"`

	Functions       map[string]FunctionConfig            `yaml:functions`
	Pipelines       map[string]PipelineConfig            `yaml:"pipelines"`
	DevicePipelines map[string]map[string]DevicePipeline `yaml:"device_pipelines"`

	Forward ForwardConfig `yaml:"forward"`

	ACL ACLConfig `yaml:"acl"`
}

func configureFunctions(
	agent *C.struct_agent,
	functions map[string]FunctionConfig,
) {
	if functions == nil {
		return
	}

	configs := make([]*C.struct_cp_function_config, 0)

	for name, cfg := range functions {
		func_cfg := C.cp_function_config_create(C.CString(name), C.uint64_t(len(cfg.Chains)))

		chain_idx := C.uint64_t(0)
		for name, chain := range cfg.Chains {
			types := make([]*C.char, 0)
			names := make([]*C.char, 0)

			for _, mod := range chain.Modules {
				types = append(types, C.CString(mod.ModuleName))
				names = append(names, C.CString(mod.ConfigName))
			}

			chain_cfg := C.cp_chain_config_create(C.CString(name), C.uint64_t(len(chain.Modules)), &types[0], &names[0])
			C.cp_function_config_set_chain(func_cfg, chain_idx, chain_cfg, C.uint64_t(chain.Weight))
			chain_idx++
		}

		configs = append(configs, func_cfg)
	}

	C.agent_update_functions(
		agent,
		C.uint64_t(len(configs)),
		&configs[0],
	)

	for _, config := range configs {
		C.cp_function_config_free(config)
	}
}

func configureDevices(
	agent *C.struct_agent,
	devices map[string]map[string]DevicePipeline,
) {
	if devices == nil {
		return
	}

	configs := make([]*C.struct_cp_device_config, 0)

	for id, pipelines := range devices {
		deviceConfig := C.cp_device_config_create(C.CString(id), C.uint64_t(len(pipelines)))
		idx := uint64(0)
		for name, pipeline := range pipelines {
			C.cp_device_config_set_pipeline(
				deviceConfig,
				C.uint64_t(idx),
				C.CString(name),
				C.uint64_t(pipeline.Weight),
			)

			idx++
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
	pipelineConfigs map[string]PipelineConfig,
) {
	if pipelineConfigs == nil {
		return
	}

	pipelines := make(
		[]*C.struct_cp_pipeline_config,
		0,
		len(pipelineConfigs),
	)

	for name, pipelineConfig := range pipelineConfigs {

		pipeline := C.cp_pipeline_config_create(
			C.CString(name),
			C.uint64_t(len(pipelineConfig.Functions)),
		)
		defer C.cp_pipeline_config_free(pipeline)

		for idx, name := range pipelineConfig.Functions {
			C.cp_pipeline_config_set_function(
				pipeline,
				C.uint64_t(idx),
				C.CString(name),
			)
		}

		pipelines = append(pipelines, pipeline)
	}

	pth := C.agent_update_pipelines(
		agent,
		C.uint64_t(len(pipelineConfigs)),
		&pipelines[0],
	)

	fmt.Printf("pipes %v\n", pth)
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

	fmt.Printf("cfg: %+v\n", config)

	cPath := C.CString(config.Storage)
	defer C.free(unsafe.Pointer(cPath))

	shm, err := C.yanet_shm_attach(cPath)
	if err != nil {
		panic(err)
	}
	defer C.yanet_shm_detach(shm)

	for instance := 0; instance < config.InstanceCount; instance++ {
		agent := C.agent_attach(
			shm,
			C.uint32_t(instance),
			C.CString(config.AgentName),
			C.uint64_t(config.MemoryLimit),
		)

		configureForward(agent, config.Forward)

		//		configureACL(agent, config.ACL)

		configureFunctions(agent, config.Functions)

		configurePipelines(agent, config.Pipelines)

		configureDevices(agent, config.DevicePipelines)

	}

}
