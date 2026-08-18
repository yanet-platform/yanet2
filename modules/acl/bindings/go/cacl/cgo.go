package cacl

//#cgo CFLAGS: -I../../../../../
//#cgo LDFLAGS: -L../../../../../build/modules/acl/api -lacl_cp
//#cgo LDFLAGS: -L../../../../../build/lib/filter -lfilter_compiler
//#cgo LDFLAGS: -L../../../../../build/lib/logging -llogging
//
//#include "api/agent.h"
//#include "modules/acl/api/controlplane.h"
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	cfwstate "github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

// ModuleConfig is an opaque handle to the ACL module configuration in
// shared memory.
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// NewModuleConfig allocates a new ACL module configuration and compiles
// the given rules into it in one step: a config handle is constructed
// once and never updated afterwards.
//
// Pass nil emitConfig for a ruleset that emits no sync packets.
func NewModuleConfig(
	agent *ffi.Agent,
	name string,
	rules []AclRule,
	emitConfig *cfwstate.SyncEmitConfig,
) (*ModuleConfig, error) {
	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	cRules := make([]C.struct_acl_rule, len(rules))
	for idx, rule := range rules {
		cRules[idx] = rule.cBuild(pinner)
	}

	var cRulesPtr *C.struct_acl_rule
	if len(cRules) > 0 {
		cRulesPtr = &cRules[0]
	}

	var cEmitPtr unsafe.Pointer
	if emitConfig != nil {
		cEmitPtr = cfwstate.NewCEmitSyncConfig(*emitConfig)
		if cEmitPtr == nil {
			return nil, errors.New("failed to allocate ACL sync emission config")
		}
		defer cfwstate.FreeCEmitSyncConfig(cEmitPtr)
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.acl_module_config_init(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		cRulesPtr,
		C.uint32_t(len(cRules)),
		(*C.struct_fwstate_sync_emit_config)(cEmitPtr),
		&cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to initialize module config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return &ModuleConfig{
		ptr: ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}, nil
}

func (m *ModuleConfig) asRawPtr() *C.struct_cp_module {
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

// AsFFIModule returns the underlying common module config handle.
func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

// Free releases the underlying C memory.
//
// Safe to call multiple times: subsequent calls are no-ops.
func (m *ModuleConfig) Free() {
	if ptr := m.asRawPtr(); ptr != nil {
		C.acl_module_config_free(ptr)
		m.ptr = ffi.ModuleConfig{}
	}
}
