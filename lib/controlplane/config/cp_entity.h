#pragma once

#include "common/container_of.h"
#include "common/memory.h"

#include "lib/controlplane/config/registry.h"

#include "lib/errors/errors.h"

struct agent;
struct registry_item;

// Embedded base of every agent-owned config entity: the registry item,
// the owning agent back-pointer, and the entity's own memory context.
//
// The base is always the kind struct's first member, so a holder recovers
// it from a bare registry item with one container_of step, and the typed
// kind struct is one more step from there.
struct cp_entity {
	struct registry_item config_item;

	// Controlplane agent the configuration belongs to. Set by the
	// kind's init; the typed free routines resolve it to return their
	// destruction hold and to reclaim the entity's arena.
	struct agent *agent;

	// Memory context for the entity's own allocations.
	struct memory_context memory_context;
};

// Recover the embedded base of any item an entity registry hands out.
static inline struct cp_entity *
cp_entity_of(struct registry_item *item) {
	return container_of(item, struct cp_entity, config_item);
}

// Initialize the base: the registry item, the agent back-pointer, and a
// sub-memory-context named after the entity.
void
cp_entity_init(struct cp_entity *self, struct agent *agent, const char *name);

// Tear down the base: the agent back-pointer and the sub-memory-context.
//
// A kind frees its own allocations from the context before calling this;
// safe on a zero-init base.
void
cp_entity_fini(struct cp_entity *self);

// The item-to-owner mapping shared by every entity registry, including
// the allocation-free teardown fallback.
struct agent *
cp_entity_item_owner(struct registry_item *item);

// A registry of embedded-base entities, the shape every kind's registry
// proxies.
struct cp_entity_registry {
	struct memory_context *memory_context;
	struct registry registry;
};

// Create an entity registry.
//
// The owner's lock guards the reference counts; NULL exempts an ownerless
// test registry. Returns 0, or -1 leaving the error to the caller.
int
cp_entity_registry_init(
	struct memory_context *memory_context,
	struct cp_config *owner,
	struct cp_entity_registry *registry
);

// Copy an entity registry, taking one reference per surviving item and
// mirroring each gain into the item owner's loaded count.
//
// Returns 0, or -1 leaving the error to the caller.
int
cp_entity_registry_copy(
	struct memory_context *memory_context,
	const struct registry_keys *keys,
	struct cp_entity_registry *new_registry,
	struct cp_entity_registry *old_registry
);

// Release every item and free the registry storage.
void
cp_entity_registry_fini(struct cp_entity_registry *registry);

// Replace the item matching the given comparison shape — the kind's
// canonical comparison, so the indexed lookup path recognises it — and
// mirror the generation-reference change into the affected owners'
// loaded counts: the new item gains one, the displaced one loses one; a
// NULL new item is a delete that only drops the displaced reference.
//
// Returns 0, or -1 when the replacement itself fails, leaving the error
// to the caller.
int
cp_entity_registry_replace(
	struct cp_entity_registry *registry,
	const struct registry_keys *keys,
	registry_item_cmp_func cmp,
	const void *cmp_data,
	struct registry_item *new_item
);

// Try to take a dangling entity out of circulation for destruction.
//
// The item's reference count is the number of live configuration
// generations that registered it; construction never takes a reference of
// its own. Zero therefore means dangling: the item is registered nowhere,
// no reader can reach it, and only its owner still knows it exists.
//
// Returns 0 when the count is zero, granting the caller the exclusive
// right to run the item's typed destroy routine immediately, and taking
// one destruction hold of the owning agent — the caller must run the
// item's typed destructor and then return that hold with
// agent_destroy_released, on storage only the hold keeps alive. Returns
// -1 while a live generation still holds the item; the caller must keep
// its handle and retry once the generations drain, and formats the
// refusal itself: the refusal keeps the item referenced, which keeps the
// owning arena alive, so reading the item's own identity fields unlocked
// is safe.
int
cp_entity_try_destroy(struct cp_entity *self);
