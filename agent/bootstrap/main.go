package main

//#cgo CFLAGS: -I../../ -I../../lib
//#cgo LDFLAGS: -L../../build/modules/forward/api -lforward_cp
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#cgo LDFLAGS: -L../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../build/lib/dataplane/config -lconfig_dp
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
	NumaCount      int                   `yaml:"numa_count"`
	Storage        string                `yaml:"storage"`
	AgentName      string                `yaml:"agent_name"`
	MemoryLimit    uint64                `yaml:"memory_limit"`
	ModuleName     string                `yaml:"module_name"`
	DeviceForwards []ForwardDeviceConfig `yaml:"devices"`
}

func main() {
	config := ForwardConfig{}

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

		forward := C.forward_module_config_init(
			agent,
			C.CString(config.ModuleName),
			C.uint16_t(len(config.DeviceForwards)),
		)

		for devIdx, device := range config.DeviceForwards {
			C.forward_module_config_enable_l2(
				forward,
				C.uint16_t(devIdx),
				C.uint16_t(device.L2ForwardDeviceID),
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
				)
			}
		}

		C.agent_update_modules(
			agent,
			1,
			&forward,
		)

		pipeline0 := C.pipeline_config_create(C.CString("phy"), 1)
		defer C.pipeline_config_free(pipeline0)
		C.pipeline_config_set_module(pipeline0, 0, C.CString("forward"), C.CString(config.ModuleName))

		pipeline1 := C.pipeline_config_create(C.CString("virt"), 1)
		defer C.pipeline_config_free(pipeline1)
		C.pipeline_config_set_module(pipeline1, 0, C.CString("forward"), C.CString(config.ModuleName))

		pipelines := [2]*C.struct_pipeline_config{
			pipeline0,
			pipeline1,
		}
		C.agent_update_pipelines(
			agent,
			2,
			&pipelines[0],
		)
	}
}
