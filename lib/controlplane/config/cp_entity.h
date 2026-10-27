#pragma once

#include "common/container_of.h"

#include "lib/controlplane/config/registry.h"

#include "lib/errors/errors.h"

struct agent;
struct registry_item;

// Embedded base of every agent-owned config entity: the registry item
// and the owning agent back-pointer.
//
// The base is always the kind struct's first member, so a holder recovers
// it from a bare registry item with one container_of step, and the typed
// kind struct is one more step from there.
struct cp_entity {
	struct registry_item config_item;

	// Controlplane agent the configuration belongs to. Set by the kind's
	// init; the typed free routines resolve it to return their
	// destruction hold and to reclaim the entity's arena.
	struct agent *agent;
};

// Recover the embedded base of any item an entity registry hands out.
static inline struct cp_entity *
cp_entity_of(struct registry_item *item) {
	return container_of(item, struct cp_entity, config_item);
}

// The item-to-owner mapping shared by every entity registry, including
// the allocation-free teardown fallback.
struct agent *
cp_entity_item_owner(struct registry_item *item);

// How one agent-owned config entity kind plugs into the generic registry
// wrapper layer.
struct cp_entity_desc {
	// Key set driving the registry's hash side-index, built from the
	// kind's own comparison and hash handlers.
	struct registry_keys keys;

	// Noun naming the kind in error messages.
	const char *noun;

	// Offsets of the identity fields within the kind struct, relative
	// to its first member (the embedded base); the destroy gate's
	// refusal message is the only reader.
	size_t name_offset;
	size_t type_offset;

	// Counter-continuity step of an upsert: links the new item's
	// counter registries against the displaced item's. NULL skips
	// linking.
	int (*link_counters)(
		struct registry_item *new_item,
		struct registry_item *old_item,
		yanet_error **err
	);
};

// Try to take a dangling item out of circulation for destruction.
//
// An item's reference count is the number of live configuration
// generations that registered it; construction never takes a reference of
// its own. Zero therefore means dangling: the item is registered nowhere,
// no reader can reach it, and only its owner still knows it exists.
//
// Returns 0 when the count is zero, granting the caller the exclusive
// right to run the item's typed destroy routine immediately. Returns -1
// with errno EAGAIN while a live generation still holds the item; the
// caller must keep its handle and retry once the generations drain.
//
// A successful call also takes one destruction hold of the owning agent:
// the caller must run the item's typed destructor and then return that
// hold with agent_destroy_released, on storage only the hold keeps alive.
int
cp_entity_try_destroy(
	struct registry_item *item,
	const struct cp_entity_desc *desc,
	yanet_error **err
);

// Create a registry for one entity kind.
//
// The owner's lock guards the reference counts; NULL exempts an ownerless
// test registry.
int
cp_entity_registry_init(
	struct memory_context *memory_context,
	struct cp_config *owner,
	struct registry *registry,
	const struct cp_entity_desc *desc,
	yanet_error **err
);

// Copy a registry, taking one reference per surviving item and mirroring
// each gain into the item owner's loaded count.
int
cp_entity_registry_copy(
	struct memory_context *memory_context,
	struct registry *new_registry,
	struct registry *old_registry,
	const struct cp_entity_desc *desc,
	yanet_error **err
);

// Release every item and free the registry storage.
void
cp_entity_registry_fini(struct registry *registry);

// Insert or replace by the given comparison shape — the kind's canonical
// comparison, so the indexed lookup path recognises it — running the
// kind's counter linking against the displaced item first.
int
cp_entity_registry_upsert(
	struct registry *registry,
	const struct cp_entity_desc *desc,
	registry_item_cmp_func cmp,
	const void *cmp_data,
	struct registry_item *new_item,
	yanet_error **err
);

// Remove the item matching the given comparison shape, which may be the
// canonical key comparison or a name-only one.
int
cp_entity_registry_delete(
	struct registry *registry,
	const struct cp_entity_desc *desc,
	registry_item_cmp_func cmp,
	const void *cmp_data
);
