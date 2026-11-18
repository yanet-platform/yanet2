//go:build linux && amd64

package coracle

/*
#cgo CFLAGS: -I${SRCDIR}/../../../..
#include "lib/controlplane/config/cp_device.h"
#include "lib/controlplane/config/cp_function.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/cp_object.h"
#include "lib/controlplane/config/cp_pipeline.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/controlplane/config/zone.h"
#include "lib/errors/errors.h"

static int
coracle_module_upsert(struct cp_module_registry *registry, struct cp_module *module) {
	yanet_error *err = NULL;
	int rc = cp_module_registry_upsert(registry, module->type, module->name, module, &err);
	yanet_error_free(err);
	return rc;
}

#define CORACLE_COPY(kind)                                                     \
	static int coracle_##kind##_registry_copy(                             \
		struct memory_context *context,                                \
		struct cp_##kind##_registry *fresh,                            \
		struct cp_##kind##_registry *old                               \
	) {                                                                    \
		yanet_error *err = NULL;                                       \
		int rc = cp_##kind##_registry_copy(context, fresh, old, &err); \
		yanet_error_free(err);                                         \
		return rc;                                                     \
	}

CORACLE_COPY(module)
CORACLE_COPY(function)
CORACLE_COPY(pipeline)
CORACLE_COPY(device)
CORACLE_COPY(object)

static void *
coracle_ectxs_create(struct cp_config_gen *gen, struct cp_config_gen *old, uint64_t workers) {
	yanet_error *err = NULL;
	void *result = config_gen_ectxs_create(gen, old, workers, &err);
	yanet_error_free(err);
	return result;
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"
)

// RegistryKind names one of the five registries of a generation.
type RegistryKind int

// The registries of a generation.
const (
	ModuleRegistry RegistryKind = iota
	FunctionRegistry
	PipelineRegistry
	DeviceRegistry
	ObjectRegistry
)

// RegistryCopy runs cp_<kind>_registry_copy, copying old into fresh with
// allocations from context.
func RegistryCopy(kind RegistryKind, context, fresh, old unsafe.Pointer) error {
	memory := (*C.struct_memory_context)(context)
	var rc C.int
	switch kind {
	case ModuleRegistry:
		rc = C.coracle_module_registry_copy(memory, (*C.struct_cp_module_registry)(fresh), (*C.struct_cp_module_registry)(old))
	case FunctionRegistry:
		rc = C.coracle_function_registry_copy(memory, (*C.struct_cp_function_registry)(fresh), (*C.struct_cp_function_registry)(old))
	case PipelineRegistry:
		rc = C.coracle_pipeline_registry_copy(memory, (*C.struct_cp_pipeline_registry)(fresh), (*C.struct_cp_pipeline_registry)(old))
	case DeviceRegistry:
		rc = C.coracle_device_registry_copy(memory, (*C.struct_cp_device_registry)(fresh), (*C.struct_cp_device_registry)(old))
	case ObjectRegistry:
		rc = C.coracle_object_registry_copy(memory, (*C.struct_cp_object_registry)(fresh), (*C.struct_cp_object_registry)(old))
	default:
		return fmt.Errorf("unknown registry kind %d", kind)
	}
	if rc != 0 {
		return fmt.Errorf("copy of registry kind %d failed", kind)
	}
	return nil
}

// WithConfigLock runs operation holding the C configuration lock, which C
// records per thread: the calling goroutine stays on one OS thread
// throughout.
func WithConfigLock(cp unsafe.Pointer, operation func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	C.cp_config_lock((*C.struct_cp_config)(cp))
	defer C.cp_config_unlock((*C.struct_cp_config)(cp))
	operation()
}

// ModuleUpsert runs cp_module_registry_upsert for a module.
func ModuleUpsert(registry, module unsafe.Pointer) error {
	if C.coracle_module_upsert((*C.struct_cp_module_registry)(registry), (*C.struct_cp_module)(module)) != 0 {
		return fmt.Errorf("module upsert failed")
	}
	return nil
}

// BuildWorkerContexts runs config_gen_ectxs_create and returns the array
// of per-worker contexts, or nil.
func BuildWorkerContexts(gen, old unsafe.Pointer, workers uint64) unsafe.Pointer {
	return C.coracle_ectxs_create((*C.struct_cp_config_gen)(gen), (*C.struct_cp_config_gen)(old), C.uint64_t(workers))
}
