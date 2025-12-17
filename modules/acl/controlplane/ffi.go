package acl

//#cgo CFLAGS: -I../../../build
//#cgo CFLAGS: -I.. -I../../.. -I../../../lib -I../../../common
//#cgo LDFLAGS: -L../../../build/modules/acl/api -lacl_cp
//#cgo LDFLAGS: -L../../../build/filter -lfilter
//#cgo LDFLAGS: -L../../../build/lib/logging -llogging
//
//#include "api/agent.h"
//#include "modules/acl/api/controlplane.h"
//#include "modules/acl/api/fwstate_cp.h"
import "C"

import (
	"fmt"
	"net/netip"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ModuleConfig wraps the C ACL module configuration
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// NewModuleConfig creates a new ACL module configuration
func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	// Create a new module config using the C API
	ptr, err := C.acl_module_config_init((*C.struct_agent)(agent.AsRawPtr()), cName)
	if ptr == nil {
		if err != nil {
			return nil, fmt.Errorf("failed to initialize ACL module config: %w", err)
		}
		return nil, fmt.Errorf("failed to initialize ACL module config: module %q not found", name)
	}

	return &ModuleConfig{
		ptr: ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}, nil
}

func (m *ModuleConfig) Free() {
	C.acl_module_config_free(m.asRawPtr())
}

// asRawPtr returns the raw C pointer
func (m *ModuleConfig) asRawPtr() *C.struct_cp_module {
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

// AsFFIModule returns the FFI module config
func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

type network struct {
	addr netip.Addr
	mask netip.Addr
}

type portRange struct {
	from uint16
	to   uint16
}

type protoRange struct {
	from uint16
	to   uint16
}

type vlanRange struct {
	from uint16
	to   uint16
}

type aclRule struct {
	action        uint64
	counter       string
	devices       []string
	vlanRanges    []vlanRange
	srcs          []network
	dsts          []network
	protoRanges   []protoRange
	srcPortRanges []portRange
	dstPortRanges []portRange
}

func (m *ModuleConfig) Update(rules []aclRule) error {
	deviceCount := 0
	vlanRangeCount := 0

	srcNet4Count := 0
	dstNet4Count := 0

	srcNet6Count := 0
	dstNet6Count := 0

	protoRangeCount := 0

	srcPortRangeCount := 0
	dstPortRangeCount := 0

	for _, rule := range rules {
		for _, src := range rule.srcs {
			if src.addr.Is4() {
				srcNet4Count++
			} else {
				srcNet6Count++
			}
		}

		for _, dst := range rule.dsts {
			if dst.addr.Is4() {
				dstNet4Count++
			} else {
				dstNet6Count++
			}
		}

		deviceCount = deviceCount + len(rule.devices)
		vlanRangeCount = vlanRangeCount + len(rule.vlanRanges)
		protoRangeCount = protoRangeCount + len(rule.protoRanges)
		srcPortRangeCount = srcPortRangeCount + len(rule.srcPortRanges)
		dstPortRangeCount = dstPortRangeCount + len(rule.dstPortRanges)
	}

	cRules := make([]C.struct_acl_rule, 0, len(rules))
	cSrc4Nets := make([]C.struct_net4, 0, srcNet4Count)
	cDst4Nets := make([]C.struct_net4, 0, dstNet4Count)
	cSrc6Nets := make([]C.struct_net6, 0, srcNet6Count)
	cDst6Nets := make([]C.struct_net6, 0, dstNet6Count)
	cDevices := make([]C.struct_filter_device, 0, deviceCount)
	cVlanRanges := make([]C.struct_filter_vlan_range, 0, vlanRangeCount)
	cProtoRanges := make([]C.struct_filter_proto_range, 0, protoRangeCount)
	cSrcPortRanges := make([]C.struct_filter_port_range, 0, srcPortRangeCount)
	cDstPortRanges := make([]C.struct_filter_port_range, 0, dstPortRangeCount)

	for _, rule := range rules {
		for _, src := range rule.srcs {
			addr := src.addr.AsSlice()
			mask := src.mask.AsSlice()
			if src.addr.Is4() {
				net := C.struct_net4{}
				for idx := range addr {
					net.addr[idx] = C.uint8_t(addr[idx])
					net.mask[idx] = C.uint8_t(mask[idx])
				}

				cSrc4Nets = append(cSrc4Nets, net)
			} else {
				net := C.struct_net6{}
				for idx := range addr {
					net.addr[idx] = C.uint8_t(addr[idx])
					net.mask[idx] = C.uint8_t(mask[idx])
				}

				cSrc6Nets = append(cSrc6Nets, net)
			}
		}

		for _, dst := range rule.dsts {
			addr := dst.addr.AsSlice()
			mask := dst.mask.AsSlice()
			if dst.addr.Is4() {
				net := C.struct_net4{}
				for idx := range addr {
					net.addr[idx] = C.uint8_t(addr[idx])
					net.mask[idx] = C.uint8_t(mask[idx])
				}

				cDst4Nets = append(cDst4Nets, net)
			} else {
				net := C.struct_net6{}
				for idx := range addr {
					net.addr[idx] = C.uint8_t(addr[idx])
					net.mask[idx] = C.uint8_t(mask[idx])
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

		for _, protoRange := range rule.protoRanges {
			cProtoRanges = append(
				cProtoRanges,
				C.struct_filter_proto_range{
					from: C.uint16_t(protoRange.from),
					to:   C.uint16_t(protoRange.to),
				},
			)
		}

		for _, srcPortRange := range rule.srcPortRanges {
			cSrcPortRanges = append(
				cSrcPortRanges,
				C.struct_filter_port_range{
					from: C.uint16_t(srcPortRange.from),
					to:   C.uint16_t(srcPortRange.to),
				},
			)
		}

		for _, dstPortRange := range rule.dstPortRanges {
			cDstPortRanges = append(
				cDstPortRanges,
				C.struct_filter_port_range{
					from: C.uint16_t(dstPortRange.from),
					to:   C.uint16_t(dstPortRange.to),
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
	protoRangeIdx := 0
	srcPortRangeIdx := 0
	dstPortRangeIdx := 0

	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	for _, rule := range rules {
		cRule := C.struct_acl_rule{
			action: C.uint64_t(rule.action),

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

			proto_range_count: C.uint16_t(len(rule.protoRanges)),
			proto_ranges:      nil,

			src_port_range_count: C.uint16_t(len(rule.srcPortRanges)),
			src_port_ranges:      nil,

			dst_port_range_count: C.uint16_t(len(rule.dstPortRanges)),
			dst_port_ranges:      nil,
		}

		for _, src := range rule.srcs {
			if src.addr.Is4() {
				cRule.net4.src_count = cRule.net4.src_count + 1
			} else {
				cRule.net6.src_count = cRule.net6.src_count + 1
			}
		}

		for _, dst := range rule.dsts {
			if dst.addr.Is4() {
				cRule.net4.dst_count = cRule.net4.dst_count + 1
			} else {
				cRule.net6.dst_count = cRule.net6.dst_count + 1
			}
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

		if cRule.proto_range_count > 0 {
			pinner.Pin(&cProtoRanges[protoRangeIdx])
			cRule.proto_ranges = &cProtoRanges[protoRangeIdx]
		}
		protoRangeIdx = protoRangeIdx + len(rule.protoRanges)

		if cRule.src_port_range_count > 0 {
			pinner.Pin(&cSrcPortRanges[srcPortRangeIdx])
			cRule.src_port_ranges = &cSrcPortRanges[srcPortRangeIdx]
		}
		srcPortRangeIdx = srcPortRangeIdx + len(rule.srcPortRanges)

		if cRule.dst_port_range_count > 0 {
			pinner.Pin(&cDstPortRanges[dstPortRangeIdx])
			cRule.dst_port_ranges = &cDstPortRanges[dstPortRangeIdx]
		}
		dstPortRangeIdx = dstPortRangeIdx + len(rule.dstPortRanges)

		cRules = append(cRules, cRule)
	}

	cRulesPtr := (*C.struct_acl_rule)(nil)
	if len(cRules) > 0 {
		cRulesPtr = &cRules[0]
	}

	rc, err := C.acl_module_config_update(
		m.asRawPtr(),
		cRulesPtr,
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

func DeleteModule(m *ACLService, configName string) bool {
	cTypeName := C.CString(agentName)
	defer C.free(unsafe.Pointer(cTypeName))

	cConfigName := C.CString(configName)
	defer C.free(unsafe.Pointer(cConfigName))

	result := C.agent_delete_module((*C.struct_agent)(m.agent.AsRawPtr()), cTypeName, cConfigName)
	return result == 0
}

func (m *ModuleConfig) SetFwStateConfig(agent *ffi.Agent, fwstateConfig *FwStateConfig) {
	C.acl_module_config_set_fwstate_config(m.asRawPtr(), fwstateConfig.asCPModule())
}
