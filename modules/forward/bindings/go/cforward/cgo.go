package cforward

//#cgo CFLAGS: -I../../../../../
//#cgo LDFLAGS: -L../../../../../build/modules/forward/api -lforward_cp
//#cgo LDFLAGS: -L../../../../../build/objects/classifier/api -lclassifier_objects
//#cgo LDFLAGS: -L../../../../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../../../../build/lib/classify -lclassify_compiler
//#cgo LDFLAGS: -L../../../../../build/lib/errors -lerrors
//
//#include "api/agent.h"
//#include "modules/forward/api/controlplane.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ModuleConfig is an opaque handle to the forward module configuration in
// shared memory.
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// NewModuleConfig allocates a new forward module configuration bound to
// the named classifier object via the C API. The object must already be
// installed (agent_update_objects), and the decision count must match
// the object's ruleset.
func NewModuleConfig(agent *ffi.Agent, name, classifierObject string, decisions []Decision) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	cClassifier := C.CString(classifierObject)
	defer C.free(unsafe.Pointer(cClassifier))

	cDecisions := make([]C.struct_forward_decision, len(decisions))
	for idx := range decisions {
		cDecisions[idx] = decisions[idx].cBuild()
	}

	var cDecisionsPtr *C.struct_forward_decision
	if len(cDecisions) > 0 {
		cDecisionsPtr = &cDecisions[0]
	}

	var cErr *C.yanet_error
	ptr := C.forward_module_config_init(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		cClassifier,
		cDecisionsPtr,
		C.uint32_t(len(cDecisions)),
		&cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to initialize module config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return &ModuleConfig{
		ptr: ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}, nil
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
		rc, errno := C.forward_module_config_free((*C.struct_cp_module)(ptr), &cErr)
		return int(rc), unsafe.Pointer(cErr), errno
	})
}
