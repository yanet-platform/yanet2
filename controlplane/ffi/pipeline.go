package ffi

//#cgo CFLAGS: -I../../
//#cgo LDFLAGS: -L../../build/lib/controlplane/agent -lagent
//#include "api/agent.h"
import "C"
import (
	"fmt"
	"unsafe"
)

// TODO: docs
type PipelineConfig struct {
	Name  string
	Chain []PipelineModuleConfig
}

// TODO: docs
type PipelineModuleConfig struct {
	ModuleName string
	ConfigName string
}

// TODO: docs
type pipelineConfig struct {
	ptr *C.struct_pipeline_config
}

// TODO: docs
func newPipelineConfig(name string, numChains int) (*pipelineConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.pipeline_config_create(cName, C.uint64_t(numChains))
	if err != nil {
		return nil, fmt.Errorf("failed to create ffi pipeline config: %w", err)
	}
	if ptr == nil {
		return nil, fmt.Errorf("failed to create ffi pipeline config")
	}

	return &pipelineConfig{ptr: ptr}, nil
}

// TODO: docs
func (m *pipelineConfig) Free() {
	if m.ptr != nil {
		C.pipeline_config_free(m.ptr)
		m.ptr = nil
	}
}

func (m *pipelineConfig) AsRawPtr() *C.struct_pipeline_config {
	return m.ptr
}

func (m *pipelineConfig) SetNode(idx int, moduleName string, configName string) error {
	cModuleName := C.CString(moduleName)
	defer C.free(unsafe.Pointer(cModuleName))

	cConfigName := C.CString(configName)
	defer C.free(unsafe.Pointer(cConfigName))

	_, err := C.pipeline_config_set_module(
		m.ptr,
		C.uint64_t(idx),
		cModuleName,
		cConfigName,
	)
	if err != nil {
		return fmt.Errorf("failed to set pipeline node: %w", err)
	}

	return nil
}
