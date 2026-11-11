// Package cvlan is the Go binding for the vlan device.
package cvlan

//#cgo CFLAGS: -I../../../../../
//
// #include "api/agent.h"
// #include "api/config.h"
// #include "devices/vlan/api/controlplane.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Pipeline is a weighted input/output pipeline assignment for the device.
type Pipeline struct {
	Name   string
	Weight uint64
}

// DeviceConfig is an opaque handle to the vlan device configuration in
// shared memory.
type DeviceConfig struct {
	ptr ffi.ShmDeviceConfig
}

// NewDeviceConfig allocates a new vlan device configuration via the C API.
func NewDeviceConfig(
	agent *ffi.Agent,
	name string,
	input []Pipeline,
	output []Pipeline,
	vlanID uint16,
) (*DeviceConfig, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.struct_yanet_error
	cCfg := C.cp_device_vlan_config_new(
		cName,
		C.uint64_t(len(input)),
		C.uint64_t(len(output)),
		C.uint16_t(vlanID),
		&cErr,
	)
	if cCfg == nil {
		return nil, fmt.Errorf(
			"failed to initialize vlan device config: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	defer C.cp_device_vlan_config_free(cCfg)

	for idx, pipeline := range input {
		cName := C.CString(pipeline.Name)
		defer C.free(unsafe.Pointer(cName))
		C.cp_device_vlan_config_set_input_pipeline(
			cCfg,
			C.uint64_t(idx),
			cName,
			C.uint64_t(pipeline.Weight),
		)
	}

	for idx, pipeline := range output {
		cName := C.CString(pipeline.Name)
		defer C.free(unsafe.Pointer(cName))
		C.cp_device_vlan_config_set_output_pipeline(
			cCfg,
			C.uint64_t(idx),
			cName,
			C.uint64_t(pipeline.Weight),
		)
	}

	ptr := C.cp_device_vlan_new((*C.struct_agent)(agent.AsRawPtr()), cCfg, &cErr)
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to create vlan device: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}

	return &DeviceConfig{
		ptr: ffi.NewShmDeviceConfig(unsafe.Pointer(ptr)),
	}, nil
}

// LookupVlan returns the vlan id of the live vlan device with the given
// name.
//
// A name with no vlan device reports ffi.ErrNotFound.
func LookupVlan(agent *ffi.Agent, name string) (uint16, error) {
	if agent == nil {
		return 0, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cVlan C.uint16_t
	var cErr *C.struct_yanet_error
	rc := C.cp_device_vlan_get_vlan((*C.struct_agent)(agent.AsRawPtr()), cName, &cVlan, &cErr)
	if rc != 0 {
		return 0, fmt.Errorf("failed to look up vlan device: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return uint16(cVlan), nil
}

// AsFFIDevice returns the underlying shared-memory device config handle.
func (m *DeviceConfig) AsFFIDevice() ffi.ShmDeviceConfig {
	return m.ptr
}

// Free destroys the device, or reports ffi.ErrStillReferenced while a
// live generation still holds it. Safe to call multiple times.
func (m *DeviceConfig) Free() error {
	return m.ptr.Free(func(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
		var cErr *C.yanet_error
		rc, errno := C.cp_device_vlan_free((*C.struct_cp_device)(ptr), &cErr)
		return int(rc), unsafe.Pointer(cErr), errno
	})
}
