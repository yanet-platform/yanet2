#pragma once

#include "common/strutils.h"

#include "lib/controlplane/config/registry.h"

#include "lib/errors/errors.h"

struct agent;
struct registry_item;

// Longest identity field the generic key buffers hold; every kind's name
// and type fields are this length today.
#define CP_ENTITY_KEY_LEN 80

// Canonical (name, type) lookup key shared by the agent-owned config
// entity kinds.
struct cp_entity_key {
	char name[CP_ENTITY_KEY_LEN];
	char type[CP_ENTITY_KEY_LEN];
};

// How one agent-owned config entity kind plugs into the generic registry
// wrapper layer.
//
// The identity field offsets are relative to the kind struct's first
// member, its registry item, which is also what lets a holder recover the
// typed struct from a bare item pointer.
struct cp_entity_desc {
	// Key set driving the registry's hash side-index. The comparison
	// must be the kind's trampoline over cp_entity_item_cmp: the
	// indexed lookup path recognises the canonical comparison by
	// function identity.
	struct registry_keys keys;

	// Noun naming the kind in error messages.
	const char *noun;

	// Maps one registry item to the agent that owns it.
	struct agent *(*item_owner)(struct registry_item *item);

	size_t name_offset;
	size_t name_len;
	size_t type_offset;
	size_t type_len;

	// Counter-continuity step of an upsert: links the new item's
	// counter registries against the displaced item's. NULL skips
	// linking.
	int (*link_counters)(
		struct registry_item *new_item,
		struct registry_item *old_item,
		yanet_error **err
	);
};

// Fill a key from the kind's identity strings.
static inline void
cp_entity_key_set(
	struct cp_entity_key *key, const char *type, const char *name
) {
	strtcpy(key->name, name, sizeof(key->name));
	strtcpy(key->type, type, sizeof(key->type));
}

// Compare an item's identity against a key: name first, then type.
int
cp_entity_item_cmp(
	const struct cp_entity_desc *desc,
	const struct registry_item *item,
	const struct cp_entity_key *key
);

// Compare an item's name alone; backs the name-keyed delete path.
int
cp_entity_name_cmp(
	const struct cp_entity_desc *desc,
	const struct registry_item *item,
	const char *name
);

uint64_t
cp_entity_key_hash(
	const struct cp_entity_desc *desc, const struct cp_entity_key *key
);

uint64_t
cp_entity_item_hash(
	const struct cp_entity_desc *desc, const struct registry_item *item
);

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
cp_entity_registry_fini(
	struct registry *registry, const struct cp_entity_desc *desc
);

// Find the slot of the item matching the canonical key.
int
cp_entity_registry_lookup_index(
	struct registry *registry,
	const struct cp_entity_desc *desc,
	const struct cp_entity_key *key,
	uint64_t *index
);

// Find the item matching the canonical key, or NULL.
struct registry_item *
cp_entity_registry_lookup(
	struct registry *registry,
	const struct cp_entity_desc *desc,
	const struct cp_entity_key *key
);

// Insert or replace by the canonical key, running the kind's counter
// linking against the displaced item first.
int
cp_entity_registry_upsert(
	struct registry *registry,
	const struct cp_entity_desc *desc,
	const struct cp_entity_key *key,
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
