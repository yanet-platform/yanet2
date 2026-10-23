#include "reclaim.h"

#include <stdlib.h>

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/registry.h"
#include "lib/controlplane/config/zone.h"

void
agent_cleanup(struct agent *agent) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	// Child contexts may be backed by the arenas reclaimed below.
	memory_context_fini(&agent->memory_context);

	struct agent_arena *arenas = ADDR_OF(&agent->arenas);
	if (arenas) {
		for (uint64_t arena_idx = 0; arena_idx < agent->arena_count;
		     ++arena_idx) {
			memory_bfree(
				&cp_config->memory_context,
				ADDR_OF(&arenas[arena_idx].data),
				arenas[arena_idx].size
			);
		}
		memory_bfree(
			&cp_config->memory_context,
			arenas,
			sizeof(struct agent_arena) * agent->arena_count
		);
	}

	struct agent_storage *storage = ADDR_OF(&agent->storage);
	while (storage != NULL) {
		struct agent_storage *next = ADDR_OF(&storage->next);
		memory_bfree(
			&cp_config->memory_context,
			storage,
			sizeof(struct agent_storage) + storage->size
		);
		storage = next;
	}

	memory_bfree(&cp_config->memory_context, agent, sizeof(struct agent));
}

// Owner pointers compare by address only: the array is scratch the
// caller frees immediately, and no agent memory is dereferenced here.
// The comparison goes through the integer representation, because
// unrelated agents are not part of one array object and relational
// comparison of the pointers themselves is undefined.
static int
agent_owner_cmp(const void *left, const void *right) {
	uintptr_t l = (uintptr_t)*(struct agent *const *)left;
	uintptr_t r = (uintptr_t)*(struct agent *const *)right;
	if (l == r) {
		return 0;
	}
	return l < r ? -1 : 1;
}

void
agent_unload_references(struct agent **owners, uint64_t count) {
	// Sort the scratch snapshot and apply one subtraction per run of
	// equal pointers: teardown runs synchronously on the final release,
	// so aggregating must stay near-linear in the registry size rather
	// than rescanning per owner.
	qsort(owners, count, sizeof(*owners), agent_owner_cmp);

	for (uint64_t idx = 0; idx < count;) {
		struct agent *owner = owners[idx];
		if (owner == NULL) {
			++idx;
			continue;
		}

		uint64_t total = 0;
		while (idx < count && owners[idx] == owner) {
			++idx;
			++total;
		}

		// The subtraction is the final touch of the agent: once it
		// reaches zero a concurrent sweep may reclaim the agent and
		// free its storage, and only pointer comparisons — which no
		// reclamation invalidates — follow it. Its release half
		// publishes this teardown's prior accesses to the agent-owned
		// items, which the sweep's zero check acquires before
		// reclamation.
		__atomic_sub_fetch(
			&owner->loaded_entity_count, total, __ATOMIC_RELEASE
		);
	}
}

void
cp_config_reclaim_unused_agents_locked(struct cp_config *cp_config) {
	cp_config_assert_locked(cp_config);

	struct cp_agent_registry *registry =
		ADDR_OF(&cp_config->agent_registry);
	if (registry == NULL) {
		return;
	}

	for (uint64_t idx = 0; idx < registry->count; ++idx) {
		struct agent *agent = ADDR_OF(&registry->agents[idx]);
		while (agent != NULL && ADDR_OF(&agent->prev) != NULL) {
			struct agent *prev_agent = ADDR_OF(&agent->prev);
			// The count moves with atomics, so a reference dropped
			// outside the lock may not be visible yet — a stale
			// high read only postpones that agent's reclamation to
			// a later sweep, never frees it early. The load
			// acquires the dropping paths' release halves: once it
			// observes zero, the reclamation below runs ordered
			// after every teardown access that preceded the final
			// drop.
			if (__atomic_load_n(
				    &prev_agent->loaded_entity_count,
				    __ATOMIC_ACQUIRE
			    ) == 0) {
				SET_OFFSET_OF(
					&agent->prev, ADDR_OF(&prev_agent->prev)
				);
				agent_cleanup(prev_agent);
				continue;
			}

			agent = prev_agent;
		}
	}
}

void
agent_destroy_released(struct agent *agent) {
	if (agent == NULL) {
		return;
	}

	// The release half publishes the caller's typed destructor and
	// everything before it, so the sweep that observes the resulting
	// zero reclaims the arena ordered after those accesses.
	__atomic_sub_fetch(&agent->loaded_entity_count, 1, __ATOMIC_RELEASE);
}

// Release every item of a retiring registry and mirror the drops into
// the owners' loaded count without allocating: the fallback for
// teardown whose one-shot owners snapshot could not be allocated.
//
// Batches of a fixed size keep the strict order the snapshot path keeps
// for the whole registry at once: every dereference of agent-owned
// storage — reading an item's owner, releasing its registry reference —
// happens before that batch's loaded count drops. An owner whose count
// reaches zero in a batch has no reference left in any batch, earlier
// or later, so neither a later batch nor the storage release below can
// dereference an arena a concurrent sweep reclaimed.
void
agent_release_registry_items(
	struct registry *registry,
	struct agent *(*item_owner)(struct registry_item *item)
) {
#define AGENT_RELEASE_BATCH 64
	struct agent *owners[AGENT_RELEASE_BATCH];
	for (uint64_t idx = 0; idx < registry->capacity;) {
		size_t count = 0;
		while (idx < registry->capacity && count < AGENT_RELEASE_BATCH
		) {
			struct registry_item *item =
				registry_get(registry, idx++);
			if (item == NULL) {
				continue;
			}
			struct agent *owner = item_owner(item);
			registry_item_unref(
				item, ADDR_OF(&registry->owner), NULL, NULL
			);
			owners[count++] = owner;
		}
		agent_unload_references(owners, count);
	}
#undef AGENT_RELEASE_BATCH
	registry_discard(registry);
}
