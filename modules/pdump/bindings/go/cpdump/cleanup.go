package cpdump

//#include "modules/pdump/api/cleanup.h"
import "C"

import (
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// CleanupTargets contains published names owned by predecessor pdump agents.
type CleanupTargets struct {
	Modules []string
	Rings   []string
}

// StaleTargets copies predecessor-owned names for an exclusive startup cleanup.
//
// The previous control plane must have stopped, and no other writer may replace
// the target names during discovery and deletion.
func StaleTargets(agent *ffi.Agent) (CleanupTargets, error) {
	var nativeError *C.yanet_error
	targets := C.pdump_cleanup_targets_new((*C.struct_agent)(agent.AsRawPtr()), &nativeError)
	if targets == nil {
		return CleanupTargets{}, cerrors.FromC(unsafe.Pointer(nativeError))
	}
	defer C.pdump_cleanup_targets_free(targets)

	result := CleanupTargets{
		Modules: make([]string, 0, targets.module_count),
		Rings:   make([]string, 0, targets.ring_count),
	}
	for _, name := range unsafe.Slice(targets.modules, targets.module_count) {
		result.Modules = append(result.Modules, C.GoString(&name[0]))
	}
	for _, name := range unsafe.Slice(targets.rings, targets.ring_count) {
		result.Rings = append(result.Rings, C.GoString(&name[0]))
	}
	return result, nil
}
