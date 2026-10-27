#include "cp_entity.h"

#include "common/spinlock.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/reclaim.h"
#include "lib/controlplane/config/zone.h"

#include <stdlib.h>
#include <string.h>

void
cp_entity_init(struct cp_entity *self, struct agent *agent, const char *name) {
	registry_item_init(&self->config_item);

	SET_OFFSET_OF(&self->agent, agent);

	memory_context_init_from(
		&self->memory_context, &agent->memory_context, name
	);
}

void
cp_entity_fini(struct cp_entity *self) {
	SET_OFFSET_OF(&self->agent, NULL);

	memory_context_fini(&self->memory_context);
}

struct agent *
cp_entity_item_owner(struct registry_item *item) {
	return ADDR_OF(&cp_entity_of(item)->agent);
}

int
cp_entity_try_destroy(struct cp_entity *self) {
	struct agent *agent = ADDR_OF(&self->agent);
	struct cp_config *cp_config =
		(agent != NULL) ? ADDR_OF(&agent->cp_config) : NULL;

	if (cp_config != NULL) {
		cp_config_lock(cp_config);
		spinlock_lock(&cp_config->agent_lock);
	}

	// Everything the gate reads from the item happens under the locks:
	// once they are gone, a final generation release may tear the
	// item's registries down and a sweep may reclaim the arena the
	// item lives in.
	uint64_t refcnt =
		__atomic_load_n(&self->config_item.refcnt, __ATOMIC_RELAXED);

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

	return (refcnt == 0) ? 0 : -1;
}

int
cp_entity_registry_init(
	struct memory_context *memory_context,
	struct cp_config *owner,
	struct cp_entity_registry *registry
) {
	if (registry_init(memory_context, owner, &registry->registry, 8)) {
		return -1;
	}

	SET_OFFSET_OF(&registry->memory_context, memory_context);
	return 0;
}

int
cp_entity_registry_copy(
	struct memory_context *memory_context,
	const struct registry_keys *keys,
	struct cp_entity_registry *new_registry,
	struct cp_entity_registry *old_registry
) {
	if (registry_copy(
		    memory_context,
		    keys,
		    &new_registry->registry,
		    &old_registry->registry
	    )) {
		return -1;
	}

	SET_OFFSET_OF(&new_registry->memory_context, memory_context);

	// Mirror each copied item's new generation reference into its own
	// agent.
	//
	// The reference count now counts exactly these registry references,
	// so the per-agent live count and the items' reference counts move in
	// lockstep: both track only references that can outlive a creator.
	for (uint64_t idx = 0; idx < new_registry->registry.capacity; ++idx) {
		struct registry_item *item =
			registry_get(&new_registry->registry, idx);
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
cp_entity_registry_fini(struct cp_entity_registry *registry) {
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
	if (ADDR_OF(&registry->registry.items) == NULL) {
		return;
	}

	uint64_t capacity = registry->registry.capacity;
	struct agent **owners = malloc(sizeof(*owners) * capacity);
	if (owners != NULL) {
		for (uint64_t idx = 0; idx < capacity; ++idx) {
			struct registry_item *item =
				registry_get(&registry->registry, idx);
			owners[idx] = (item != NULL)
					      ? cp_entity_item_owner(item)
					      : NULL;
		}
		registry_fini(&registry->registry, NULL, NULL);
		agent_unload_references(owners, capacity);
		free(owners);
		return;
	}

	// Teardown could not snapshot the owners; release the items and
	// drop the owners' references in allocation-free batches that keep
	// the same dereferences-before-drops order per batch.
	agent_release_registry_items(&registry->registry, cp_entity_item_owner);
}

int
cp_entity_registry_replace(
	struct cp_entity_registry *registry,
	const struct registry_keys *keys,
	registry_item_cmp_func cmp,
	const void *cmp_data,
	struct registry_item *new_item
) {
	uint64_t index;
	struct registry_item *old_item = NULL;
	if (!registry_lookup(
		    &registry->registry, keys, cmp, cmp_data, &index
	    )) {
		old_item = registry_get(&registry->registry, index);
	}

	if (registry_replace(
		    &registry->registry,
		    keys,
		    cmp,
		    cmp_data,
		    new_item,
		    NULL,
		    NULL
	    )) {
		return -1;
	}

	// Mirror this replacement's generation-reference changes into each
	// affected item's own agent.
	//
	// Gaining a reference through replacement only ever happens here,
	// and losing one to a displacing replacement only ever happens here
	// too, so the per-agent live count must track both in this one
	// place.
	struct agent *new_agent =
		(new_item != NULL) ? cp_entity_item_owner(new_item) : NULL;
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
