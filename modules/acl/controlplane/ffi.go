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

	"github.com/yanet-platform/yanet2/common/go/filter/device"
	"github.com/yanet-platform/yanet2/common/go/filter/ipnet4"
	"github.com/yanet-platform/yanet2/common/go/filter/ipnet6"
	"github.com/yanet-platform/yanet2/common/go/filter/portrange"
	"github.com/yanet-platform/yanet2/common/go/filter/protorange"
	"github.com/yanet-platform/yanet2/common/go/filter/vlanrange"

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
	Devices       device.Devices
	VlanRanges    vlanrange.VlanRanges
	Src4s         ipnet4.IPNets
	Dst4s         ipnet4.IPNets
	Src6s         ipnet6.IPNets
	Dst6s         ipnet6.IPNets
	ProtoRanges   protorange.ProtoRanges
	SrcPortRanges portrange.PortRanges
	DstPortRanges portrange.PortRanges
}

func (m *AclRule) CBuild(pinner *runtime.Pinner) C.struct_acl_rule {
	cRule := C.struct_acl_rule{}

	cRule.action = C.uint64_t(m.Action)
	cCounter := C.CString(m.Counter)
	C.strncpy(&cRule.counter[0], cCounter, C.COUNTER_NAME_LEN)
	C.free(unsafe.Pointer(cCounter))

	device.CBuilds(&cRule.devices, m.Devices, pinner)
	vlanrange.CBuilds(&cRule.vlan_ranges, m.VlanRanges, pinner)
	ipnet4.CBuilds(&cRule.src_net4s, m.Src4s, pinner)
	ipnet4.CBuilds(&cRule.dst_net4s, m.Dst4s, pinner)
	ipnet6.CBuilds(&cRule.src_net6s, m.Src6s, pinner)
	ipnet6.CBuilds(&cRule.dst_net6s, m.Dst6s, pinner)
	protorange.CBuilds(&cRule.proto_ranges, m.ProtoRanges, pinner)
	portrange.CBuilds(&cRule.src_port_ranges, m.SrcPortRanges, pinner)
	portrange.CBuilds(&cRule.dst_port_ranges, m.DstPortRanges, pinner)

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
