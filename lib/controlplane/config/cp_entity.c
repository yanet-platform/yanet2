#include "cp_entity.h"

#include "common/spinlock.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/reclaim.h"
#include "lib/controlplane/config/zone.h"

#include <errno.h>
#include <stdlib.h>
#include <string.h>

// Longest identity field the destroy gate's refusal buffers hold; every
// kind's name and type fields are this length today.
#define CP_ENTITY_ID_LEN 80

struct agent *
cp_entity_item_owner(struct registry_item *item) {
	return ADDR_OF(&cp_entity_of(item)->agent);
}

int
cp_entity_try_destroy(
	struct registry_item *item,
	const struct cp_entity_desc *desc,
	yanet_error **err
) {
	struct agent *agent = cp_entity_item_owner(item);
	struct cp_config *cp_config =
		(agent != NULL) ? ADDR_OF(&agent->cp_config) : NULL;

	char type[CP_ENTITY_ID_LEN];
	char name[CP_ENTITY_ID_LEN];

	if (cp_config != NULL) {
		cp_config_lock(cp_config);
		spinlock_lock(&cp_config->agent_lock);
	}

	// Everything read from the item happens under the locks, including
	// the identity copies for the refusal.
	//
	// Once they are gone, a final generation release may tear the item's
	// registries down and an agent sweep may reclaim the arena the item
	// lives in. The hold below must also be atomic against the sweep's
	// zero check of the owner's loaded count, which runs under the agent
	// lock alone, so the acquisition takes that lock too.
	strtcpy(type, (const char *)item + desc->type_offset, sizeof(type));
	strtcpy(name, (const char *)item + desc->name_offset, sizeof(name));

	uint64_t refcnt = __atomic_load_n(&item->refcnt, __ATOMIC_RELAXED);

	if (refcnt == 0) {
		if (agent != NULL) {
			// Hold the owner against reclamation for the caller's
			// typed destroy: the destructor and the caller's error
			// reporting run unlocked, on storage only this count
			// keeps alive.
			__atomic_add_fetch(
				&agent->loaded_entity_count, 1, __ATOMIC_RELAXED
			);
		}
	}

	if (cp_config != NULL) {
		// Every completed hold of the locks sweeps: what the gate
		// leaves unreferenced is reclaimed by its own exit, not by
		// an unrelated later holder.
		cp_config_reclaim_unused_agents_locked(cp_config);
		spinlock_unlock(&cp_config->agent_lock);
		cp_config_unlock(cp_config);
	}

	if (refcnt != 0) {
		// errno is set last, right before the return, so the error
		// formatting above cannot clobber what the caller reads.
		yanet_error_add_kind(
			err,
			YANET_ERROR_BUSY,
			"%s '%s:%s' is still referenced by a live generation",
			desc->noun,
			type,
			name
		);
		errno = EAGAIN;
		return -1;
	}

	return 0;
}

int
cp_entity_registry_init(
	struct memory_context *memory_context,
	struct cp_config *owner,
	struct registry *registry,
	const struct cp_entity_desc *desc,
	yanet_error **err
) {
	if (registry_init(memory_context, owner, registry, 8)) {
		yanet_error_add(
			err, "failed to initialize %s registry", desc->noun
		);
		return -1;
	}

	return 0;
}

int
cp_entity_registry_copy(
	struct memory_context *memory_context,
	struct registry *new_registry,
	struct registry *old_registry,
	const struct cp_entity_desc *desc,
	yanet_error **err
) {
	if (registry_copy(
		    memory_context, &desc->keys, new_registry, old_registry
	    )) {
		yanet_error_add(err, "failed to copy %s registry", desc->noun);
		return -1;
	}

	// Mirror each copied item's new generation reference into its own
	// agent.
	//
	// The reference count now counts exactly these registry references,
	// so the per-agent live count and the items' reference counts move in
	// lockstep: both track only references that can outlive a creator.
	for (uint64_t idx = 0; idx < new_registry->capacity; ++idx) {
		struct registry_item *item = registry_get(new_registry, idx);
		if (item == NULL) {
			continue;
		}
		struct agent *agent = cp_entity_item_owner(item);
		if (agent != NULL) {
			__atomic_add_fetch(
				&agent->loaded_entity_count, 1, __ATOMIC_RELAXED
			);
		}
	}

	return 0;
}

void
cp_entity_registry_fini(struct registry *registry) {
	// Mirror each remaining item's dropped generation reference into its
	// own agent.
	//
	// The order is strict: every dereference of agent-owned storage —
	// snapshotting the owners, releasing the item references — happens
	// while the loaded count still carries these references, and the
	// count drops only afterwards, one subtraction per distinct agent. A
	// count reaching zero makes the agent reclaimable by a concurrent
	// sweep, which frees the agent and its arena wholesale, so the
	// subtraction is the final touch of anything the agent owns.
	//
	// The zero transition releases nothing by itself: an item that
	// leaves its last registry becomes dangling and is destroyed by its
	// owner's next free attempt, or reclaimed with the agent's arena if
	// that owner is gone.
	if (ADDR_OF(&registry->items) == NULL) {
		return;
	}

	uint64_t capacity = registry->capacity;
	struct agent **owners = malloc(sizeof(*owners) * capacity);
	if (owners != NULL) {
		for (uint64_t idx = 0; idx < capacity; ++idx) {
			struct registry_item *item =
				registry_get(registry, idx);
			owners[idx] = (item != NULL)
					      ? cp_entity_item_owner(item)
					      : NULL;
		}
		registry_fini(registry, NULL, NULL);
		agent_unload_references(owners, capacity);
		free(owners);
		return;
	}

	// Teardown could not snapshot the owners; release the items and
	// drop the owners' references in allocation-free batches that keep
	// the same dereferences-before-drops order per batch.
	agent_release_registry_items(registry, cp_entity_item_owner);
}

int
cp_entity_registry_upsert(
	struct registry *registry,
	const struct cp_entity_desc *desc,
	registry_item_cmp_func cmp,
	const void *cmp_data,
	struct registry_item *new_item,
	yanet_error **err
) {
	uint64_t index;
	struct registry_item *old_item = NULL;
	if (!registry_lookup(registry, &desc->keys, cmp, cmp_data, &index)) {
		old_item = registry_get(registry, index);
	}

	if (desc->link_counters != NULL &&
	    desc->link_counters(new_item, old_item, err)) {
		return -1;
	}

	if (registry_replace(
		    registry, &desc->keys, cmp, cmp_data, new_item, NULL, NULL
	    )) {
		yanet_error_add(
			err, "failed to replace %s in registry", desc->noun
		);
		return -1;
	}

	// Mirror this upsert's generation-reference changes into each
	// affected item's own agent.
	//
	// Gaining a reference through upsert only ever happens here, and
	// losing one to a displacing upsert only ever happens here too, so
	// the per-agent live count must track both in this one place.
	struct agent *new_agent = cp_entity_item_owner(new_item);
	if (new_agent != NULL) {
		__atomic_add_fetch(
			&new_agent->loaded_entity_count, 1, __ATOMIC_RELAXED
		);
	}
	if (old_item != NULL) {
		struct agent *old_agent = cp_entity_item_owner(old_item);
		if (old_agent != NULL) {
			__atomic_sub_fetch(
				&old_agent->loaded_entity_count,
				1,
				__ATOMIC_RELEASE
			);
		}
	}

	return 0;
}

int
cp_entity_registry_delete(
	struct registry *registry,
	const struct cp_entity_desc *desc,
	registry_item_cmp_func cmp,
	const void *cmp_data
) {
	uint64_t index;
	struct registry_item *old_item = NULL;
	if (!registry_lookup(registry, &desc->keys, cmp, cmp_data, &index)) {
		old_item = registry_get(registry, index);
	}

	if (registry_replace(
		    registry, &desc->keys, cmp, cmp_data, NULL, NULL, NULL
	    )) {
		return -1;
	}

	// Mirror the generation reference this delete drops into the removed
	// item's own agent, matching the decrement upsert performs when it
	// displaces an entry.
	if (old_item != NULL) {
		struct agent *old_agent = cp_entity_item_owner(old_item);
		if (old_agent != NULL) {
			__atomic_sub_fetch(
				&old_agent->loaded_entity_count,
				1,
				__ATOMIC_RELEASE
			);
		}
	}

	return 0;
}
