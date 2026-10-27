package testutils

/*
#cgo CFLAGS: -I../../../../
#include <stdlib.h>
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "objects/l3b/api/l3b_virtual_service_object.h"

// The caller serializes mutations; the generation pin protects object lifetime.
static int
l3b_test_read_real_ring(struct agent *agent, const char *name,
                      uint32_t *indexes, uint32_t capacity) {
    struct cp_config *config = ADDR_OF(&agent->cp_config);
    struct cp_config_gen *generation = cp_config_gen_acquire(config);
    struct cp_object *object = cp_object_registry_lookup(
        &generation->object_registry, L3B_VIRTUAL_SERVICE_OBJECT_TYPE, name);
    int result = -1;
    if (object != NULL) {
        struct l3b_virtual_service_object *service = container_of(
            object, struct l3b_virtual_service_object, cp_object);
        struct real_ring *ring = &service->virtual_service.real_ring;
        uint32_t count = __atomic_load_n(&ring->count, __ATOMIC_ACQUIRE);
        if (count <= capacity) {
            uint32_t *stored = ADDR_OF(&ring->server_indexes);
            for (uint32_t idx = 0; idx < count; ++idx) {
                indexes[idx] = __atomic_load_n(&stored[idx], __ATOMIC_RELAXED);
            }
            result = (int)count;
        }
    }
    cp_config_gen_release(config, generation);
    return result;
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ReadRealRing copies installed selector indexes without processing packets.
//
// The caller keeps the agent alive and runs no concurrent mutations.
func ReadRealRing(agent *ffi.Agent, name string, capacity uint32) ([]uint32, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	indexes := make([]uint32, capacity)
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pointer := unsafe.SliceData(indexes)
	if pointer != nil {
		pinner.Pin(pointer)
	}
	count := C.l3b_test_read_real_ring(
		(*C.struct_agent)(agent.AsRawPtr()), cName,
		(*C.uint32_t)(unsafe.Pointer(pointer)), C.uint32_t(capacity),
	)
	if count < 0 {
		return nil, fmt.Errorf("service %q is absent or its ring exceeds the inspection capacity", name)
	}
	return indexes[:int(count)], nil
}
