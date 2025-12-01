#include "module.h"
#include "counter.h"
#include "lookup.h"

#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "filter.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"

#include "../dataplane/module.h"
#include "../dataplane/real.h"
#include "../dataplane/vs.h"

#include "ring.h"

////////////////////////////////////////////////////////////////////////////////

int
balancer_vs_init(
	struct balancer_module_config *cfg,
	size_t vs_count,
	struct balancer_vs_config **vs_configs
);

// Create new config for the balancer module
struct cp_module *
balancer_module_config_create(
	struct agent *agent,
	const char *name,
	struct balancer_state *state,
	size_t vs_count,
	struct balancer_vs_config **vs_configs
) {
	struct balancer_module_config *balancer_config =
		(struct balancer_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct balancer_module_config)
		);
	if (balancer_config == NULL) {
		return NULL;
	}

	// Init cp_module
	if (cp_module_init(
		    &balancer_config->cp_module,
		    agent,
		    "balancer",
		    name,
		    balancer_module_config_free
	    )) {
		goto free_config;
	}

	// Set balancer state
	SET_OFFSET_OF(&balancer_config->state, state);

	// Set default values to safe free on error
	balancer_config->vs_count = 0;
	balancer_config->vs = NULL;
	balancer_config->real_count = 0;
	balancer_config->reals = NULL;
	int ret = balancer_vs_init(balancer_config, vs_count, vs_configs);
	if (ret < 0) {
		goto free_config;
	}

	// init module config counters

	balancer_config->counter_id = counter_registry_register(
		&balancer_config->cp_module.counter_registry,
		"balancer_counter",
		MODULE_CONFIG_COUNTER_SIZE
	);

	return &balancer_config->cp_module;

free_config:
	memory_bfree(
		&agent->memory_context,
		balancer_config,
		sizeof(struct balancer_module_config)
	);
	return NULL;
}

void
balancer_module_config_free(struct cp_module *config) {
	struct balancer_module_config *balancer_config =
		container_of(config, struct balancer_module_config, cp_module);

	for (size_t i = 0; i < balancer_config->vs_count; ++i) {
		struct virtual_service *vs = ADDR_OF(&balancer_config->vs) + i;
		if (!(vs->flags & VS_PRESENT_IN_CONFIG_FLAG)) {
			continue;
		}
		lpm_free(&vs->src_filter);
		ring_free(&vs->real_ring);
	}

	memory_bfree(
		&config->memory_context,
		ADDR_OF(&balancer_config->vs),
		sizeof(struct virtual_service) * balancer_config->vs_count
	);
	memory_bfree(
		&config->memory_context,
		ADDR_OF(&balancer_config->reals),
		sizeof(struct real) * balancer_config->real_count
	);

	FILTER_FREE(&balancer_config->vs_v4_table, VS_V4_TABLE_TAG);
	FILTER_FREE(&balancer_config->vs_v6_table, VS_V6_TABLE_TAG);

	memory_bfree(
		&ADDR_OF(&config->agent)->memory_context,
		balancer_config,
		sizeof(struct balancer_module_config)
	);
}