#include "yanet_mock.h"
#include "api/agent.h"
#include "common/exp_array.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "controlplane/config/econtext.h"
#include "dataplane/worker.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"

#include <stddef.h>
#include <stdint.h>
#include <string.h>

static inline int
dataplane_register_module(struct dp_config *dp_config, const char *name) {
	struct dp_module *dp_modules = ADDR_OF(&dp_config->dp_modules);
	if (mem_array_expand_exp(
		    &dp_config->memory_context,
		    (void **)&dp_modules,
		    sizeof(*dp_modules),
		    &dp_config->module_count
	    )) {
		return -1;
	}

	struct dp_module *dp_module = dp_modules + dp_config->module_count - 1;

	dp_module->handler = NULL;

	memset(dp_module->name, 0, sizeof(sizeof(dp_module->name)));
	memcpy(dp_module->name, name, strlen(name));

	SET_OFFSET_OF(&dp_config->dp_modules, dp_modules);
	return 0;
}

static inline int
dataplane_init(
	uint32_t numa_idx,
	uint32_t instance_idx,
	void *storage,
	size_t dp_memory,
	size_t cp_memory,
	char **module_types,
	size_t module_types_cnt,
	struct dp_config **res_dp_config,
	struct cp_config **res_cp_config
) {
	struct dp_config *dp_config = (struct dp_config *)storage;
	memset(dp_config, 0, sizeof(*dp_config));

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

	struct cp_config *cp_config =
		(struct cp_config *)((uintptr_t)storage + dp_memory);
	memset(cp_config, 0, sizeof(*cp_config));

	block_allocator_init(&cp_config->block_allocator);
	block_allocator_put_arena(
		&cp_config->block_allocator,
		storage + dp_memory + sizeof(struct cp_config),
		cp_memory - sizeof(struct cp_config)
	);
	memory_context_init(
		&cp_config->memory_context, "cp", &cp_config->block_allocator
	);

	struct cp_agent_registry *cp_agent_registry =
		(struct cp_agent_registry *)memory_balloc(
			&cp_config->memory_context,
			sizeof(struct cp_agent_registry)
		);
	cp_agent_registry->count = 0;
	SET_OFFSET_OF(&cp_config->agent_registry, cp_agent_registry);

	SET_OFFSET_OF(&dp_config->cp_config, cp_config);
	SET_OFFSET_OF(&cp_config->dp_config, dp_config);

	cp_config->cp_config_gen = NULL;
	struct agent agent;
	memory_context_init_from(
		&agent.memory_context, &cp_config->memory_context, "stub agent"
	);
	SET_OFFSET_OF(&agent.dp_config, dp_config);
	SET_OFFSET_OF(&agent.cp_config, cp_config);
	struct cp_config_gen *cp_config_gen = cp_config_gen_create(&agent);
	cp_config_gen->config_gen_ectx = NULL;
	SET_OFFSET_OF(&cp_config->cp_config_gen, cp_config_gen);

	dp_config->instance_idx = instance_idx;
	dp_config->instance_count = 1;

	for (size_t i = 0; i < module_types_cnt; ++i) {
		if (dataplane_register_module(dp_config, module_types[i]) !=
		    0) {
			return -1;
		}
	}

	dp_config->worker_count = 1;
	struct dp_worker *worker =
		memory_balloc(&dp_config->memory_context, sizeof(*worker));
	if (worker == NULL) {
		return -1;
	}
	struct dp_worker **workers =
		memory_balloc(&dp_config->memory_context, sizeof(*workers));
	if (workers == NULL) {
		return -1;
	}
	SET_OFFSET_OF(&workers[0], worker);
	SET_OFFSET_OF(&dp_config->workers, workers);
	worker->gen = 0;

	*res_dp_config = dp_config;
	*res_cp_config = cp_config;

	return 0;
}

void
yanet_mock_cp_update_prepare(struct yanet_mock *mock) {
	struct dp_config *dp_config = ADDR_OF(&mock->dp_config);
	struct cp_config *cp_config = ADDR_OF(&mock->cp_config);
	uint64_t cur_gen = ADDR_OF(&cp_config->cp_config_gen)->gen;
	struct dp_worker **workers = ADDR_OF(&dp_config->workers);
	for (size_t i = 0; i < dp_config->worker_count; ++i) {
		struct dp_worker *worker = ADDR_OF(&workers[i]);
		worker->gen = cur_gen + 1;
	}
}

void
yanet_mock_free(struct yanet_mock *mock) {
	(void)mock;
}

int
yanet_mock_init(
	struct yanet_mock *mock,
	void *storage,
	size_t dp_memory,
	size_t cp_memory,
	char **module_types,
	size_t module_types_cnt
) {
	if ((uintptr_t)storage % 64 != 0) {
		return -1;
	}
	SET_OFFSET_OF(&mock->shm, storage);
	struct cp_config *cp_config;
	struct dp_config *dp_config;
	int res = dataplane_init(
		0,
		0,
		storage,
		dp_memory,
		cp_memory,
		module_types,
		module_types_cnt,
		&dp_config,
		&cp_config
	);
	if (res != 0) {
		return res;
	}
	SET_OFFSET_OF(&mock->dp_config, dp_config);
	SET_OFFSET_OF(&mock->cp_config, cp_config);
	return 0;
}

struct agent *
yanet_mock_agent_attach(
	struct yanet_mock *mock, const char *agent_name, size_t memory_limit
) {
	return agent_attach(
		(struct yanet_shm *)ADDR_OF(&mock->shm),
		0,
		agent_name,
		memory_limit
	);
}

void
yanet_mock_handle_packets(
	struct yanet_mock *mock,
	struct cp_module *cp_module,
	struct packet_front *packet_front,
	packets_handler handler
) {
	(void)mock;
	struct module_ectx ctx;
	struct dp_worker worker;
	worker.idx = 0;
	SET_OFFSET_OF(&ctx.cp_module, cp_module);
	handler(&worker, &ctx, packet_front);
}