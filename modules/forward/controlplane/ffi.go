package forward

//#cgo CFLAGS: -I../../../
//#cgo LDFLAGS: -L../../../build/modules/forward/api -lforward_cp
//
//#include "api/agent.h"
//#include "modules/forward/api/controlplane.h"
import "C"

import (
	"fmt"
	"net/netip"
	"unsafe"

	dataplane "github.com/yanet-platform/yanet2/common/go/dataplane"
	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.forward_module_config_init((*C.struct_agent)(agent.AsRawPtr()), cName)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize module config: %w", err)
	}
	if ptr == nil {
		return nil, fmt.Errorf("failed to initialize module config: module %q not found", name)
	}

	return &ModuleConfig{
		ptr: ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}, nil
}

func (m *ModuleConfig) asRawPtr() *C.struct_cp_module {
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

// L2ForwardEnable configures a device for L2 forwarding
func (m *ModuleConfig) L2ForwardEnable(srcDeviceID DeviceID, dstDeviceID DeviceID) error {
	cname := C.CString("l2")
	defer C.free(unsafe.Pointer(cname))
	rc, err := C.forward_module_config_enable_l2(
		m.asRawPtr(),
		C.uint16_t(srcDeviceID),
		C.uint16_t(dstDeviceID),
		cname,
	)
	if err != nil {
		return fmt.Errorf("failed to enable device %d: %w", dstDeviceID, err)
	}
	if rc != 0 {
		return fmt.Errorf("failed to enable device %d: return code=%d", dstDeviceID, rc)
	}
	return nil
}

// ForwardEnable configures forwarding for a specified IP prefix from a source device to a target device.
// The prefix can be either IPv4 or IPv6.
func (m *ModuleConfig) L3ForwardEnable(prefix netip.Prefix, srcDeviceID DeviceID, dstDeviceID DeviceID) error {
	addrStart := prefix.Addr()
	addrEnd := xnetip.LastAddr(prefix)

	if addrStart.Is4() {
		start := addrStart.As4()
		end := addrEnd.As4()
		return m.forwardEnableV4(start, end, srcDeviceID, dstDeviceID)
	}

	if addrStart.Is6() {
		start := addrStart.As16()
		end := addrEnd.As16()
		return m.forwardEnableV6(start, end, srcDeviceID, dstDeviceID)
	}

	return fmt.Errorf("unsupported prefix: %s must be either IPv4 or IPv6", prefix)
}

func (m *ModuleConfig) forwardEnableV4(addrStart [4]byte, addrEnd [4]byte, srcDeviceID DeviceID, dstDeviceID DeviceID) error {
	cname := C.CString("v4")
	defer C.free(unsafe.Pointer(cname))
	rc, err := C.forward_module_config_enable_v4(
		m.asRawPtr(),
		(*C.uint8_t)(&addrStart[0]),
		(*C.uint8_t)(&addrEnd[0]),
		C.uint16_t(srcDeviceID),
		C.uint16_t(dstDeviceID),
		cname,
	)
	if err != nil {
		return fmt.Errorf("failed to enable v4 forward from device %d to %d: %w", srcDeviceID, dstDeviceID, err)
	}
	if rc != 0 {
		return fmt.Errorf("failed to enable v4 forward from device %d to %d: return code=%d", srcDeviceID, dstDeviceID, rc)
	}
	return nil
}

func (m *ModuleConfig) forwardEnableV6(addrStart [16]byte, addrEnd [16]byte, srcDeviceID DeviceID, dstDeviceID DeviceID) error {
	cname := C.CString("v6")
	defer C.free(unsafe.Pointer(cname))
	rc, err := C.forward_module_config_enable_v6(
		m.asRawPtr(),
		(*C.uint8_t)(&addrStart[0]),
		(*C.uint8_t)(&addrEnd[0]),
		C.uint16_t(srcDeviceID),
		C.uint16_t(dstDeviceID),
		cname,
	)
	if err != nil {
		return fmt.Errorf("failed to enable v6 forward from device %d to %d: %w", srcDeviceID, dstDeviceID, err)
	}
	if rc != 0 {
		return fmt.Errorf("failed to enable v6 forward from device %d to %d: return code=%d", srcDeviceID, dstDeviceID, rc)
	}
	return nil
}

func topologyDeviceCount(agent *ffi.Agent) uint64 {
	return uint64(C.forward_module_topology_device_count((*C.struct_agent)(agent.AsRawPtr())))
}

func DeleteModule(m *ForwardService, instanceMap dataplane.DpInstanceMap, moduleName string) dataplane.DpInstanceMap {
	cTypeName := C.CString(agentName)
	defer C.free(unsafe.Pointer(cTypeName))

	cModuleName := C.CString(moduleName)
	defer C.free(unsafe.Pointer(cModuleName))

	deleted := dataplane.DpInstanceMap(0)
	for inst := range instanceMap.Iter() {
		if inst >= uint32(len(m.agents)) {
			break
		}
		agent := m.agents[inst]
		result := C.agent_delete_module((*C.struct_agent)(agent.AsRawPtr()), cTypeName, cModuleName)
		if result == 0 {
			deleted.Enable(inst)
		}
	}

	return deleted
}
