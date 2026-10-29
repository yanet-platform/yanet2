package cacl

//#cgo CFLAGS: -I../../../../../
//
//#include "api/agent.h"
//#include "modules/acl/api/controlplane.h"
//#include "modules/acl/dataplane/config.h"
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// MaxActions is the capacity of the C per-rule action storage.
const MaxActions = C.ACL_MAX_ACTIONS

// ModuleConfig is an opaque handle to the ACL module configuration in
// shared memory.
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// Rules is a ruleset in the C layout the module config compiles, with the Go
// memory it points into pinned until it is freed.
type Rules struct {
	rules  []C.struct_acl_rule
	pinner runtime.Pinner
}

// NewRules lays the rules out as the C compile reads them.
//
// The compile writes the device indices back into the layout, so one layout
// may serve any number of configs, one compile at a time.
func NewRules(rules []ACLRule) *Rules {
	prepared := &Rules{rules: make([]C.struct_acl_rule, len(rules))}
	for idx, rule := range rules {
		prepared.rules[idx] = rule.cBuild(&prepared.pinner)
	}
	return prepared
}

// Free unpins the Go memory of the layout; no config may be compiled from it
// afterwards.
func (m *Rules) Free() {
	m.pinner.Unpin()
}

// NewModuleConfig allocates a new ACL module configuration and compiles
// the given rules into it in one step: a config handle is constructed
// once and never updated afterwards.
//
// fw4MapName and fw6MapName name standalone fwstate_map_v4 /
// fwstate_map_v6 objects whose fwtables back state lookups; the names
// are declared as object links here and resolve against published
// objects when the config is published. An empty name declares no link,
// and CHECK_STATE then finds no state for that family.
func NewModuleConfig(
	agent *ffi.Agent,
	name string,
	rules []ACLRule,
	fw4MapName, fw6MapName string,
) (*ModuleConfig, error) {
	prepared := NewRules(rules)
	defer prepared.Free()
	return NewModuleConfigOfRules(agent, name, prepared, fw4MapName, fw6MapName)
}

// NewModuleConfigOfRules is NewModuleConfig over rules already laid out.
func NewModuleConfigOfRules(
	agent *ffi.Agent,
	name string,
	rules *Rules,
	fw4MapName, fw6MapName string,
) (*ModuleConfig, error) {
	var cRulesPtr *C.struct_acl_rule
	if len(rules.rules) > 0 {
		cRulesPtr = &rules.rules[0]
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

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.acl_module_config_init(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		cRulesPtr,
		C.uint32_t(len(rules.rules)),
		cFw4Name,
		cFw6Name,
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

// Free destroys the module config, or reports ffi.ErrStillReferenced while a
// live generation still holds it. Safe to call multiple times.
func (m *ModuleConfig) Free() error {
	return m.ptr.Free(func(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
		var cErr *C.yanet_error
		rc, errno := C.acl_module_config_free((*C.struct_cp_module)(ptr), &cErr)
		return int(rc), unsafe.Pointer(cErr), errno
	})
}
