package vxlan

//#cgo CFLAGS: -I../../../ -I../../../lib
//#cgo LDFLAGS: -L../../../build/devices/vxlan/api -ldev_vxlan_api
//#cgo LDFLAGS: -L../../../build/lib/logging/ -llogging
//
//#include "api/agent.h"
//#include "api/config.h"
//#include "devices/vxlan/api/controlplane.h"
import "C"

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// DeviceNameMaxLength is the longest device name the C configuration can
// carry: cp_device_config_init truncates into a CP_DEVICE_NAME_LEN buffer,
// which would silently publish a differently named dataplane device.
const DeviceNameMaxLength = int(C.CP_DEVICE_NAME_LEN) - 1

// Settings are the tunnel parameters copied verbatim into the shared-memory
// configuration.
//
// VNI and DstPort carry host-order values, the addresses are IPv4, and the
// MACs hold raw wire bytes, matching struct cp_device_vxlan_settings.
type Settings struct {
	VNI     uint32
	DstPort uint16
	SrcMAC  [6]byte
	DstMAC  [6]byte
	SrcIP   netip.Addr
	DstIP   netip.Addr
}

// validate checks the tunnel parameters against what the C settings struct
// and the dataplane can carry.
func (m Settings) validate() error {
	if m.VNI > 0xFFFFFF {
		return fmt.Errorf("vni %d exceeds the 24-bit limit", m.VNI)
	}
	if m.DstPort == 0 {
		return fmt.Errorf("destination port must not be zero")
	}
	if !m.SrcIP.IsValid() || !m.SrcIP.Is4() {
		return fmt.Errorf("source ip %q must be an IPv4 address", m.SrcIP)
	}
	if !m.DstIP.IsValid() || !m.DstIP.Is4() {
		return fmt.Errorf("destination ip %q must be an IPv4 address", m.DstIP)
	}
	return nil
}

// toC builds the C settings struct and pins it for the duration of the call.
func (m Settings) toC(pinner *runtime.Pinner) (*C.struct_cp_device_vxlan_settings, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}

	// The C struct carries the addresses as network-order uint32 values;
	// reading the four wire-order address bytes as a native uint32 yields
	// exactly that representation on both endiannesses.
	srcIPBytes := m.SrcIP.As4()
	dstIPBytes := m.DstIP.As4()
	settings := &C.struct_cp_device_vxlan_settings{
		vni:      C.uint32_t(m.VNI),
		dst_port: C.uint16_t(m.DstPort),
		src_ip:   C.uint32_t(binary.NativeEndian.Uint32(srcIPBytes[:])),
		dst_ip:   C.uint32_t(binary.NativeEndian.Uint32(dstIPBytes[:])),
	}
	for idx := range m.SrcMAC {
		settings.src_mac[idx] = C.uint8_t(m.SrcMAC[idx])
		settings.dst_mac[idx] = C.uint8_t(m.DstMAC[idx])
	}
	pinner.Pin(settings)

	return settings, nil
}

// DeviceConfig wraps C module configuration
type DeviceConfig struct {
	ptr ffi.ShmDeviceConfig
}

// NewDeviceConfig creates a new vxlan device configuration
func NewDeviceConfig(
	agent *ffi.Agent,
	name string,
	device *commonpb.Device,
	settings Settings,
) (
	*DeviceConfig,
	error,
) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	cSettings, err := settings.toC(&pinner)
	if err != nil {
		return nil, fmt.Errorf("invalid tunnel settings: %w", err)
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	input := device.GetInput()
	output := device.GetOutput()

	var cErr *C.struct_yanet_error
	cCfg := C.cp_device_vxlan_config_new(
		cName,
		C.uint64_t(len(input)),
		C.uint64_t(len(output)),
		cSettings,
		&cErr,
	)
	if cCfg == nil {
		return nil, fmt.Errorf("failed to initialize vxlan device config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	defer C.cp_device_vxlan_config_free(cCfg)

	for idx, pipeline := range input {
		cName := C.CString(pipeline.GetName())
		defer C.free(unsafe.Pointer(cName))
		if rc := C.cp_device_vxlan_config_set_input_pipeline(
			cCfg,
			C.uint64_t(idx),
			cName,
			C.uint64_t(pipeline.GetWeight()),
		); rc != 0 {
			return nil, fmt.Errorf("failed to set input pipeline %q at index %d", pipeline.GetName(), idx)
		}
	}

	for idx, pipeline := range output {
		cName := C.CString(pipeline.GetName())
		defer C.free(unsafe.Pointer(cName))
		if rc := C.cp_device_vxlan_config_set_output_pipeline(
			cCfg,
			C.uint64_t(idx),
			cName,
			C.uint64_t(pipeline.GetWeight()),
		); rc != 0 {
			return nil, fmt.Errorf("failed to set output pipeline %q at index %d", pipeline.GetName(), idx)
		}
	}

	ptr := C.cp_device_vxlan_new((*C.struct_agent)(agent.AsRawPtr()), cCfg, &cErr)
	if ptr == nil {
		return nil, fmt.Errorf("failed to create vxlan device: %w", cerrors.FromC(unsafe.Pointer(cErr)))
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

// Free drops the underlying device's construction reference.
//
// Safe to call multiple times: subsequent calls are no-ops. When this is the
// last reference, the device parks on the agent until the next vxlan-device
// construction reclaims it.
func (m *DeviceConfig) Free() {
	if ptr := m.asRawPtr(); ptr != nil {
		C.cp_device_vxlan_free(ptr)
		m.ptr = ffi.ShmDeviceConfig{}
	}
}
