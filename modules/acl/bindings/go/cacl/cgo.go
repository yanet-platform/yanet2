package cacl

//#cgo CFLAGS: -I../../../../../
//#cgo LDFLAGS: -L../../../../../build/modules/acl/api -lacl_cp
//#cgo LDFLAGS: -L../../../../../build/lib/filter -lfilter_compiler
//#cgo LDFLAGS: -L../../../../../build/lib/logging -llogging
//#cgo LDFLAGS: -L../../../../../build/objects/fwstate/api -lfwstate_objects
//
//#include "api/agent.h"
//#include "modules/acl/api/controlplane.h"
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
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
// fw4MapName and fw6MapName name standalone fwstate_map_v4 /
// fwstate_map_v6 objects whose fwtables back state lookups; the names
// are declared as object links here and resolve against published
// objects when the config is published. An empty name declares no link,
// and CHECK_STATE then finds no state for that family. Pass nil
// emitConfig for a ruleset that emits no sync packets.
func NewModuleConfig(
	agent *ffi.Agent,
	name string,
	rules []AclRule,
	fw4MapName, fw6MapName string,
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

	var cFw4Name *C.char
	if fw4MapName != "" {
		cFw4Name = C.CString(fw4MapName)
		defer C.free(unsafe.Pointer(cFw4Name))
	}

	var cFw6Name *C.char
	if fw6MapName != "" {
		cFw6Name = C.CString(fw6MapName)
		defer C.free(unsafe.Pointer(cFw6Name))
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
		cFw4Name,
		cFw6Name,
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

// Free destroys the module config when it is dangling — referenced by no live
// configuration generation — and reports nil. While a live generation
// still references it the free is refused with ffi.ErrStillReferenced
// and the handle stays usable: the caller must remember it and free it
// again once the generations holding it drain. Safe to call multiple
// times: subsequent calls are no-ops reporting nil.
func (m *ModuleConfig) Free() error {
	ptr := m.asRawPtr()
	if ptr == nil {
		return nil
	}
	var cErr *C.yanet_error
	rc, errno := C.acl_module_config_free(ptr, &cErr)
	if rc == 0 {
		m.ptr = ffi.ModuleConfig{}
		return nil
	}
	if errors.Is(errno, syscall.EAGAIN) {
		// The refused attempt allocated an error chain; release it
		// rather than leaking one per attempt. The object is intact.
		C.yanet_error_free(cErr)
		return ffi.ErrStillReferenced
	}
	return fmt.Errorf("failed to free module config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
}
