package vlan

//#cgo CFLAGS: -I../../../ -I../../../lib
//#cgo LDFLAGS: -L../../../build/devices/vlan/api -ldev_vlan_api
//#cgo LDFLAGS: -L../../../build/lib/logging/ -llogging
//
//#include "api/agent.h"
//#include "api/config.h"
//#include "devices/vlan/api/controlplane.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// DeviceConfig wraps C module configuration
type DeviceConfig struct {
	ptr ffi.ShmDeviceConfig
}

// NewDeviceConfig creates a new balancer module configuration
func NewDeviceConfig(
	agent *ffi.Agent,
	name string,
	device *commonpb.Device,
	vlan uint16,
) (
	*DeviceConfig,
	error,
) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	input := device.GetInput()
	output := device.GetOutput()

	var cErr *C.struct_yanet_error
	cCfg := C.cp_device_vlan_config_create(cName, C.uint64_t(len(input)), C.uint64_t(len(output)), C.uint16_t(vlan), &cErr)
	if cCfg == nil {
		return nil, fmt.Errorf("failed to initialize vlan device config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	for idx, pipeline := range input {
		cName := C.CString(pipeline.GetName())
		defer C.free(unsafe.Pointer(cName))
		C.cp_device_vlan_config_set_input_pipeline(
			cCfg,
			C.uint64_t(idx),
			cName,
			C.uint64_t(pipeline.GetWeight()),
		)
	}

	for idx, pipeline := range output {
		cName := C.CString(pipeline.GetName())
		defer C.free(unsafe.Pointer(cName))
		C.cp_device_vlan_config_set_output_pipeline(
			cCfg,
			C.uint64_t(idx),
			cName,
			C.uint64_t(pipeline.GetWeight()),
		)
	}

	ptr := C.cp_device_vlan_create((*C.struct_agent)(agent.AsRawPtr()), cCfg, &cErr)
	if ptr == nil {
		return nil, fmt.Errorf("failed to create vlan device: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return &DeviceConfig{
		ptr: ffi.NewShmDeviceConfig(unsafe.Pointer(ptr)),
	}, nil
}

func (m *DeviceConfig) asRawPtr() *C.struct_cp_device {
	return (*C.struct_cp_device)(m.ptr.AsRawPtr())
}

// AsFFIDevice returns the module configuration as an FFI module
func (m *DeviceConfig) AsFFIDevice() ffi.ShmDeviceConfig {
	return m.ptr
}
