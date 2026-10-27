#pragma once

#include "common/memory.h"

#include "lib/counters/counters.h"

#include "lib/controlplane/config/cp_entity.h"
#include "lib/controlplane/config/defines.h"
#include "lib/controlplane/config/registry.h"

#include "lib/errors/errors.h"

struct agent;
struct cp_object;

/*
 * Generic, uniquely-named shared object accounted in a controlplane
 * configuration generation.
 *
 * Like cp_module/cp_device it is refcounted via registry_item and owns a
 * counter registry, and like them it starts dangling at a zero reference
 * count: only an upsert into a live generation references it, and only
 * its owner may destroy it once every referencing generation is gone.
 * Unlike them it has no dataplane handler and no device binding; its
 * purpose is to be referenced by name from other entities.
 */
struct cp_object {
	struct cp_entity entity;

	char type[CP_OBJECT_TYPE_LEN];
	char name[CP_OBJECT_NAME_LEN];

	// Reference to the dataplane object type resolved by cp_object_init.
	uint64_t dp_object_idx;

	struct counter_registry counter_registry;

	// Counters describing the relation between this object and the modules
	// that link it. Each module's per-worker link execution context spawns
	// its own counter storage from this registry, keeping the counter
	// definitions on the object and the per-link values independent.
	struct counter_registry link_counter_registry;
};
// The entity base must sit at offset zero: registry items hand out bare
// pointers into it.
_Static_assert(
	offsetof(struct cp_object, entity) == 0,
	"struct cp_object must embed the entity base at offset zero"
);

// Initialize object resources: sub-context, identity, and counter registry.
//
// Zeroes self first, like cp_module_init. Resolves object_type against the
// dataplane's object-type registry (failing if it is not loaded), mirroring
// cp_module_init's module-type lookup. The object starts dangling at a
// zero reference count. On failure, internally calls cp_object_fini and
// returns -1.
int
cp_object_init(
	struct cp_object *self,
	struct agent *agent,
	const char *object_type,
	const char *name,
	yanet_error **err
);

// Tear down resources acquired by cp_object_init.
//
// Finalizes the sub-context last, mirroring cp_module_fini. Idempotent on
// zero-init. Runs only from the owner's destroy path, never on a
// referenced object.
void
cp_object_fini(struct cp_object *self);

// Try to take a dangling object out of circulation for destruction.
//
// An object's reference count is the number of live configuration
// generations that registered it; construction never takes a reference of
// its own. Zero therefore means dangling: the object is registered
// nowhere, no reader can reach it, and only its owner still knows it
// exists.
//
// Returns 0 when the count is zero, granting the caller the exclusive
// right to run the object's typed destroy routine immediately. Returns -1
// with errno EAGAIN while a live generation still holds the object; the
// caller must keep its handle and retry once the generations drain.
//
// A successful call also takes one destruction hold of the owning
// agent: the caller must run the object's typed destructor and then
// return that hold with agent_destroy_released, on storage only the hold
// keeps alive.
//
// Gains and registry mutations run under the configuration lock; the
// final release of a retired generation drops references without it, so
// a racing drop can lower the count after the read — which can only
// postpone a destroy attempt, never free an item a registry still
// holds.
int
cp_object_try_destroy(struct cp_object *self, yanet_error **err);

struct cp_object_registry {
	struct cp_entity_registry entity_registry;
};

int
cp_object_registry_init(
	struct memory_context *memory_context,
	struct cp_config *owner,
	struct cp_object_registry *registry,
	yanet_error **err
);

int
cp_object_registry_copy(
	struct memory_context *memory_context,
	struct cp_object_registry *new_registry,
	struct cp_object_registry *old_registry,
	yanet_error **err
);

void
cp_object_registry_fini(struct cp_object_registry *registry);

struct cp_object *
cp_object_registry_get(struct cp_object_registry *registry, uint64_t index);

struct cp_object *
cp_object_registry_lookup(
	struct cp_object_registry *registry,
	const char *object_type,
	const char *object_name
);

int
cp_object_registry_lookup_index(
	struct cp_object_registry *registry,
	const char *object_type,
	const char *object_name,
	uint64_t *index
);

int
cp_object_registry_upsert(
	struct cp_object_registry *registry,
	const char *object_type,
	const char *object_name,
	struct cp_object *new_object,
	yanet_error **err
);

int
cp_object_registry_delete(
	struct cp_object_registry *registry,
	const char *object_type,
	const char *object_name
);

static inline uint64_t
cp_object_registry_capacity(struct cp_object_registry *registry) {
	return registry->entity_registry.registry.capacity;
}
