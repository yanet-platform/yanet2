package ffi

//#cgo CFLAGS: -I../../
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#include "api/agent.h"
import "C"
import (
	"fmt"
	"unsafe"
)

type ChainModuleConfig struct {
	Type string
	Name string
}

type ChainConfig struct {
	Name    string
	Modules []ChainModuleConfig
}

type FunctionChainConfig struct {
	Chain  ChainConfig
	Weight uint64
}

type FunctionConfig struct {
	Name   string
	Chains []FunctionChainConfig
}

// TODO: docs
type PipelineConfig struct {
	Name      string
	Functions []string
}

type DevicePipelineConfig struct {
	Name   string
	Weight uint64
}

type DeviceConfig struct {
	Name      string
	Pipelines []DevicePipelineConfig
}

type functionConfig struct {
	ptr *C.struct_cp_function_config
}

// TODO: docs
type pipelineConfig struct {
	ptr *C.struct_cp_pipeline_config
}

type deviceConfig struct {
	ptr *C.struct_cp_device_config
}

// TODO: docs
func newFunctionConfig(config FunctionConfig) (*functionConfig, error) {
	cName := C.CString(config.Name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.cp_function_config_create(cName, C.uint64_t(len(config.Chains)))
	if err != nil {
		return nil, fmt.Errorf("failed to create ffi function config: %w", err)
	}
	if ptr == nil {
		return nil, fmt.Errorf("failed to create ffi function config")
	}

	for idx, chain := range config.Chains {
		chainName := C.CString(chain.Chain.Name)
		defer C.free(unsafe.Pointer(chainName))

		types := make([]*C.char, 0, len(chain.Chain.Modules))
		names := make([]*C.char, 0, len(chain.Chain.Modules))

		for _, module := range chain.Chain.Modules {
			cType := C.CString(module.Type)
			defer C.free(unsafe.Pointer(cType))

			cName := C.CString(module.Name)
			defer C.free(unsafe.Pointer(cName))

			types = append(types, cType)
			names = append(names, cName)
		}

		chainPtr, err := C.cp_chain_config_create(chainName, C.uint64_t(len(chain.Chain.Modules)), &types[0], &names[0])
		if err != nil {
			C.cp_function_config_free(ptr)
			return nil, fmt.Errorf("failed to create ffi function config: %w", err)
		}

		if C.cp_function_config_set_chain(ptr, C.uint64_t(idx), chainPtr, C.uint64_t(chain.Weight)) != 0 {
			C.cp_function_config_free(ptr)
			return nil, fmt.Errorf("failed to create ffi function config")
		}
	}

	return &functionConfig{ptr: ptr}, nil
}

// TODO: docs
func (m *functionConfig) Free() {
	if m.ptr != nil {
		C.cp_function_config_free(m.ptr)
		m.ptr = nil
	}
}

func (m *functionConfig) AsRawPtr() *C.struct_cp_function_config {
	return m.ptr
}

// TODO: docs
func newPipelineConfig(config PipelineConfig) (*pipelineConfig, error) {
	cName := C.CString(config.Name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.cp_pipeline_config_create(cName, C.uint64_t(len(config.Functions)))
	if err != nil {
		return nil, fmt.Errorf("failed to create ffi pipeline config: %w", err)
	}
	if ptr == nil {
		return nil, fmt.Errorf("failed to create ffi pipeline config")
	}

	for idx, function := range config.Functions {
		fName := C.CString(function)
		defer C.free(unsafe.Pointer(fName))

		if C.cp_pipeline_config_set_function(ptr, C.uint64_t(idx), fName) != 0 {
			C.cp_pipeline_config_free(ptr)
			return nil, fmt.Errorf("failed to create ffi pipeline config")
		}
	}

	return &pipelineConfig{ptr: ptr}, nil
}

// TODO: docs
func (m *pipelineConfig) Free() {
	if m.ptr != nil {
		C.cp_pipeline_config_free(m.ptr)
		m.ptr = nil
	}
}

func (m *pipelineConfig) AsRawPtr() *C.struct_cp_pipeline_config {
	return m.ptr
}

// TODO: docs
func newDeviceConfig(config DeviceConfig) (*deviceConfig, error) {
	cName := C.CString(config.Name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.cp_device_config_create(cName, C.uint64_t(len(config.Pipelines)))
	if err != nil {
		return nil, fmt.Errorf("failed to create ffi pipeline config: %w", err)
	}
	if ptr == nil {
		return nil, fmt.Errorf("failed to create ffi pipeline config")
	}

	for idx, pipeline := range config.Pipelines {
		pName := C.CString(pipeline.Name)
		defer C.free(unsafe.Pointer(pName))

		if C.cp_device_config_set_pipeline(ptr, C.uint64_t(idx), pName, C.uint64_t(pipeline.Weight)) != 0 {
			C.cp_device_config_free(ptr)
			return nil, fmt.Errorf("failed to create ffi pipeline config")
		}
	}

	return &deviceConfig{ptr: ptr}, nil
}

// TODO: docs
func (m *deviceConfig) Free() {
	if m.ptr != nil {
		C.cp_device_config_free(m.ptr)
		m.ptr = nil
	}
}

func (m *deviceConfig) AsRawPtr() *C.struct_cp_device_config {
	return m.ptr
}
