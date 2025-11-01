#include "module.h"

#include "common/container_of.h"
#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "filter.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"

#include "../dataplane/module.h"
#include "../dataplane/real.h"
#include "../dataplane/vs.h"

#include "modules/balancer/dataplane/session.h"
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
	struct balancer_session_table *session_table,
	size_t vs_count,
	struct balancer_vs_config **vs_configs,
	struct balancer_sessions_timeouts *sessions_timeouts
) {
	struct balancer_module_config *config =
		(struct balancer_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct balancer_module_config)
		);
	if (config == NULL) {
		return NULL;
	}

	// Init cp_module
	if (cp_module_init(
		    &config->cp_module,
		    agent,
		    "balancer",
		    name,
		    balancer_module_config_free
	    )) {
		goto free_config;
	}

	// Set sessions timeouts
	config->timeouts = *sessions_timeouts;

	// Set session table
	SET_OFFSET_OF(&config->session_table, session_table);

	// Set default values to safe free on error
	config->vs_count = 0;
	config->vs = NULL;
	config->real_count = 0;
	config->reals = NULL;
	int ret = balancer_vs_init(config, vs_count, vs_configs);
	if (ret < 0) {
		goto free_config;
	}

	return &config->cp_module;

free_config:
	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct balancer_module_config)
	);
	return NULL;
}

void
balancer_module_config_free(struct cp_module *config) {
	struct balancer_module_config *balancer_config =
		container_of(config, struct balancer_module_config, cp_module);

	for (size_t vs = 0; vs < balancer_config->vs_count; ++vs) {
		lpm_free(&balancer_config->vs[vs].src_filter);
		ring_free(&balancer_config->vs[vs].real_ring);
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
		config,
		sizeof(struct balancer_module_config)
	);
}