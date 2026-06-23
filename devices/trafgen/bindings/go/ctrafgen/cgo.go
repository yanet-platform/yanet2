// Package ctrafgen is the Go binding for the trafgen device.
package ctrafgen

//#cgo CFLAGS: -I../../../../../ -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/devices/trafgen/api -ldev_trafgen_api
//
// #include "api/agent.h"
// #include "devices/trafgen/api/controlplane.h"
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

// DeviceConfig is an opaque handle to the trafgen device configuration in
// shared memory.
type DeviceConfig struct {
	ptr ffi.ShmDeviceConfig
}

// NewDeviceConfig allocates a trafgen device configuration via the C API.
//
// It binds the given input/output pipelines and loads the frames and rate, then
// returns a handle ready to be published with (*ffi.Agent).UpdateDevices.
func NewDeviceConfig(
	agent *ffi.Agent,
	name string,
	input []Pipeline,
	output []Pipeline,
	frames []byte,
	lengths []uint32,
	ratePps uint64,
) (*DeviceConfig, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.struct_yanet_error
	cCfg := C.cp_device_trafgen_config_new(
		cName,
		C.uint64_t(len(input)),
		C.uint64_t(len(output)),
		&cErr,
	)
	if cCfg == nil {
		return nil, fmt.Errorf("failed to initialize trafgen device config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	defer C.cp_device_trafgen_config_free(cCfg)

	for idx := range input {
		pipeline := &input[idx]
		cPipeline := C.CString(pipeline.Name)
		defer C.free(unsafe.Pointer(cPipeline))
		C.cp_device_trafgen_config_set_input_pipeline(
			cCfg,
			C.uint64_t(idx),
			cPipeline,
			C.uint64_t(pipeline.Weight),
		)
	}

	for idx := range output {
		pipeline := &output[idx]
		cPipeline := C.CString(pipeline.Name)
		defer C.free(unsafe.Pointer(cPipeline))
		C.cp_device_trafgen_config_set_output_pipeline(
			cCfg,
			C.uint64_t(idx),
			cPipeline,
			C.uint64_t(pipeline.Weight),
		)
	}

	ptr := C.cp_device_trafgen_new((*C.struct_agent)(agent.AsRawPtr()), cCfg, &cErr)
	if ptr == nil {
		return nil, fmt.Errorf("failed to create trafgen device: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	device := &DeviceConfig{
		ptr: ffi.NewShmDeviceConfig(unsafe.Pointer(ptr)),
	}

	if err := device.setFrames(frames, lengths); err != nil {
		device.Free()
		return nil, err
	}

	if err := device.setRate(ratePps); err != nil {
		device.Free()
		return nil, err
	}

	return device, nil
}

func (m *DeviceConfig) asRawPtr() *C.struct_cp_device {
	return (*C.struct_cp_device)(m.ptr.AsRawPtr())
}

// AsFFIDevice returns the underlying shared-memory device config handle.
func (m *DeviceConfig) AsFFIDevice() ffi.ShmDeviceConfig {
	return m.ptr
}

// Free releases the underlying C memory.
//
// Safe to call multiple times: subsequent calls are no-ops.
func (m *DeviceConfig) Free() {
	if ptr := m.asRawPtr(); ptr != nil {
		C.cp_device_trafgen_free(ptr)
		m.ptr = ffi.ShmDeviceConfig{}
	}
}

// DrainUnusedDevices reclaims trafgen devices retired from the config
// generation and parked on the agent's unused list.
//
// Call it after UpdateDevices, once the retiring generation no longer
// references the parked devices.
func DrainUnusedDevices(agent *ffi.Agent) {
	C.cp_device_trafgen_drain_unused((*C.struct_agent)(agent.AsRawPtr()))
}

// setRate sets the target aggregate packet rate in packets per second.
func (m *DeviceConfig) setRate(ratePps uint64) error {
	if rc := C.cp_device_trafgen_set_rate(
		m.asRawPtr(),
		C.uint64_t(ratePps),
	); rc != 0 {
		return fmt.Errorf("failed to set rate: error code=%d", rc)
	}
	return nil
}

// setFrames replaces the frame set replayed by the generator.
//
// frames is the concatenation of the raw L2 frames whose individual lengths are
// given by lengths. The C side copies the data, so the slices may be reused
// after the call returns.
func (m *DeviceConfig) setFrames(frames []byte, lengths []uint32) error {
	var framesPtr *C.uint8_t
	if len(frames) > 0 {
		framesPtr = (*C.uint8_t)(unsafe.Pointer(&frames[0]))
	}

	var lengthsPtr *C.uint32_t
	if len(lengths) > 0 {
		lengthsPtr = (*C.uint32_t)(unsafe.Pointer(&lengths[0]))
	}

	var cErr *C.struct_yanet_error
	if rc := C.cp_device_trafgen_set_frames(
		m.asRawPtr(),
		framesPtr,
		C.uint64_t(len(frames)),
		lengthsPtr,
		C.uint32_t(len(lengths)),
		&cErr,
	); rc != 0 {
		return fmt.Errorf("failed to set frames: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return nil
}
