package forward

//#cgo CFLAGS: -I../../../
//#cgo CFLAGS: -I../../../lib
//#cgo LDFLAGS: -L../../../build/modules/forward/api -lforward_cp
//#cgo LDFLAGS: -L../../../build/filter -lfilter
//
//#include "api/agent.h"
//#include "modules/forward/api/controlplane.h"
import "C"

import (
	"fmt"
	"net/netip"
	"runtime"
	"unsafe"

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

type vlanRange struct {
	from uint16
	to   uint16
}

type forwardMode int

const (
	modeNone forwardMode = 0
	modeIn   forwardMode = 1
	modeOut  forwardMode = 2
)

type forwardRule struct {
	target     string
	mode       forwardMode
	counter    string
	devices    []string
	vlanRanges []vlanRange
	srcs       []netip.Prefix
	dsts       []netip.Prefix
}

// L2ForwardEnable configures a device for L2 forwarding
func (m *ModuleConfig) Update(rules []forwardRule) error {
	deviceCount := 0
	vlanRangeCount := 0

	srcNet4Count := 0
	dstNet4Count := 0

	srcNet6Count := 0
	dstNet6Count := 0

	for _, rule := range rules {
		for _, src := range rule.srcs {
			if src.Addr().Is4() {
				srcNet4Count++
			} else {
				srcNet6Count++
			}
		}

		for _, dst := range rule.dsts {
			if dst.Addr().Is4() {
				dstNet4Count++
			} else {
				dstNet6Count++
			}
		}

		deviceCount = deviceCount + len(rule.devices)
		vlanRangeCount = vlanRangeCount + len(rule.vlanRanges)
	}

	cRules := make([]C.struct_forward_rule, 0, len(rules))
	cSrc4Nets := make([]C.struct_net4, 0, srcNet4Count)
	cDst4Nets := make([]C.struct_net4, 0, dstNet4Count)
	cSrc6Nets := make([]C.struct_net6, 0, srcNet6Count)
	cDst6Nets := make([]C.struct_net6, 0, dstNet6Count)
	cDevices := make([]C.struct_filter_device, 0, deviceCount)
	cVlanRanges := make([]C.struct_filter_vlan_range, 0, vlanRangeCount)

	for _, rule := range rules {
		for _, src := range rule.srcs {
			addr := src.Addr().AsSlice()
			if src.Addr().Is4() {
				net := C.struct_net4{}
				for idx := range addr {
					net.addr[idx] = C.uint8_t(addr[idx])
					if (idx+1)*8 <= src.Bits() {
						net.mask[idx] = 0xff
					} else if idx*8 >= src.Bits() {
						net.mask[idx] = 0
					} else {
						net.mask[idx] = 0xff << (src.Bits() - idx*8)
					}
				}

				cSrc4Nets = append(cSrc4Nets, net)
			} else {
				net := C.struct_net6{}
				for idx := range addr {
					net.addr[idx] = C.uint8_t(addr[idx])
					if (idx+1)*8 <= src.Bits() {
						net.mask[idx] = 0xff
					} else if idx*8 >= src.Bits() {
						net.mask[idx] = 0
					} else {
						net.mask[idx] = 0xff << (src.Bits() - idx*8)
					}
				}

				cSrc6Nets = append(cSrc6Nets, net)
			}
		}

		for _, dst := range rule.dsts {
			addr := dst.Addr().AsSlice()
			if dst.Addr().Is4() {
				net := C.struct_net4{}
				for idx := range addr {
					net.addr[idx] = C.uint8_t(addr[idx])
					if (idx+1)*8 <= dst.Bits() {
						net.mask[idx] = 0xff
					} else if idx*8 >= dst.Bits() {
						net.mask[idx] = 0
					} else {
						net.mask[idx] = 0xff << (dst.Bits() - idx*8)
					}
				}

				cDst4Nets = append(cDst4Nets, net)
			} else {
				net := C.struct_net6{}
				for idx := range addr {
					net.addr[idx] = C.uint8_t(addr[idx])
					if (idx+1)*8 <= dst.Bits() {
						net.mask[idx] = 0xff
					} else if idx*8 >= dst.Bits() {
						net.mask[idx] = 0
					} else {
						net.mask[idx] = 0xff << (dst.Bits() - idx*8)
					}
				}

				cDst6Nets = append(cDst6Nets, net)
			}
		}

		for _, device := range rule.devices {
			cDevice := C.struct_filter_device{
				id: 0,
			}
			cStr := C.CString(device)
			C.strncpy(&cDevice.name[0], cStr, C.ACL_DEVICE_NAME_LEN)
			C.free(unsafe.Pointer(cStr))
			cDevices = append(
				cDevices,
				cDevice,
			)
		}

		for _, vlanRange := range rule.vlanRanges {
			cVlanRanges = append(
				cVlanRanges,
				C.struct_filter_vlan_range{
					from: C.uint16_t(vlanRange.from),
					to:   C.uint16_t(vlanRange.to),
				},
			)
		}
	}

	src4Idx := 0
	dst4Idx := 0
	src6Idx := 0
	dst6Idx := 0
	deviceIdx := 0
	vlanRangeIdx := 0

	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	for _, rule := range rules {
		cRule := C.struct_forward_rule{
			net4: C.struct_filter_net4{
				src_count: 0,
				srcs:      nil,
				dst_count: 0,
				dsts:      nil,
			},

			net6: C.struct_filter_net6{
				src_count: 0,
				srcs:      nil,
				dst_count: 0,
				dsts:      nil,
			},

			device_count: C.uint16_t(len(rule.devices)),
			devices:      nil,

			vlan_range_count: C.uint16_t(len(rule.vlanRanges)),
			vlan_ranges:      nil,
		}

		for _, src := range rule.srcs {
			if src.Addr().Is4() {
				cRule.net4.src_count = cRule.net4.src_count + 1
			} else {
				cRule.net6.src_count = cRule.net6.src_count + 1
			}
		}

		for _, dst := range rule.dsts {
			if dst.Addr().Is4() {
				cRule.net4.dst_count = cRule.net4.dst_count + 1
			} else {
				cRule.net6.dst_count = cRule.net6.dst_count + 1
			}
		}

		cTarget := C.CString(rule.target)
		C.strncpy(&cRule.target[0], cTarget, C.CP_DEVICE_NAME_LEN)
		C.free(unsafe.Pointer(cTarget))

		if rule.mode == modeIn {
			cRule.mode = C.FORWARD_MODE_IN
		} else if rule.mode == modeOut {
			cRule.mode = C.FORWARD_MODE_OUT
		} else {
			cRule.mode = C.FORWARD_MODE_NONE
		}

		cCounter := C.CString(rule.counter)
		C.strncpy(&cRule.counter[0], cCounter, C.COUNTER_NAME_LEN)
		C.free(unsafe.Pointer(cCounter))

		if cRule.net4.src_count > 0 {
			pinner.Pin(&cSrc4Nets[src4Idx])
			cRule.net4.srcs = &cSrc4Nets[src4Idx]
		}
		src4Idx = src4Idx + int(cRule.net4.src_count)

		if cRule.net4.dst_count > 0 {
			pinner.Pin(&cDst4Nets[dst4Idx])
			cRule.net4.dsts = &cDst4Nets[dst4Idx]
		}
		dst4Idx = dst4Idx + int(cRule.net4.dst_count)

		if cRule.net6.src_count > 0 {
			pinner.Pin(&cSrc6Nets[src6Idx])
			cRule.net6.srcs = &cSrc6Nets[src6Idx]
		}
		src6Idx = src6Idx + int(cRule.net6.src_count)

		if cRule.net6.dst_count > 0 {
			pinner.Pin(&cDst6Nets[dst6Idx])
			cRule.net6.dsts = &cDst6Nets[dst6Idx]
		}
		dst6Idx = dst6Idx + int(cRule.net6.dst_count)

		if cRule.device_count > 0 {
			pinner.Pin(&cDevices[deviceIdx])
			cRule.devices = &cDevices[deviceIdx]
		}
		deviceIdx = deviceIdx + len(rule.devices)

		if cRule.vlan_range_count > 0 {
			pinner.Pin(&cVlanRanges[vlanRangeIdx])
			cRule.vlan_ranges = &cVlanRanges[vlanRangeIdx]
		}
		vlanRangeIdx = vlanRangeIdx + len(rule.vlanRanges)

		cRules = append(cRules, cRule)
	}

	rc, err := C.forward_module_config_update(
		m.asRawPtr(),
		&cRules[0],
		C.uint32_t(len(cRules)),
	)
	if err != nil {
		return fmt.Errorf("failed to update config %w", err)
	}
	if rc != 0 {
		return fmt.Errorf("failed to update config with return code=%d", rc)
	}
	return nil
}

func DeleteConfig(m *ForwardService, configName string) bool {
	cTypeName := C.CString(agentName)
	defer C.free(unsafe.Pointer(cTypeName))

	cConfigName := C.CString(configName)
	defer C.free(unsafe.Pointer(cConfigName))

	result := C.agent_delete_module((*C.struct_agent)(m.agent.AsRawPtr()), cTypeName, cConfigName)
	return result == 0
}
