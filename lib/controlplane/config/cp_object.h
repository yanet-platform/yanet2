#pragma once

#include "common/memory.h"

#include "lib/counters/counters.h"

#include "lib/controlplane/config/defines.h"
#include "lib/controlplane/config/registry.h"

#include "lib/errors/errors.h"

struct agent;
struct cp_object;

typedef void (*cp_object_free_handler)(struct cp_object *self);

/*
 * Generic, uniquely-named shared object accounted in a controlplane
 * configuration generation.
 *
 * Like cp_module/cp_device it is refcounted via registry_item, owns a
 * counter registry, and takes its creator's reference at init: the last
 * release parks it on its agent's parked list until a construction call
 * for its own type reclaims it. Unlike them it has no dataplane handler
 * and no device binding; its purpose is to be referenced by name from
 * other entities.
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

	// Link to the next object parked on the same agent's list, once this
	// object's reference count reaches zero.
	//
	// Only the zero-transition handler sets it, and only a later reclaim
	// for this object's own type reads it. It stays unset until that
	// transition happens. The parked list's tail refers to itself
	// instead of ending at null, so a parked entry's link is never null
	// — which also marks that this object is already parked.
	struct cp_object *parked_next;
};

// Initialize object resources: sub-context, identity, and counter registry.
//
// Zeroes self first, like cp_module_init. Resolves object_type against the
// dataplane's object-type registry (failing if it is not loaded), mirroring
// cp_module_init's module-type lookup. On success takes the creator's
// reference, which cp_object_release drops. On failure, internally calls
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
// zero-init. Runs only from a parked-entry destructor, never on a live
// reference: use cp_object_release for that.
void
cp_object_fini(struct cp_object *self);

// Destroy every parked object of one type, using the caller's destructor
// for that type.
//
// A different type sharing the same parked list is left in place for its
// own next call. A parked entry already sits at reference count zero, so
// the destructor runs directly with no further release and nothing here
// outlives this call. An object type's construction call runs this before
// allocating, so a recreation under memory pressure benefits from the
// space a parked instance would free rather than failing before reaching
// that reclaim. The caller must not hold the configuration lock.
void
cp_object_drain_parked(
	struct agent *agent,
	const char *object_type,
	cp_object_free_handler destroy
);

// The single handler for an object reference count reaching zero, reached
// the same way from a registry drop or an explicit release.
//
// Parks the object on its agent's list instead of destroying it, because
// this generic layer does not know the object's subclass, and an agent can
// host more than one object type at once. The object's own type-specific
// reclaim destroys it later.
//
// Idempotent: once set, a parked entry's link is never null again, so a
// duplicate transition leaves it in place instead of relinking it. Every
// caller already holds the configuration lock, so this handler must not
// take it itself.
void
cp_object_registry_item_free_cb(struct registry_item *item, void *data);

// Drop the reference construction took on the caller's behalf.
//
// The zero-transition handler runs only when this drop is the last
// reference, so a caller must not assume the call freed anything: a live
// or pinned configuration generation may still hold the object. An object
// parked here is not destroyed on the spot — the next construction call
// for the same type reclaims it, or it is freed along with the agent's
// arena.
//
// Takes the object's own agent's configuration lock itself, unlike the
// registry-driven path to the same handler, which already runs under
// that lock.
void
cp_object_release(struct cp_object *self);

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
