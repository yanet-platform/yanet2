//go:build linux && (amd64 || arm64)

package coracle

/*
#cgo CFLAGS: -I${SRCDIR}/../../../..
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_object.h"
#include "lib/counters/counters.h"
#include "lib/errors/errors.h"
#include <stdlib.h>

static struct cp_object *
coracle_object_new(
	struct agent *agent,
	const char *type,
	const char *name,
	size_t size,
	yanet_error **err
) {
	if (size < sizeof(struct cp_object)) {
		yanet_error_add(
			err, "object of %zu bytes is smaller than its header", size
		);
		return NULL;
	}
	struct cp_object *object = memory_balloc(&agent->memory_context, size);
	if (object == NULL) {
		yanet_error_add(err, "failed to allocate object");
		return NULL;
	}
	if (cp_object_init(object, agent, type, name, err)) {
		memory_bfree(&agent->memory_context, object, size);
		return NULL;
	}
	return object;
}

static void
coracle_object_destroy(struct cp_object *object, size_t size) {
	struct agent *agent = ADDR_OF(&object->entity.agent);
	cp_object_fini(object);
	memory_bfree(&agent->memory_context, object, size);
}

static int
coracle_register_counter(
	struct counter_registry *registry,
	const char *name,
	uint64_t size,
	yanet_error **err
) {
	return counter_registry_register(registry, name, size, err) ==
			       COUNTER_INVALID
		       ? -1
		       : 0;
}
*/
import "C"

import (
	"unsafe"
)

// ObjectNew allocates an object of that many bytes in an agent's memory
// and runs cp_object_init on its header, as an object builder does.
//
// The body after the header is left as the allocator returns it. A size
// smaller than the header is refused before C writes past the block, and
// a failed header frees the block.
func ObjectNew(agent unsafe.Pointer, objectType, name string, size uintptr) (unsafe.Pointer, error) {
	cType, cName := C.CString(objectType), C.CString(name)
	defer C.free(unsafe.Pointer(cType))
	defer C.free(unsafe.Pointer(cName))
	var cErr *C.yanet_error
	object := C.coracle_object_new((*C.struct_agent)(agent), cType, cName, C.size_t(size), &cErr)
	if object == nil {
		return nil, failure(-1, cErr)
	}
	return unsafe.Pointer(object), failure(0, cErr)
}

// RegisterCounter runs counter_registry_register, adding a counter of
// that many values to a counter registry.
func RegisterCounter(registry unsafe.Pointer, name string, size uint64) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	var cErr *C.yanet_error
	rc := C.coracle_register_counter((*C.struct_counter_registry)(registry), cName, C.uint64_t(size), &cErr)
	return failure(rc, cErr)
}

// ObjectTryDestroy runs cp_object_try_destroy: it fails while a
// generation references the object and otherwise holds the object's agent
// for its free.
func ObjectTryDestroy(object unsafe.Pointer) error {
	var cErr *C.yanet_error
	rc := C.cp_object_try_destroy((*C.struct_cp_object)(object), &cErr)
	return failure(rc, cErr)
}

// ObjectDestroy frees an object of that many bytes no generation holds,
// as the typed frees of objects do once the body is freed.
//
// It runs cp_object_fini, then frees the block in its agent's memory.
func ObjectDestroy(object unsafe.Pointer, size uintptr) {
	C.coracle_object_destroy((*C.struct_cp_object)(object), C.size_t(size))
}

// AgentDestroyReleased runs agent_destroy_released, returning the hold a
// typed free took on an agent.
func AgentDestroyReleased(agent unsafe.Pointer) {
	C.agent_destroy_released((*C.struct_agent)(agent))
}
