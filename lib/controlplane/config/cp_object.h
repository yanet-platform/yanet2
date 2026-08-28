#pragma once

#include "common/memory.h"

#include "lib/counters/counters.h"

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
	struct registry_item config_item;

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

	// Controlplane agent the configuration belongs to.
	struct agent *agent;

	// Memory context for the object's own allocations.
	struct memory_context memory_context;
};

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
// The count is read under the configuration lock, which every registry
// mutation also runs under, so the answer is stable against concurrent
// generation installs and releases in other processes.
int
cp_object_try_destroy(struct cp_object *self, yanet_error **err);

struct cp_object_registry {
	struct memory_context *memory_context;
	struct registry registry;
};

// Lookup key for a registry slot: the object identity is the (type, name)
// pair, mirroring the cp_module (type, name) key.
struct cp_object_cmp_data {
	char type[CP_OBJECT_TYPE_LEN];
	char name[CP_OBJECT_NAME_LEN];
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
	return registry->registry.capacity;
}
