package ffi

//#include "dataplane/config/zone.h"
import "C"

type DPConfig struct {
	ptr *C.struct_dp_config
}

func (m *DPConfig) ModulesCount() int {
	return int(C.dp_config_modules_count(m.ptr))
}

func (m *DPConfig) ModuleByIndex(index int) *DPModule {
	ptr := C.dp_config_module_by_index(m.ptr, C.size_t(index))
	if ptr == nil {
		return nil
	}

	return &DPModule{
		ptr: ptr,
	}
}

type DPModule struct {
	ptr *C.struct_dp_module
}

// Name returns the name of the module.
func (m *DPModule) Name() string {
	return C.GoString(&m.ptr.name[0])
}
