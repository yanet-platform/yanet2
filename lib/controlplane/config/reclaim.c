#include "reclaim.h"

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
			if (prev_agent->loaded_module_count == 0 &&
			    prev_agent->loaded_device_count == 0 &&
			    prev_agent->loaded_object_count == 0) {
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
