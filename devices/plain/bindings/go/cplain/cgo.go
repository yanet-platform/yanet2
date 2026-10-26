// Package cplain is the Go binding for the plain device.
package cplain

//#cgo CFLAGS: -I../../../../../
//
// #include "api/agent.h"
// #include "api/config.h"
// #include "devices/plain/api/controlplane.h"
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

// DeviceConfig is an opaque handle to the plain device configuration in
// shared memory.
type DeviceConfig struct {
	ptr ffi.ShmDeviceConfig
}

// NewDeviceConfig allocates a new plain device configuration via the C API.
func NewDeviceConfig(
	agent *ffi.Agent,
	name string,
	input []Pipeline,
	output []Pipeline,
) (*DeviceConfig, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.struct_yanet_error
	cCfg := C.cp_device_plain_config_new(
		cName,
		C.uint64_t(len(input)),
		C.uint64_t(len(output)),
		&cErr,
	)
	if cCfg == nil {
		return nil, fmt.Errorf(
			"failed to initialize plain device config: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	defer C.cp_device_plain_config_free(cCfg)

	for idx, pipeline := range input {
		cName := C.CString(pipeline.Name)
		defer C.free(unsafe.Pointer(cName))
		C.cp_device_plain_config_set_input_pipeline(
			cCfg,
			C.uint64_t(idx),
			cName,
			C.uint64_t(pipeline.Weight),
		)
	}

	for idx, pipeline := range output {
		cName := C.CString(pipeline.Name)
		defer C.free(unsafe.Pointer(cName))
		C.cp_device_plain_config_set_output_pipeline(
			cCfg,
			C.uint64_t(idx),
			cName,
			C.uint64_t(pipeline.Weight),
		)
	}

	ptr := C.cp_device_plain_new((*C.struct_agent)(agent.AsRawPtr()), cCfg, &cErr)
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to create plain device: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}

	return &DeviceConfig{
		ptr: ffi.NewShmDeviceConfig(unsafe.Pointer(ptr)),
	}, nil
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
		rc, errno := C.cp_device_plain_free((*C.struct_cp_device)(ptr), &cErr)
		return int(rc), unsafe.Pointer(cErr), errno
	})
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
		input := make([]Pipeline, 0, len(deviceCfg.Input))
		for _, pipeline := range deviceCfg.Input {
			input = append(input, Pipeline{Name: pipeline.Name, Weight: pipeline.Weight})
		}
		output := make([]Pipeline, 0, len(deviceCfg.Output))
		for _, pipeline := range deviceCfg.Output {
			output = append(output, Pipeline{Name: pipeline.Name, Weight: pipeline.Weight})
		}

		device, err := NewDeviceConfig(agent, deviceCfg.Name, input, output)
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
