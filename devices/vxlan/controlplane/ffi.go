package vxlan

//#cgo CFLAGS: -I../../../build/lib/rust/cp
//#cgo LDFLAGS: -L../../../build/lib/rust/cp -lyanet_cp -lyanet_cp_shim
//
//#include <stdlib.h>
//#include "yanet_cp.h"
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Tunnel is the IPv4 VXLAN tunnel of a vxlan device.
//
// Addresses are in network byte order, the VNI in host byte order.
type Tunnel struct {
	LocalMAC  [6]byte
	RemoteMAC [6]byte
	LocalIP   [4]byte
	RemoteIP  [4]byte
	VNI       uint32
}

// DeviceConfig wraps a vxlan device created in shared memory.
type DeviceConfig struct {
	ptr ffi.ShmDeviceConfig
}

// CheckABI reports whether the linked Rust control-plane archive speaks the
// ABI version this package was compiled against.
func CheckABI() error {
	if got := uint32(C.yanet_cp_abi_version()); got != C.YANET_CP_ABI_VERSION {
		return fmt.Errorf(
			"rust control plane ABI version is %d, this build needs %d",
			got,
			C.YANET_CP_ABI_VERSION,
		)
	}

	return nil
}

// NewDeviceConfig creates a vxlan device in the agent's memory.
//
// The device is dangling until it is published through the agent and must
// be freed when publishing fails. A dataplane without the vxlan device type
// reports a failed precondition.
func NewDeviceConfig(
	agent *ffi.Agent,
	name string,
	device *commonpb.Device,
	tunnel Tunnel,
) (
	*DeviceConfig,
	error,
) {
	if agent == nil {
		return nil, errors.New("agent cannot be nil")
	}

	var strs cStrings
	defer strs.release()

	var pinner runtime.Pinner
	defer pinner.Unpin()

	cName, err := strs.add("device name", name)
	if err != nil {
		return nil, err
	}
	input, err := newPipelines(&strs, &pinner, "input", device.GetInput())
	if err != nil {
		return nil, err
	}
	output, err := newPipelines(&strs, &pinner, "output", device.GetOutput())
	if err != nil {
		return nil, err
	}

	request := C.struct_yanet_cp_vxlan_device_request{
		name:       cName,
		tunnel:     tunnelToC(tunnel),
		input:      input,
		input_len:  C.size_t(len(device.GetInput())),
		output:     output,
		output_len: C.size_t(len(device.GetOutput())),
	}

	var cDevice unsafe.Pointer
	var errBuf [C.YANET_CP_ERROR_LEN]C.char
	rc := C.yanet_cp_vxlan_device_new(
		agent.AsRawPtr(),
		&request,
		&cDevice,
		&errBuf[0],
		C.size_t(len(errBuf)),
	)
	if err := statusError(rc, &errBuf[0]); err != nil {
		return nil, fmt.Errorf("failed to create vxlan device: %w", err)
	}
	if cDevice == nil {
		return nil, errors.New("failed to create vxlan device: no device returned")
	}

	return &DeviceConfig{ptr: ffi.NewShmDeviceConfig(cDevice)}, nil
}

// LookupTunnel returns the tunnel of the live vxlan device with the given
// name.
//
// A name with no vxlan device reports a not-found error.
func LookupTunnel(agent *ffi.Agent, name string) (Tunnel, error) {
	if agent == nil {
		return Tunnel{}, errors.New("agent cannot be nil")
	}

	var strs cStrings
	defer strs.release()

	cName, err := strs.add("device name", name)
	if err != nil {
		return Tunnel{}, err
	}

	var cTunnel C.struct_yanet_cp_vxlan_tunnel
	var errBuf [C.YANET_CP_ERROR_LEN]C.char
	rc := C.yanet_cp_vxlan_device_show(
		agent.AsRawPtr(),
		cName,
		&cTunnel,
		&errBuf[0],
		C.size_t(len(errBuf)),
	)
	if err := statusError(rc, &errBuf[0]); err != nil {
		return Tunnel{}, fmt.Errorf("failed to look up vxlan device: %w", err)
	}

	return tunnelFromC(&cTunnel), nil
}

// AsFFIDevice returns the device as an FFI device configuration.
func (m *DeviceConfig) AsFFIDevice() ffi.ShmDeviceConfig {
	return m.ptr
}

// Free destroys the device, or reports that it is still referenced while a
// live generation holds it. Safe to call multiple times.
func (m *DeviceConfig) Free() error {
	return m.ptr.Free(func(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
		var errBuf [C.YANET_CP_ERROR_LEN]C.char
		rc := C.yanet_cp_vxlan_device_free(ptr, &errBuf[0], C.size_t(len(errBuf)))
		if rc == C.YANET_CP_STILL_REFERENCED {
			return int(rc), nil, syscall.EAGAIN
		}

		return int(rc), nil, statusError(rc, &errBuf[0])
	})
}

// cStrings owns the C copies of Go strings handed to one C call.
type cStrings struct {
	ptrs []*C.char
}

// add copies the string into C memory that lives until the owner releases
// its strings.
//
// A string with a NUL byte is refused rather than silently truncated by C.
func (m *cStrings) add(field, value string) (*C.char, error) {
	if strings.IndexByte(value, 0) >= 0 {
		return nil, &statusFault{kind: ffi.ErrInvalidArgument, message: field + " must not contain NUL"}
	}

	ptr := C.CString(value)
	m.ptrs = append(m.ptrs, ptr)

	return ptr, nil
}

func (m *cStrings) release() {
	for _, ptr := range m.ptrs {
		C.free(unsafe.Pointer(ptr))
	}
	m.ptrs = nil
}

// newPipelines converts device bindings into a C array, nil when empty.
//
// The array is Go memory holding C string pointers; it is pinned so the
// request that references it may travel to C, and stays pinned until the
// caller unpins it.
func newPipelines(
	strs *cStrings,
	pinner *runtime.Pinner,
	field string,
	pipelines []*commonpb.DevicePipeline,
) (*C.struct_yanet_cp_pipeline, error) {
	if len(pipelines) == 0 {
		return nil, nil
	}

	bindings := make([]C.struct_yanet_cp_pipeline, len(pipelines))
	for idx, pipeline := range pipelines {
		name, err := strs.add(fmt.Sprintf("%s[%d].name", field, idx), pipeline.GetName())
		if err != nil {
			return nil, err
		}
		bindings[idx] = C.struct_yanet_cp_pipeline{
			name:   name,
			weight: C.uint64_t(pipeline.GetWeight()),
		}
	}
	pinner.Pin(&bindings[0])

	return &bindings[0], nil
}

func tunnelToC(tunnel Tunnel) C.struct_yanet_cp_vxlan_tunnel {
	var cTunnel C.struct_yanet_cp_vxlan_tunnel
	for idx, octet := range tunnel.LocalMAC {
		cTunnel.local_mac[idx] = C.uint8_t(octet)
	}
	for idx, octet := range tunnel.RemoteMAC {
		cTunnel.remote_mac[idx] = C.uint8_t(octet)
	}
	for idx, octet := range tunnel.LocalIP {
		cTunnel.local_ip[idx] = C.uint8_t(octet)
	}
	for idx, octet := range tunnel.RemoteIP {
		cTunnel.remote_ip[idx] = C.uint8_t(octet)
	}
	cTunnel.vni = C.uint32_t(tunnel.VNI)

	return cTunnel
}

func tunnelFromC(cTunnel *C.struct_yanet_cp_vxlan_tunnel) Tunnel {
	var tunnel Tunnel
	for idx := range tunnel.LocalMAC {
		tunnel.LocalMAC[idx] = byte(cTunnel.local_mac[idx])
	}
	for idx := range tunnel.RemoteMAC {
		tunnel.RemoteMAC[idx] = byte(cTunnel.remote_mac[idx])
	}
	for idx := range tunnel.LocalIP {
		tunnel.LocalIP[idx] = byte(cTunnel.local_ip[idx])
	}
	for idx := range tunnel.RemoteIP {
		tunnel.RemoteIP[idx] = byte(cTunnel.remote_ip[idx])
	}
	tunnel.VNI = uint32(cTunnel.vni)

	return tunnel
}

// statusFault is a failure reported by the Rust control plane.
//
// The kind is the error category the failure belongs to, nil for a failure
// no caller can act on.
type statusFault struct {
	kind    error
	message string
}

func (m *statusFault) Error() string {
	return m.message
}

func (m *statusFault) Unwrap() error {
	return m.kind
}

// statusError converts a status code and the zero-terminated message the
// Rust control plane wrote into the error buffer.
//
// The kinds mirror the status codes: invalid argument, not found, still
// referenced. A device creation refused because the dataplane lacks the
// vxlan device type, or has one loaded for another configuration layout,
// is a failed precondition; every other refusal and a caught panic carry
// no kind.
func statusError(rc C.int32_t, message *C.char) error {
	if rc == C.YANET_CP_OK {
		return nil
	}

	fault := &statusFault{message: C.GoString(message)}
	if fault.message == "" {
		fault.message = fmt.Sprintf("status %d without a message", int(rc))
	}
	switch rc {
	case C.YANET_CP_INVALID_ARGUMENT:
		fault.kind = ffi.ErrInvalidArgument
	case C.YANET_CP_NOT_FOUND:
		fault.kind = ffi.ErrNotFound
	case C.YANET_CP_STILL_REFERENCED:
		fault.kind = ffi.ErrBusy
	case C.YANET_CP_FAILED_PRECONDITION:
		fault.kind = ffi.ErrFailedPrecondition
	case C.YANET_CP_FAILED:
	case C.YANET_CP_PANIC:
	default:
		fault.message = fmt.Sprintf("unknown status %d: %s", int(rc), fault.message)
	}

	return fault
}
