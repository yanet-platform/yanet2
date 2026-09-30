package ringtest

//#cgo CFLAGS: -I../../../../
//#cgo LDFLAGS: -L../../../../build/lib/controlplane/config -lconfig_cp
//
//#include <stdlib.h>
//
//#include "common/memory_address.h"
//#include "lib/controlplane/agent/agent.h"
//#include "lib/controlplane/config/cp_object.h"
//#include "lib/controlplane/config/zone.h"
//
//// ringtest_ref_config returns the agent's config pointer.
////
//// cp_config_lock records this pointer as the lock holder of the calling
//// thread. Every registry change asserts the lock against the same pointer.
//static inline struct cp_config *
//ringtest_ref_config(struct agent *agent) {
//	return ADDR_OF(&agent->cp_config);
//}
//
//// ringtest_ref_lookup_object returns the published cp_object with the given
//// type and name in the agent's live generation.
////
//// It finds the object the same way ring_object_exists does.
//static inline struct cp_object *
//ringtest_ref_lookup_object(
//	struct agent *agent, const char *object_type, const char *object_name
//) {
//	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
//	cp_config_lock(cp_config);
//	struct cp_config_gen *gen = ADDR_OF(&cp_config->cp_config_gen);
//	struct cp_object *object =
//		cp_config_gen_lookup_object(gen, object_type, object_name);
//	cp_config_unlock(cp_config);
//	return object;
//}
//
//// ringtest_ref_locked_upsert runs cp_object_registry_upsert under the
//// agent's config lock.
////
//// Production code changes the registry under the same lock, for example in
//// cp_config_gen_install and cp_object_try_destroy. So this extra reference
//// cannot race with a publish or a delete that changes the same object's
//// reference count.
//static inline int
//ringtest_ref_locked_upsert(
//	struct agent *agent,
//	struct cp_object_registry *registry,
//	const char *object_type,
//	const char *object_name,
//	struct cp_object *object,
//	yanet_error **err
//) {
//	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
//	cp_config_lock(cp_config);
//	int rc = cp_object_registry_upsert(
//		registry, object_type, object_name, object, err
//	);
//	cp_config_unlock(cp_config);
//	return rc;
//}
//
//// ringtest_ref_locked_fini runs cp_object_registry_fini under the agent's
//// config lock, for the same reason as ringtest_ref_locked_upsert.
//static inline void
//ringtest_ref_locked_fini(
//	struct agent *agent, struct cp_object_registry *registry
//) {
//	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
//	cp_config_lock(cp_config);
//	cp_object_registry_fini(registry);
//	cp_config_unlock(cp_config);
//}
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
)

// Reference is an extra reference to a published ring.
//
// It plays the role of a live generation that has not retired yet. Its
// registry lives in C memory. The C registry calls may keep pointers into
// it after they return, so it needs an address that does not move.
type Reference struct {
	agent    *C.struct_agent
	registry *C.struct_cp_object_registry
}

// Hold adds an extra reference to the named ring in the agent's published
// generation.
//
// Until Release, every attempt to free the ring fails.
func Hold(agent *ffi.Agent, name string) (*Reference, error) {
	cAgent := (*C.struct_agent)(agent.AsRawPtr())

	cType := C.CString(cring.ObjectType)
	defer C.free(unsafe.Pointer(cType))
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	object := C.ringtest_ref_lookup_object(cAgent, cType, cName)
	if object == nil {
		return nil, fmt.Errorf("ring %q is not published", name)
	}

	registry := (*C.struct_cp_object_registry)(C.malloc(C.sizeof_struct_cp_object_registry))
	if registry == nil {
		return nil, fmt.Errorf("failed to allocate reference registry")
	}

	// The registry's owner must be the real config. The registry calls
	// check that the caller holds the lock of this owner.
	//
	// A NULL owner matches only a thread that holds no lock. This helper
	// takes the agent's lock, so a NULL owner would fail that check.
	cpConfig := C.ringtest_ref_config(cAgent)

	var cErr *C.yanet_error
	if rc := C.cp_object_registry_init(&cAgent.memory_context, cpConfig, registry, &cErr); rc != 0 {
		C.free(unsafe.Pointer(registry))
		return nil, fmt.Errorf("failed to init reference registry: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	if rc := C.ringtest_ref_locked_upsert(cAgent, registry, cType, cName, object, &cErr); rc != 0 {
		C.ringtest_ref_locked_fini(cAgent, registry)
		C.free(unsafe.Pointer(registry))
		return nil, fmt.Errorf("failed to hold ring %q: %w", name, cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return &Reference{agent: cAgent, registry: registry}, nil
}

// Release drops the extra reference and frees the registry.
//
// Later calls do nothing.
func (m *Reference) Release() {
	if m.registry == nil {
		return
	}
	C.ringtest_ref_locked_fini(m.agent, m.registry)
	C.free(unsafe.Pointer(m.registry))
	m.registry = nil
}
