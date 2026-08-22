package plain

//#cgo CFLAGS: -I../../../
//#cgo LDFLAGS: -L../../../build/devices/plain/api -ldev_plain_api
//#cgo LDFLAGS: -L../../../build/lib/logging/ -llogging
//
//#include "api/agent.h"
//#include "api/config.h"
//#include "devices/plain/api/controlplane.h"
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
	cCfg := C.cp_device_plain_config_new(cName, C.uint64_t(len(input)), C.uint64_t(len(output)), &cErr)
	if cCfg == nil {
		return nil, fmt.Errorf("failed to initialize plain device config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	defer C.cp_device_plain_config_free(cCfg)

	for idx, pipeline := range input {
		cName := C.CString(pipeline.GetName())
		defer C.free(unsafe.Pointer(cName))
		C.cp_device_plain_config_set_input_pipeline(
			cCfg,
			C.uint64_t(idx),
			cName,
			C.uint64_t(pipeline.GetWeight()),
		)
	}

	for idx, pipeline := range output {
		cName := C.CString(pipeline.GetName())
		defer C.free(unsafe.Pointer(cName))
		C.cp_device_plain_config_set_output_pipeline(
			cCfg,
			C.uint64_t(idx),
			cName,
			C.uint64_t(pipeline.GetWeight()),
		)
	}

	ptr := C.cp_device_plain_new((*C.struct_agent)(agent.AsRawPtr()), cCfg, &cErr)
	if ptr == nil {
		return nil, fmt.Errorf("failed to create plain device: %w", cerrors.FromC(unsafe.Pointer(cErr)))
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

// Free destroys the plain device when it is dangling — referenced by no live
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
	rc, errno := C.cp_device_plain_free(ptr, &cErr)
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
	return fmt.Errorf("failed to free plain device: %w", cerrors.FromC(unsafe.Pointer(cErr)))
}

// UpdateDevices creates one plain device per configuration and
// publishes them all in a single configuration update, returning the
// created handles in order.
//
// The caller becomes the owner of the returned handles: each device is
// referenced by the live generation, so freeing a handle now is refused
// with ffi.ErrStillReferenced and the owner must retry once the
// generations drain. On failure every handle is destroyed here — the
// devices were never registered — and nil is returned.
func UpdateDevices(agent *ffi.Agent, devices []ffi.DeviceConfig) ([]*DeviceConfig, error) {
	handles := make([]*DeviceConfig, 0, len(devices))
	defer func() {
		if len(handles) == 0 {
			return
		}
		// Reached only on failure: every created device was either
		// never registered or its publish failed partway, so each one
		// is dangling and destroyed on the spot.
		for _, handle := range handles {
			_ = handle.Free()
		}
	}()

	configs := make([]ffi.ShmDeviceConfig, 0, len(devices))
	for _, deviceCfg := range devices {
		common := &commonpb.Device{}
		for _, pipeline := range deviceCfg.Input {
			common.Input = append(common.Input, &commonpb.DevicePipeline{
				Name:   pipeline.Name,
				Weight: pipeline.Weight,
			})
		}
		for _, pipeline := range deviceCfg.Output {
			common.Output = append(common.Output, &commonpb.DevicePipeline{
				Name:   pipeline.Name,
				Weight: pipeline.Weight,
			})
		}

		device, err := NewDeviceConfig(agent, deviceCfg.Name, common)
		if err != nil {
			return nil, fmt.Errorf("failed to create plain device %q: %w", deviceCfg.Name, err)
		}
		handles = append(handles, device)
		configs = append(configs, device.AsFFIDevice())
	}

	if err := agent.UpdateDevices(configs); err != nil {
		return nil, fmt.Errorf("failed to update devices: %w", err)
	}

	// Success: hand the handles to the caller. Clear the slice so the
	// failure unwind above does not also run.
	created := handles
	handles = nil
	return created, nil
}
