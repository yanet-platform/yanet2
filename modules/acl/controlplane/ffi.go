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
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/filter"

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

type aclRule struct {
	action        uint64
	counter       string
	devices       []filter.Device
	vlanRanges    []filter.VlanRange
	src4s         []filter.IPNet4
	dst4s         []filter.IPNet4
	src6s         []filter.IPNet6
	dst6s         []filter.IPNet6
	protoRanges   []filter.ProtoRange
	srcPortRanges []filter.PortRange
	dstPortRanges []filter.PortRange
}

func (m *ModuleConfig) Update(rules []aclRule) error {
	cRules := make([]C.struct_acl_rule, len(rules))

	for idx, rule := range rules {
		// Use defined constant
		cRules[idx].action = C.uint64_t(rule.action)
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

	filter.SetupFilterAttr(
		len(rules),
		func(attrIdx int, itemIdx int) filter.IAttrItem[filter.CProtoRange] {
			rule := rules[attrIdx]
			if itemIdx >= len(rule.protoRanges) {
				return nil
			}

			return &rule.protoRanges[itemIdx]
		},
		func(attrIdx int) filter.ICAttr[filter.CProtoRange] {
			return (*filter.CProtoRanges)(unsafe.Pointer(&cRules[attrIdx].proto_ranges))
		},
		&pinner,
	)

	filter.SetupFilterAttr(
		len(rules),
		func(attrIdx int, itemIdx int) filter.IAttrItem[filter.CPortRange] {
			rule := rules[attrIdx]
			if itemIdx >= len(rule.srcPortRanges) {
				return nil
			}

			return &rule.srcPortRanges[itemIdx]
		},
		func(attrIdx int) filter.ICAttr[filter.CPortRange] {
			return (*filter.CPortRanges)(unsafe.Pointer(&cRules[attrIdx].src_port_ranges))
		},
		&pinner,
	)

	filter.SetupFilterAttr(
		len(rules),
		func(attrIdx int, itemIdx int) filter.IAttrItem[filter.CPortRange] {
			rule := rules[attrIdx]
			if itemIdx >= len(rule.dstPortRanges) {
				return nil
			}

			return &rule.dstPortRanges[itemIdx]
		},
		func(attrIdx int) filter.ICAttr[filter.CPortRange] {
			return (*filter.CPortRanges)(unsafe.Pointer(&cRules[attrIdx].dst_port_ranges))
		},
		&pinner,
	)

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

func (m *ModuleConfig) SetFwStateConfig(agent *ffi.Agent, fwstateConfig *FwStateConfig) {
	C.acl_module_config_set_fwstate_config(m.asRawPtr(), fwstateConfig.asCPModule())
}
