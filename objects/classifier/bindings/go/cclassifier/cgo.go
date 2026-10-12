package cclassifier

//#cgo CFLAGS: -I../../../../../
//#cgo LDFLAGS: -L../../../../../build/objects/classifier/api -lclassifier_objects
//#cgo LDFLAGS: -L../../../../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../../../../build/lib/classify -lclassify_compiler
//#cgo LDFLAGS: -L../../../../../build/lib/counters -lcounters
//#cgo LDFLAGS: -L../../../../../build/lib/errors -lerrors
//
//#include "api/agent.h"
//#include "objects/classifier/api/classifier_object.h"
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ObjectConfig is an opaque handle to a classifier object in shared
// memory.
type ObjectConfig struct {
	ptr ffi.ObjectConfig
}

// Free destroys the object config, or reports ffi.ErrStillReferenced
// while a live generation still holds it. Safe to call multiple times.
func (o *ObjectConfig) Free() error {
	return o.ptr.Free(func(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
		var cErr *C.yanet_error
		rc, errno := C.classifier_object_config_free((*C.struct_cp_object)(ptr), &cErr)
		return int(rc), unsafe.Pointer(cErr), errno
	})
}

// AsFFIObject returns the underlying common object config handle.
func (o *ObjectConfig) AsFFIObject() ffi.ObjectConfig {
	return o.ptr
}

// RuleCount reports the size of the object's ruleset: the valid range
// of the classification results the object yields.
func (o *ObjectConfig) RuleCount() uint32 {
	return uint32(C.classifier_object_rule_count((*C.struct_cp_object)(o.ptr.AsRawPtr())))
}

// newObject wraps the create API of one subtype: compiles the rules
// into C structures and creates the object in shared memory.
func newObject(
	agent *ffi.Agent,
	name string,
	rules []Rule,
	create func(agent *C.struct_agent, cName *C.char, rules *C.struct_classifier_object_rule, count C.uint32_t, cErr **C.yanet_error) *C.struct_cp_object,
) (*ObjectConfig, error) {
	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	cRules := make([]C.struct_classifier_object_rule, len(rules))
	for idx := range rules {
		cRules[idx] = rules[idx].cBuild(pinner)
	}

	var cRulesPtr *C.struct_classifier_object_rule
	if len(cRules) > 0 {
		cRulesPtr = &cRules[0]
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := create((*C.struct_agent)(agent.AsRawPtr()), cName, cRulesPtr, C.uint32_t(len(cRules)), &cErr)
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to create classifier object %q: %w",
			name,
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}

	return &ObjectConfig{
		ptr: ffi.NewObjectConfig(unsafe.Pointer(ptr)),
	}, nil
}
