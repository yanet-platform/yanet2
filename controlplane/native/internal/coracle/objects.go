//go:build linux && (amd64 || arm64)

package coracle

/*
#cgo CFLAGS: -I${SRCDIR}/../../../..
#include <stdlib.h>
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/cp_object.h"
#include "lib/controlplane/config/zone.h"
#include "lib/counters/counters.h"
#include "lib/errors/errors.h"

// Allocates an object of size bytes, its header first, in the agent's
// memory and initializes the header, as the Rust SDK's object builder
// does; NULL when either fails.
static struct cp_object *
coracle_object_new(
	struct agent *agent, const char *type, const char *name, size_t size
) {
	struct cp_object *object =
		memory_balloc(&agent->memory_context, size);
	if (object == NULL) {
		return NULL;
	}
	yanet_error *err = NULL;
	if (cp_object_init(object, agent, type, name, &err)) {
		yanet_error_free(err);
		memory_bfree(&agent->memory_context, object, size);
		return NULL;
	}
	return object;
}

static int
coracle_objects_update(
	struct agent *agent, struct cp_object **objects, uint64_t count
) {
	yanet_error *err = NULL;
	int rc = cp_config_update_objects(
		ADDR_OF(&agent->dp_config),
		ADDR_OF(&agent->cp_config),
		count,
		objects,
		&err
	);
	yanet_error_free(err);
	return rc;
}

static int
coracle_module_link_object(
	struct cp_module *module, const char *type, const char *name
) {
	yanet_error *err = NULL;
	uint64_t index;
	int rc = cp_module_link_object(module, type, name, &index, &err);
	yanet_error_free(err);
	return rc;
}

static int64_t
coracle_counter_register(
	struct counter_registry *registry, const char *name, uint64_t size
) {
	yanet_error *err = NULL;
	int64_t rc = counter_registry_register(registry, name, size, &err);
	yanet_error_free(err);
	return rc;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// NewObject allocates an object of size bytes, its header first, in an
// agent's memory and runs cp_object_init on the header.
func NewObject(agent unsafe.Pointer, objectType, name string, size uintptr) (unsafe.Pointer, error) {
	cType, cName := C.CString(objectType), C.CString(name)
	defer C.free(unsafe.Pointer(cType))
	defer C.free(unsafe.Pointer(cName))
	object := C.coracle_object_new((*C.struct_agent)(agent), cType, cName, C.size_t(size))
	if object == nil {
		return nil, fmt.Errorf("create object %s:%s", objectType, name)
	}
	return unsafe.Pointer(object), nil
}

// UpdateObjects runs cp_config_update_objects for the objects through
// an agent, as agent_update_objects does.
func UpdateObjects(agent unsafe.Pointer, objects []unsafe.Pointer) error {
	pointers := (*[1 << 20]*C.struct_cp_object)(C.calloc(C.size_t(max(len(objects), 1)), C.size_t(unsafe.Sizeof(uintptr(0)))))
	defer C.free(unsafe.Pointer(pointers))
	for idx, object := range objects {
		pointers[idx] = (*C.struct_cp_object)(object)
	}
	if C.coracle_objects_update((*C.struct_agent)(agent), &pointers[0], C.uint64_t(len(objects))) != 0 {
		return fmt.Errorf("update %d objects", len(objects))
	}
	return nil
}

// LinkObject runs cp_module_link_object, linking a module to the object
// of a type and name.
func LinkObject(module unsafe.Pointer, objectType, name string) error {
	cType, cName := C.CString(objectType), C.CString(name)
	defer C.free(unsafe.Pointer(cType))
	defer C.free(unsafe.Pointer(cName))
	if C.coracle_module_link_object((*C.struct_cp_module)(module), cType, cName) != 0 {
		return fmt.Errorf("link object %s:%s", objectType, name)
	}
	return nil
}

// RegisterCounter runs counter_registry_register and returns the
// counter's index.
func RegisterCounter(registry unsafe.Pointer, name string, size uint64) (uint64, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	index := C.coracle_counter_register((*C.struct_counter_registry)(registry), cName, C.uint64_t(size))
	if index < 0 {
		return 0, fmt.Errorf("register counter %q", name)
	}
	return uint64(index), nil
}
