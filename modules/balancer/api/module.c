#include "../dataplane/module.h"
#include "../dataplane/lookup.h"
#include "../dataplane/real.h"
#include "../dataplane/vs.h"

#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/network.h"

#include "counters/counters.h"
#include "module.h"

#include "filter.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"

#include "ring.h"
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

extern uint64_t
register_common_counter(struct counter_registry *registry);

extern uint64_t
register_icmp_v4_counter(struct counter_registry *registry);

extern uint64_t
register_icmp_v6_counter(struct counter_registry *registry);

extern uint64_t
register_l4_counter(struct counter_registry *registry);

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
	struct balancer_sessions_timeouts *sessions_timeouts,
	size_t vs_count,
	struct balancer_vs_config **vs_configs,
	struct net4_addr *source_addr,
	struct net6_addr *source_addr_v6,
	size_t decap_addr_count,
	struct net4_addr *decap_addrs,
	size_t decap_addr_v6_count,
	struct net6_addr *decap_addrs_v6
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
		goto free_config_no_lpm;
	}

	// Init sessions timeouts
	memcpy(&balancer_config->sessions_timeouts,
	       sessions_timeouts,
	       sizeof(struct balancer_sessions_timeouts));

	// Set balancer state
	SET_OFFSET_OF(&balancer_config->state, state);

	// Set default values to safe free on error
	if (lpm_init(
		    &balancer_config->decap_filter_v4, &agent->memory_context
	    )) {
		goto free_config_no_lpm;
	}
	if (lpm_init(
		    &balancer_config->decap_filter_v6, &agent->memory_context
	    )) {
		lpm_free(&balancer_config->decap_filter_v4);
		goto free_config_no_lpm;
	}
	balancer_config->vs_count = 0;
	balancer_config->vs = NULL;
	balancer_config->real_count = 0;
	balancer_config->reals = NULL;
	int ret = balancer_vs_init(balancer_config, vs_count, vs_configs);
	if (ret < 0) {
		goto free_config;
	}

	// register module counters

	struct counter_registry *registry =
		&balancer_config->cp_module.counter_registry;
	balancer_config->counter.common = register_common_counter(registry);
	balancer_config->counter.icmp_v4 = register_icmp_v4_counter(registry);
	balancer_config->counter.icmp_v6 = register_icmp_v6_counter(registry);
	balancer_config->counter.l4 = register_l4_counter(registry);
	if (balancer_config->counter.common == (uint64)-1 ||
	    balancer_config->counter.icmp_v4 == (uint64)-1 ||
	    balancer_config->counter.icmp_v6 == (uint64)-1 ||
	    balancer_config->counter.l4 == (uint64)-1) {
		goto free_config;
	}

	// set source address
	memcpy(balancer_config->source_ip, source_addr, NET4_LEN);
	memcpy(balancer_config->source_ip_v6, source_addr_v6, NET6_LEN);

	// setup decap lpm for ipv4 addresses
	for (size_t i = 0; i < decap_addr_count; ++i) {
		if (lpm_insert(
			    &balancer_config->decap_filter_v4,
			    NET4_LEN,
			    decap_addrs[i].bytes,
			    decap_addrs[i].bytes,
			    1
		    )) {
			goto free_config;
		}
	}

	// setup decap lpm for ipv6 addresses
	for (size_t i = 0; i < decap_addr_v6_count; ++i) {
		if (lpm_insert(
			    &balancer_config->decap_filter_v6,
			    NET6_LEN,
			    decap_addrs_v6[i].bytes,
			    decap_addrs_v6[i].bytes,
			    1
		    )) {
			goto free_config;
		}
	}

	return &balancer_config->cp_module;

free_config:
	lpm_free(&balancer_config->decap_filter_v4);
	lpm_free(&balancer_config->decap_filter_v6);

free_config_no_lpm:
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