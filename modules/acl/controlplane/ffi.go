package acl

//#cgo CFLAGS: -I../../../build
//#cgo CFLAGS: -I.. -I../../.. -I../../../lib -I../../../common
//#cgo LDFLAGS: -L../../../build/modules/acl/api -lacl_cp
//#cgo LDFLAGS: -L../../../build/filter -lfilter_compiler
//#cgo LDFLAGS: -L../../../build/lib/logging -llogging
//
//#include "api/agent.h"
//#include "modules/acl/api/controlplane.h"
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/filter"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
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

type AclRule struct {
	Action        uint64
	Counter       string
	Devices       []filter.Device
	VlanRanges    []filter.VlanRange
	Src4s         []filter.IPNet4
	Dst4s         []filter.IPNet4
	Src6s         []filter.IPNet6
	Dst6s         []filter.IPNet6
	ProtoRanges   []filter.ProtoRange
	SrcPortRanges []filter.PortRange
	DstPortRanges []filter.PortRange
}

func (m *AclRule) CBuild(pinner *runtime.Pinner) C.struct_acl_rule {
	cRule := C.struct_acl_rule{}

	cRule.action = C.uint64_t(m.Action)
	cCounter := C.CString(m.Counter)
	C.strncpy(&cRule.counter[0], cCounter, C.COUNTER_NAME_LEN)
	C.free(unsafe.Pointer(cCounter))

	cRule.devices = *(*C.struct_filter_devices)(unsafe.Pointer(filter.Devices(m.Devices).CBuild(pinner)))
	cRule.vlan_ranges = *(*C.struct_filter_vlan_ranges)(unsafe.Pointer(filter.VlanRanges(m.VlanRanges).CBuild(pinner)))
	cRule.src_net4s = *(*C.struct_filter_net4s)(unsafe.Pointer(filter.IPNet4s(m.Src4s).CBuild(pinner)))
	cRule.dst_net4s = *(*C.struct_filter_net4s)(unsafe.Pointer(filter.IPNet4s(m.Dst4s).CBuild(pinner)))
	cRule.src_net6s = *(*C.struct_filter_net6s)(unsafe.Pointer(filter.IPNet6s(m.Src6s).CBuild(pinner)))
	cRule.dst_net6s = *(*C.struct_filter_net6s)(unsafe.Pointer(filter.IPNet6s(m.Dst6s).CBuild(pinner)))
	cRule.proto_ranges = *(*C.struct_filter_proto_ranges)(unsafe.Pointer(filter.ProtoRanges(m.ProtoRanges).CBuild(pinner)))
	cRule.src_port_ranges = *(*C.struct_filter_port_ranges)(unsafe.Pointer(filter.PortRanges(m.SrcPortRanges).CBuild(pinner)))
	cRule.dst_port_ranges = *(*C.struct_filter_port_ranges)(unsafe.Pointer(filter.PortRanges(m.DstPortRanges).CBuild(pinner)))

	return cRule
}

func (m *ModuleConfig) Update(rules []AclRule) error {
	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	cRules := make([]C.struct_acl_rule, len(rules))
	for idx, rule := range rules {
		cRules[idx] = rule.CBuild(pinner)
	}

	var cRulesPtr *C.struct_acl_rule
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

// TransferFwStateConfig transfers fwstate configuration from old ACL config to new ACL config
func (m *ModuleConfig) TransferFwStateConfig(oldACLConfig *ModuleConfig) {
	C.acl_module_config_transfer_fwstate_config(m.asRawPtr(), oldACLConfig.asRawPtr())
}

// SetFwStateConfig links fwstate configuration to this ACL config
func (m *ModuleConfig) SetFwStateConfig(fwstateConfig *fwstate.FwStateConfig) {
	ffiModule := fwstateConfig.AsFFIModule()
	C.acl_module_config_set_fwstate_config(m.asRawPtr(), (*C.struct_cp_module)(ffiModule.AsRawPtr()))
}
