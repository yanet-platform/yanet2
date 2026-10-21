#include "reclaim.h"

#include <stdlib.h>

#include "lib/controlplane/agent/agent.h"
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
agent_unload_references(
	struct agent **owners, uint64_t count, enum agent_reference_kind kind
) {
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
		// reclamation invalidates — follow it.
		switch (kind) {
		case AGENT_REFERENCE_MODULE:
			__atomic_sub_fetch(
				&owner->loaded_module_count,
				total,
				__ATOMIC_RELAXED
			);
			break;
		case AGENT_REFERENCE_DEVICE:
			__atomic_sub_fetch(
				&owner->loaded_device_count,
				total,
				__ATOMIC_RELAXED
			);
			break;
		case AGENT_REFERENCE_OBJECT:
			__atomic_sub_fetch(
				&owner->loaded_object_count,
				total,
				__ATOMIC_RELAXED
			);
			break;
		}
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
			// The counts move with relaxed atomics, so a reference
			// dropped outside the lock may not be visible yet. A
			// stale high read only postpones that agent's
			// reclamation to a later sweep, never frees it early.
			if (__atomic_load_n(
				    &prev_agent->loaded_module_count,
				    __ATOMIC_RELAXED
			    ) == 0 &&
			    __atomic_load_n(
				    &prev_agent->loaded_device_count,
				    __ATOMIC_RELAXED
			    ) == 0 &&
			    __atomic_load_n(
				    &prev_agent->loaded_object_count,
				    __ATOMIC_RELAXED
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

	// A lock held by the calling thread means the caller's own exit path
	// sweeps before releasing it; a lock held by anyone else defers to
	// that holder's sweep, which every completed hold performs.
	if (cp_config_locked_by_thread != cp_config &&
	    cp_config_try_lock(cp_config)) {
		cp_config_reclaim_unused_agents_locked(cp_config);
		cp_config_unlock(cp_config);
	}
}

void
agent_destroy_released(struct agent *agent, enum agent_reference_kind kind) {
	if (agent == NULL) {
		return;
	}

	// The configuration is captured before the subtraction: once the
	// count reaches zero a concurrent sweep may reclaim the agent, and
	// nothing of the agent may be read past that point.
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	bool released_last = false;
	switch (kind) {
	case AGENT_REFERENCE_MODULE:
		released_last =
			__atomic_sub_fetch(
				&agent->loaded_module_count, 1, __ATOMIC_RELAXED
			) == 0;
		break;
	case AGENT_REFERENCE_DEVICE:
		released_last =
			__atomic_sub_fetch(
				&agent->loaded_device_count, 1, __ATOMIC_RELAXED
			) == 0;
		break;
	case AGENT_REFERENCE_OBJECT:
		released_last =
			__atomic_sub_fetch(
				&agent->loaded_object_count, 1, __ATOMIC_RELAXED
			) == 0;
		break;
	}

	// Dropping the last live reference ends everything that kept a
	// superseded agent reclaimable-safe, so this drop reclaims it itself.
	// Unlike a
	// reader's pin release, a typed destroy has no reason to stay
	// lock-free, and deferring to the lock holder is not sound: the
	// holder's sweep may already have run before this drop became
	// visible. A caller already holding the lock is on a path that
	// sweeps before releasing it.
	if (released_last && cp_config_locked_by_thread != cp_config) {
		cp_config_lock(cp_config);
		cp_config_reclaim_unused_agents_locked(cp_config);
		cp_config_unlock(cp_config);
	}
}
