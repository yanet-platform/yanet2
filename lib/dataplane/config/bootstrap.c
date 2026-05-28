#include "bootstrap.h"

#include "common/memory.h"
#include "common/memory_address.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"

int
dp_storage_init(
	uint32_t numa_idx,
	uint32_t instance_idx,
	void *storage,
	size_t dp_memory,
	size_t cp_memory,
	struct dp_config **res_dp_config,
	struct cp_config **res_cp_config
) {
	// TODO: move pages to requested numa node

	struct dp_config *dp_config = (struct dp_config *)storage;

	dp_config->numa_idx = numa_idx;
	dp_config->instance_idx = instance_idx;
	dp_config->storage_size = dp_memory + cp_memory;

	block_allocator_init(&dp_config->block_allocator);
	block_allocator_put_arena(
		&dp_config->block_allocator,
		storage + sizeof(struct dp_config),
		dp_memory - sizeof(struct dp_config)
	);
	memory_context_init(
		&dp_config->memory_context, "dp", &dp_config->block_allocator
	);

	dp_config->config_lock = 0;

	dp_config->dp_modules = NULL;
	dp_config->module_count = 0;

	dp_config->workers = NULL;
	dp_config->worker_count = 0;

	struct cp_config *cp_config =
		(struct cp_config *)((uintptr_t)storage + dp_memory);

	block_allocator_init(&cp_config->block_allocator);
	block_allocator_put_arena(
		&cp_config->block_allocator,
		storage + dp_memory + sizeof(struct cp_config),
		cp_memory - sizeof(struct cp_config)
	);
	memory_context_init(
		&cp_config->memory_context, "cp", &cp_config->block_allocator
	);

	// FIXME: cp_config bootstrap routine
	struct cp_agent_registry *cp_agent_registry =
		(struct cp_agent_registry *)memory_balloc(
			&cp_config->memory_context,
			sizeof(struct cp_agent_registry)
		);
	cp_agent_registry->count = 0;
	SET_OFFSET_OF(&cp_config->agent_registry, cp_agent_registry);

	SET_OFFSET_OF(&dp_config->cp_config, cp_config);
	SET_OFFSET_OF(&cp_config->dp_config, dp_config);

	*res_dp_config = dp_config;
	*res_cp_config = cp_config;

	return 0;
}
