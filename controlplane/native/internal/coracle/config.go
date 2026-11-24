//go:build linux && (amd64 || arm64)

package coracle

/*
#cgo CFLAGS: -I${SRCDIR}/../../../..
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/zone.h"
#include "lib/counters/counters.h"
#include "lib/errors/errors.h"
#include <stdlib.h>

static void
coracle_module_destroy(struct cp_module *module) {
	struct agent *agent = ADDR_OF(&module->entity.agent);
	cp_module_fini(module);
	memory_bfree(&agent->memory_context, module, sizeof(*module));
}

static int
coracle_module_link_object(
	struct cp_module *module,
	const char *type,
	const char *name,
	yanet_error **err
) {
	uint64_t index;
	return cp_module_link_object(module, type, name, &index, err);
}

static int
coracle_module_link_device(
	struct cp_module *module, const char *name, yanet_error **err
) {
	uint64_t index;
	return cp_module_link_device(module, name, &index, err);
}

static int
coracle_module_register_counter(
	struct cp_module *module,
	const char *tag,
	const char *name,
	uint64_t size,
	yanet_error **err
) {
	struct counter_registry *registry =
		tag[0] == '\0'
			? &module->counter_registry
			: cp_module_counter_registry(module, tag, NULL, err);
	if (registry == NULL) {
		return -1;
	}
	return counter_registry_register(registry, name, size, err) ==
			       COUNTER_INVALID
		       ? -1
		       : 0;
}
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
)

// failure returns the error C reported for a failed call, its text and
// kind kept.
func failure(rc C.int, cErr *C.yanet_error) error {
	if rc == 0 {
		cerrors.Free(unsafe.Pointer(cErr))
		return nil
	}
	if err := cerrors.FromC(unsafe.Pointer(cErr)); err != nil {
		return err
	}
	return fmt.Errorf("the C call failed with %d and no error", int(rc))
}

// UpdateModules runs cp_config_update_modules, publishing the modules.
func UpdateModules(dp, cp unsafe.Pointer, modules []unsafe.Pointer) error {
	pointers := make([]*C.struct_cp_module, len(modules))
	for idx, module := range modules {
		pointers[idx] = (*C.struct_cp_module)(module)
	}
	var first **C.struct_cp_module
	if len(pointers) > 0 {
		first = &pointers[0]
	}
	var cErr *C.yanet_error
	rc := C.cp_config_update_modules(
		(*C.struct_dp_config)(dp), (*C.struct_cp_config)(cp), C.uint64_t(len(modules)), first, &cErr,
	)
	return failure(rc, cErr)
}

// GenRelease runs cp_config_gen_release, dropping a reference to a
// generation outside the configuration lock.
func GenRelease(cp, gen unsafe.Pointer) {
	C.cp_config_gen_release((*C.struct_cp_config)(cp), (*C.struct_cp_config_gen)(gen))
}

// ModuleDestroy frees a module configuration no generation holds, as the
// typed frees of header-only modules do.
//
// It runs cp_module_fini, then frees the block in its agent's memory.
func ModuleDestroy(module unsafe.Pointer) {
	C.coracle_module_destroy((*C.struct_cp_module)(module))
}

// ModuleLinkObject runs cp_module_link_object, declaring that a module
// links the object of a type and name.
func ModuleLinkObject(module unsafe.Pointer, objectType, name string) error {
	cType, cName := C.CString(objectType), C.CString(name)
	defer C.free(unsafe.Pointer(cType))
	defer C.free(unsafe.Pointer(cName))
	var cErr *C.yanet_error
	rc := C.coracle_module_link_object((*C.struct_cp_module)(module), cType, cName, &cErr)
	return failure(rc, cErr)
}

// ModuleLinkDevice runs cp_module_link_device, adding a device to the
// devices a module names.
func ModuleLinkDevice(module unsafe.Pointer, name string) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	var cErr *C.yanet_error
	rc := C.coracle_module_link_device((*C.struct_cp_module)(module), cName, &cErr)
	return failure(rc, cErr)
}

// ModuleRegisterCounter registers a counter of that many values in a
// module's runtime registry of that tag, or its own for an empty tag.
//
// A missing runtime registry is created.
func ModuleRegisterCounter(module unsafe.Pointer, tag, name string, size uint64) error {
	cTag, cName := C.CString(tag), C.CString(name)
	defer C.free(unsafe.Pointer(cTag))
	defer C.free(unsafe.Pointer(cName))
	var cErr *C.yanet_error
	rc := C.coracle_module_register_counter((*C.struct_cp_module)(module), cTag, cName, C.uint64_t(size), &cErr)
	return failure(rc, cErr)
}
