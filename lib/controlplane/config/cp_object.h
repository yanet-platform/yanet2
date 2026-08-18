#pragma once

#include "common/memory.h"

#include "lib/counters/counters.h"

#include "lib/controlplane/config/defines.h"
#include "lib/controlplane/config/registry.h"

#include "lib/errors/errors.h"

struct agent;

/*
 * Generic, uniquely-named shared object accounted in a controlplane
 * configuration generation.
 *
 * Like cp_module/cp_device it is refcounted via registry_item and owns a
 * counter registry. Unlike them it has no dataplane handler and no device
 * binding; its purpose is to be referenced by name from other entities.
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
// cp_module_init's module-type lookup. On failure, internally calls
// cp_object_fini and returns -1.
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
// zero-init.
void
cp_object_fini(struct cp_object *self);

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
