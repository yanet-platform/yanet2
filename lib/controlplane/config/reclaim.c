#include "reclaim.h"

#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

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

// Report whether agent is still the registry head of its name — the one a
// same-name attach would splice a successor over.
//
// The caller must hold the configuration lock, which every registry splice
// also holds, so the structure cannot change under the scan. A false
// return means the owner was superseded: items it still creates would
// raise a predecessor's loaded count from zero and race the sweep's
// reclamation, so update paths must refuse them.
bool
agent_is_current(struct cp_config *cp_config, struct agent *agent) {
	struct cp_agent_registry *registry =
		ADDR_OF(&cp_config->agent_registry);
	if (registry != NULL) {
		for (uint64_t idx = 0; idx < registry->count; ++idx) {
			struct agent *head = ADDR_OF(&registry->agents[idx]);
			if (!strncmp(head->name, agent->name, AGENT_NAME_LEN)) {
				return head == agent;
			}
		}
	}

	return false;
}

void
cp_config_reclaim_unused_agents_locked(struct cp_config *cp_config) {

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
cp_config_reclaim_unused_agents(struct cp_config *cp_config) {
	if (cp_config == NULL) {
		return;
	}

	// The sweep waits out a busy agent lock rather than leaving the
	// reclamation to a later holder. Waiting is safe: no agent-lock
	// holder ever waits for the configuration lock, and every
	// agent-lock section is short. A configuration lock held by the
	// calling thread means the caller's own exit path sweeps before
	// releasing it.
	if (cp_config_locked_by_thread != cp_config) {
		spinlock_lock(&cp_config->agent_lock);
		cp_config_reclaim_unused_agents_locked(cp_config);
		spinlock_unlock(&cp_config->agent_lock);
	}
}

void
agent_destroy_released(struct agent *agent) {
	if (agent == NULL) {
		return;
	}

	// The configuration is captured before the subtraction: once the
	// count reaches zero a concurrent sweep may reclaim the agent, and
	// nothing of the agent may be read past that point.
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	// The release half publishes the caller's typed destructor and
	// everything before it, so the sweep that observes the resulting
	// zero reclaims the arena ordered after those accesses.
	//
	// A drop that releases the last live reference runs that sweep
	// itself: a typed destroy has no reason to stay lock-free, and
	// leaving the reclamation to a later holder waits on one that may
	// never come. A caller already holding the configuration lock is
	// on a path that sweeps before releasing it.
	if (__atomic_sub_fetch(
		    &agent->loaded_entity_count, 1, __ATOMIC_RELEASE
	    ) == 0) {
		cp_config_reclaim_unused_agents(cp_config);
	}
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
