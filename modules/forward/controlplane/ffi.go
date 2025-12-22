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
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/filter"
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

func FreeModuleConfig(module *ModuleConfig) {
	C.forward_module_config_free(module.asRawPtr())
}

func (m *ModuleConfig) asRawPtr() *C.struct_cp_module {
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
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
	devices    []filter.Device
	vlanRanges []filter.VlanRange
	src4s      []filter.IPNet4
	dst4s      []filter.IPNet4
	src6s      []filter.IPNet6
	dst6s      []filter.IPNet6
}

// L2ForwardEnable configures a device for L2 forwarding
func (m *ModuleConfig) Update(rules []forwardRule) error {
	cRules := make([]C.struct_forward_rule, len(rules))

	for idx, rule := range rules {
		cTarget := C.CString(rule.target)
		C.strncpy(&cRules[idx].target[0], cTarget, C.CP_DEVICE_NAME_LEN)
		C.free(unsafe.Pointer(cTarget))

		if rule.mode == modeIn {
			cRules[idx].mode = C.FORWARD_MODE_IN
		} else if rule.mode == modeOut {
			cRules[idx].mode = C.FORWARD_MODE_OUT
		} else {
			cRules[idx].mode = C.FORWARD_MODE_NONE
		}

		cCounter := C.CString(rule.counter)
		C.strncpy(&cRules[idx].counter[0], cCounter, C.COUNTER_NAME_LEN)
		C.free(unsafe.Pointer(cCounter))
	}

	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	filter.SetupFilterAttr(
		len(rules),
		func(attrIdx int, itemIdx int) filter.IAttrItem[filter.CDevice] {
			rule := rules[attrIdx]
			if itemIdx >= len(rule.devices) {
				return nil
			}

			return &rule.devices[itemIdx]
		},
		func(attrIdx int) filter.ICAttr[filter.CDevice] {
			return (*filter.CDevices)(unsafe.Pointer(&cRules[attrIdx].devices))
		},
		&pinner,
	)

	filter.SetupFilterAttr(
		len(rules),
		func(attrIdx int, itemIdx int) filter.IAttrItem[filter.CVlanRange] {
			rule := rules[attrIdx]
			if itemIdx >= len(rule.vlanRanges) {
				return nil
			}

			return &rule.vlanRanges[itemIdx]
		},
		func(attrIdx int) filter.ICAttr[filter.CVlanRange] {
			return (*filter.CVlanRanges)(unsafe.Pointer(&cRules[attrIdx].vlan_ranges))
		},
		&pinner,
	)

	filter.SetupFilterAttr(
		len(rules),
		func(attrIdx int, itemIdx int) filter.IAttrItem[filter.CNet4] {
			rule := rules[attrIdx]
			if itemIdx >= len(rule.src4s) {
				return nil
			}

			return &rule.src4s[itemIdx]
		},
		func(attrIdx int) filter.ICAttr[filter.CNet4] {
			return (*filter.CNet4s)(unsafe.Pointer(&cRules[attrIdx].src_net4s))
		},
		&pinner,
	)

	filter.SetupFilterAttr(
		len(rules),
		func(attrIdx int, itemIdx int) filter.IAttrItem[filter.CNet4] {
			rule := rules[attrIdx]
			if itemIdx >= len(rule.dst4s) {
				return nil
			}

			return &rule.dst4s[itemIdx]
		},
		func(attrIdx int) filter.ICAttr[filter.CNet4] {
			return (*filter.CNet4s)(unsafe.Pointer(&cRules[attrIdx].dst_net4s))
		},
		&pinner,
	)

	filter.SetupFilterAttr(
		len(rules),
		func(attrIdx int, itemIdx int) filter.IAttrItem[filter.CNet6] {
			rule := rules[attrIdx]
			if itemIdx >= len(rule.src6s) {
				return nil
			}

			return &rule.src6s[itemIdx]
		},
		func(attrIdx int) filter.ICAttr[filter.CNet6] {
			return (*filter.CNet6s)(unsafe.Pointer(&cRules[attrIdx].src_net6s))
		},
		&pinner,
	)

	filter.SetupFilterAttr(
		len(rules),
		func(attrIdx int, itemIdx int) filter.IAttrItem[filter.CNet6] {
			rule := rules[attrIdx]
			if itemIdx >= len(rule.dst6s) {
				return nil
			}

			return &rule.dst6s[itemIdx]
		},
		func(attrIdx int) filter.ICAttr[filter.CNet6] {
			return (*filter.CNet6s)(unsafe.Pointer(&cRules[attrIdx].dst_net6s))
		},
		&pinner,
	)

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
