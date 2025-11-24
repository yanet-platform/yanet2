#include "mock.h"
#include <assert.h>
#include <dlfcn.h>
#include <stddef.h>
#include <stdlib.h>

#include "common/exp_array.h"
#include "lib/controlplane/config/zone.h"
#include "lib/logging/log.h"
#include "worker.h"

////////////////////////////////////////////////////////////////////////////////

extern int
dataplane_load_module(
	struct dp_config *dp_config, void *bin_hndl, const char *name
);

extern int
dataplane_load_device(
	struct dp_config *dp_config, void *bin_hndl, const char *name
);

////////////////////////////////////////////////////////////////////////////////

static int
dataplane_initialize(
	void *storage,
	size_t cp_memory,
	size_t dp_memory,
	size_t workers_count,
	struct yanet_worker_mock *workers,
	struct cp_config **res_cp_config,
	struct dp_config **res_dp_config
) {
	struct dp_config *dp_config = (struct dp_config *)storage;
	memset(dp_config, 0, sizeof(*dp_config));

	dp_config->numa_idx = 0;
	dp_config->instance_count = 1;
	dp_config->instance_idx = 0;
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

	void *bin_hndl = dlopen(NULL, RTLD_NOW | RTLD_GLOBAL);

	int rc = dataplane_load_module(dp_config, bin_hndl, "forward");
	if (rc == -1) {
		return -1;
	}
	rc = dataplane_load_module(dp_config, bin_hndl, "route");
	if (rc == -1) {
		return -1;
	}
	rc = dataplane_load_module(dp_config, bin_hndl, "decap");
	if (rc == -1) {
		return -1;
	}
	rc = dataplane_load_module(dp_config, bin_hndl, "dscp");
	if (rc == -1) {
		return -1;
	}
	rc = dataplane_load_module(dp_config, bin_hndl, "nat64");
	if (rc == -1) {
		return -1;
	}
	rc = dataplane_load_module(dp_config, bin_hndl, "balancer");
	if (rc == -1) {
		return -1;
	}
	rc = dataplane_load_module(dp_config, bin_hndl, "pdump");
	if (rc == -1) {
		return -1;
	}
	rc = dataplane_load_module(dp_config, bin_hndl, "acl");
	if (rc == -1) {
		return -1;
	}

	rc = dataplane_load_device(dp_config, bin_hndl, "plain");
	if (rc == -1) {
		return -1;
	}

	rc = dataplane_load_device(dp_config, bin_hndl, "vlan");
	if (rc == -1) {
		return -1;
	}

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

	dp_config->workers = memory_balloc(
		&dp_config->memory_context,
		workers_count * sizeof(struct dp_worker *)
	);
	if (workers == NULL) {
		return -1;
	}
	for (size_t i = 0; i < workers_count; ++i) {
		SET_OFFSET_OF(&dp_config->workers[i], &workers[i].dp_worker);
	}

	*res_dp_config = dp_config;
	*res_cp_config = cp_config;

	return 0;
}

////////////////////////////////////////////////////////////////////////////////

int
yanet_mock_init(
	struct yanet_mock *mock,
	size_t cp_memory,
	size_t dp_memory,
	void *arena,
	size_t workers
) {
	if (arena == NULL) {
		arena = aligned_alloc(64, cp_memory + dp_memory);
		if (arena == NULL) {
			return -1;
		}
		mock->arena = arena;
	}
	if ((uintptr_t)arena % 64 != 0) {
		return -1;
	}
	mock->shm = arena;

    struct cp_config *cp_config;
    struct dp_config *dp_config;
    int res = dataplane_initialize(arena, cp_memory, dp_memory, workers, mock->workers, &cp_config, &dp_config);
    if (res != 0) {
        yanet_mock_free(mock);
        return -1;
    }

    // init worker mocks
    for (size_t i = 0; i < workers; ++i) {
        memset(&mock->workers[i], 0, sizeof(struct yanet_worker_mock));
        mock->workers[i].cp_config = cp_config;
        mock->workers[i].dp_config = dp_config;
    }

	return 0;
}
