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

func (m *forwardRule) CBuild(pinner *runtime.Pinner) C.struct_forward_rule {
	cRule := C.struct_forward_rule{}

	cTarget := C.CString(m.target)
	C.strncpy(&cRule.target[0], cTarget, C.CP_DEVICE_NAME_LEN)
	C.free(unsafe.Pointer(cTarget))

	switch m.mode {
	case modeIn:
		cRule.mode = C.FORWARD_MODE_IN
	case modeOut:
		cRule.mode = C.FORWARD_MODE_OUT
	default:
		cRule.mode = C.FORWARD_MODE_NONE
	}

	cCounter := C.CString(m.counter)
	C.strncpy(&cRule.counter[0], cCounter, C.COUNTER_NAME_LEN)
	C.free(unsafe.Pointer(cCounter))

	cRule.devices = *(*C.struct_filter_devices)(unsafe.Pointer(filter.Devices(m.devices).CBuild(pinner)))
	cRule.vlan_ranges = *(*C.struct_filter_vlan_ranges)(unsafe.Pointer(filter.VlanRanges(m.vlanRanges).CBuild(pinner)))
	cRule.src_net4s = *(*C.struct_filter_net4s)(unsafe.Pointer(filter.IPNet4s(m.src4s).CBuild(pinner)))
	cRule.dst_net4s = *(*C.struct_filter_net4s)(unsafe.Pointer(filter.IPNet4s(m.dst4s).CBuild(pinner)))
	cRule.src_net6s = *(*C.struct_filter_net6s)(unsafe.Pointer(filter.IPNet6s(m.src6s).CBuild(pinner)))
	cRule.dst_net6s = *(*C.struct_filter_net6s)(unsafe.Pointer(filter.IPNet6s(m.dst6s).CBuild(pinner)))

	return cRule
}

// L2ForwardEnable configures a device for L2 forwarding
func (m *ModuleConfig) Update(rules []forwardRule) error {
	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	cRules := make([]C.struct_forward_rule, len(rules))
	for idx, rule := range rules {
		cRules[idx] = rule.CBuild(pinner)
	}

	var cRulesPtr *C.struct_forward_rule
	if len(cRules) > 0 {
		cRulesPtr = &cRules[0]
	}

	rc, err := C.forward_module_config_update(
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

func DeleteConfig(m *ForwardService, configName string) bool {
	cTypeName := C.CString(agentName)
	defer C.free(unsafe.Pointer(cTypeName))

	cConfigName := C.CString(configName)
	defer C.free(unsafe.Pointer(cConfigName))

	result := C.agent_delete_module((*C.struct_agent)(m.agent.AsRawPtr()), cTypeName, cConfigName)
	return result == 0
}
