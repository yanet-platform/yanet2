package vlan

//#cgo CFLAGS: -I../../../
//#cgo LDFLAGS: -L../../../build/devices/vlan/api -ldev_vlan_api
//#cgo LDFLAGS: -L../../../build/lib/logging/ -llogging
//
//#include "api/agent.h"
//#include "api/config.h"
//#include "devices/vlan/api/controlplane.h"
import "C"

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
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
	cCfg := C.cp_device_vlan_config_new(cName, C.uint64_t(len(input)), C.uint64_t(len(output)), C.uint16_t(vlan), &cErr)
	if cCfg == nil {
		return nil, fmt.Errorf("failed to initialize vlan device config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	defer C.cp_device_vlan_config_free(cCfg)

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

	ptr := C.cp_device_vlan_new((*C.struct_agent)(agent.AsRawPtr()), cCfg, &cErr)
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

// Free destroys the vlan device when it is dangling — referenced by no live
// configuration generation — and reports nil. While a live generation
// still references it the free is refused with ffi.ErrStillReferenced
// and the handle stays usable: the caller must remember it and free it
// again once the generations holding it drain. Safe to call multiple
// times: subsequent calls are no-ops reporting nil.
func (m *DeviceConfig) Free() error {
	ptr := m.asRawPtr()
	if ptr == nil {
		return nil
	}
	var cErr *C.yanet_error
	rc, errno := C.cp_device_vlan_free(ptr, &cErr)
	if rc == 0 {
		m.ptr = ffi.ShmDeviceConfig{}
		return nil
	}
	if errors.Is(errno, syscall.EAGAIN) {
		// The refused attempt allocated an error chain; release it
		// rather than leaking one per attempt. The object is intact.
		C.yanet_error_free(cErr)
		return ffi.ErrStillReferenced
	}
	return fmt.Errorf("failed to free vlan device: %w", cerrors.FromC(unsafe.Pointer(cErr)))
}
