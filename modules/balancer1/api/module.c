#include "module.h"

#include "common/memory.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"

#include "../dataplane/module.h"
#include "../dataplane/real.h"

#include "modules/balancer1/dataplane/session.h"

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
	uint32_t tcp_syn_ack_timeout,
	uint32_t tcp_syn_timeout,
	uint32_t tcp_fin_timeout,
	uint32_t tcp_timeout,
	uint32_t udp_timeout,
	uint32_t default_timeout
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

	// Set config timeouts
	config->timeouts = (struct sessions_timeouts){tcp_syn_ack_timeout,
						      tcp_syn_timeout,
						      tcp_fin_timeout,
						      tcp_timeout,
						      udp_timeout,
						      default_timeout};

	// Set session table
	SET_OFFSET_OF(&config->session_table, session_table);

	// Set default values to free config save
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
balancer_module_config_free(struct cp_module *) {
	// TODO
}