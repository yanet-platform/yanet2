#include "handler.h"
#include "api/balancer.h"
#include "api/counter.h"
#include "api/vs.h"
#include "common/lpm.h"
#include "common/memory.h"

#include "common/memory_address.h"
#include "common/network.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/diag/diag.h"

#include <assert.h>
#include <stdlib.h>
#include <string.h>

#include "api/handler.h"
#include "counters/counters.h"
#include "state/state.h"

#include "real.h"
#include "vs.h"

////////////////////////////////////////////////////////////////////////////////

static const char *common_module_counter_name = "cmn";
static const char *icmp_v4_module_counter_name = "iv4";
static const char *icmp_v6_module_counter_name = "iv6";
static const char *l4_module_counter_name = "l4";

uint64_t
register_common_counter(struct counter_registry *registry) {
	uint64_t res = counter_registry_register(
		registry,
		common_module_counter_name,
		sizeof(struct balancer_common_stats) / sizeof(uint64_t)
	);

	if (res == (uint64_t)-1) {
		PUSH_ERROR("failed to register counter in registry");
		return -1;
	}

	return res;
}

uint64_t
register_icmp_v4_counter(struct counter_registry *registry) {
	uint64_t res = counter_registry_register(
		registry,
		icmp_v4_module_counter_name,
		sizeof(struct balancer_icmp_stats) / sizeof(uint64_t)
	);

	if (res == (uint64_t)-1) {
		PUSH_ERROR("failed to register counter in registry");
		return -1;
	}

	return res;
}

uint64_t
register_icmp_v6_counter(struct counter_registry *registry) {
	uint64_t res = counter_registry_register(
		registry,
		icmp_v6_module_counter_name,
		sizeof(struct balancer_icmp_stats) / sizeof(uint64_t)
	);

	if (res == (uint64_t)-1) {
		PUSH_ERROR("failed to register counter in registry");
		return -1;
	}

	return res;
}

uint64_t
register_l4_counter(struct counter_registry *registry) {
	uint64_t res = counter_registry_register(
		registry,
		l4_module_counter_name,
		sizeof(struct balancer_l4_stats) / sizeof(uint64_t)
	);

	if (res == (uint64_t)-1) {
		PUSH_ERROR("failed to register counter in registry");
		return -1;
	}

	return res;
}

////////////////////////////////////////////////////////////////////////////////

static int
init_counters(
	struct packet_handler *handler, struct counter_registry *registry
) {
	if ((handler->counter.common = register_common_counter(registry)) ==
	    (uint64_t)-1) {
		PUSH_ERROR("failed to register common counter");
		return -1;
	}
	if ((handler->counter.icmp_v4 = register_icmp_v4_counter(registry)) ==
	    (uint64_t)-1) {
		PUSH_ERROR("failed to register ICMPv4 counter");
		return -1;
	}
	if ((handler->counter.icmp_v6 = register_icmp_v6_counter(registry)) ==
	    (uint64_t)-1) {
		PUSH_ERROR("failed to register ICMPv6 counter");
		return -1;
	}
	if ((handler->counter.l4 = register_l4_counter(registry)) ==
	    (uint64_t)-1) {
		PUSH_ERROR("failed to register L4 counter");
		return -1;
	}

	return 0;
}

static int
init_sources(
	struct packet_handler *handler,
	struct memory_context *mctx,
	struct packet_handler_config *config
) {
	(void)mctx;
	memcpy(&handler->source_ipv4,
	       &config->source_v4,
	       sizeof(struct net4_addr));
	memcpy(&handler->source_ipv6,
	       &config->source_v6,
	       sizeof(struct net6_addr));
	return 0;
}

static int
init_decaps(
	struct packet_handler *handler,
	struct memory_context *mctx,
	struct packet_handler_config *config
) {
	// init ipv4 decap addresses
	if (lpm_init(&handler->decap_ipv4, mctx) != 0) {
		NEW_ERROR(
			"failed to allocate container for decap IPv4 addresses"
		);
		return -1;
	}
	for (size_t i = 0; i < config->decap_v4_count; i++) {
		struct net4_addr *addr = &config->decap_v4[i];
		if (lpm4_insert(
			    &handler->decap_ipv4, addr->bytes, addr->bytes, 1
		    ) != 0) {
			lpm_free(&handler->decap_ipv4);
			NEW_ERROR(
				"failed to insert decap IPv4 address at index "
				"%zu",
				i
			);
			return -1;
		}
	}

	// init ipv6 decap addresses
	if (lpm_init(&handler->decap_ipv6, mctx) != 0) {
		NEW_ERROR(
			"failed to allocate container for decap IPv6 addresses"
		);
		return -1;
	}
	for (size_t i = 0; i < config->decap_v6_count; i++) {
		struct net6_addr *addr = &config->decap_v6[i];
		if (lpm8_insert(
			    &handler->decap_ipv6, addr->bytes, addr->bytes, 1
		    ) != 0) {
			lpm_free(&handler->decap_ipv4);
			lpm_free(&handler->decap_ipv6);
			NEW_ERROR(
				"failed to insert decap IPv6 address at index "
				"%zu",
				i
			);
			return -1;
		}
	}

	return 0;
}

static int
setup_reals_index(struct packet_handler *handler, struct memory_context *mctx) {
	struct balancer_state *state = ADDR_OF(&handler->state);
	size_t registry_reals_count = balancer_state_reals_count(state);
	uint32_t *reals_index = memory_balloc(mctx, sizeof(uint32_t) * registry_reals_count);
	if (reals_index == NULL) {
		NEW_ERROR("failed to allocate memory for reals index");
		return -1;
	}

	memset(reals_index, (uint32_t)-1, sizeof(uint32_t) * registry_reals_count);
	SET_OFFSET_OF(&handler->reals_index, reals_index);
	handler->reals_index_count = registry_reals_count;

	return 0;
}

static int
init_reals(
	struct packet_handler *handler,
	struct balancer_state *state,
	struct memory_context *mctx,
	struct packet_handler_config *config,
	struct counter_registry *registry
) {
	size_t real_count = 0;
	for (size_t i = 0; i < config->vs_count; ++i) {
		real_count += config->vs[i].config.real_count;
	}
	handler->real_count = real_count;
	struct real *reals =
		memory_balloc(mctx, sizeof(struct real) * real_count);
	if (reals == NULL) {
		NEW_ERROR("failed to allocate memory for reals");
		return -1;
	}
	memset(reals, 0, sizeof(struct real) * real_count);
	SET_OFFSET_OF(&handler->reals, reals);

	// setup reals index
	if (setup_reals_index(handler, mctx) != 0) {
		PUSH_ERROR("failed to setup reals index");
		memory_bfree(mctx, reals, sizeof(struct real) * real_count);
		return -1;
	}

	uint32_t *reals_index = ADDR_OF(&handler->reals_index);

	size_t real_ph_idx = 0;
	for (size_t i = 0; i < config->vs_count; ++i) {
		struct named_vs_config *vs_config = &config->vs[i];
		for (size_t j = 0; j < vs_config->config.real_count; ++j) {
			struct named_real_config *real_config =
				&vs_config->config.reals[j];
			struct real *real = &reals[real_ph_idx];
			if (real_init(
				    real,
				    state,
				    real_config,
				    registry
			    ) != 0) {
				// failed to init real
				PUSH_ERROR(
					"virtual service at index %zu: failed "
					"to initialize real at index %zu",
					i,
					j
				);
				memory_bfree(
					mctx,
					reals,
					sizeof(struct real) * real_count
				);
				return -1;
			}
			struct real_state *real_state = ADDR_OF(&real->state);
			reals_index[real_state->registry_idx] = real_ph_idx;
			++real_ph_idx;
		}
	}

	return 0;
}

static int
init_vs(struct packet_handler *handler,
	struct balancer_state *state,
	struct memory_context *mctx,
	struct packet_handler_config *config,
	struct counter_registry *registry) {
	handler->vs_count = config->vs_count;
	struct vs *virtual_services =
		memory_balloc(mctx, sizeof(struct vs) * config->vs_count);
	if (virtual_services == NULL) {
		NEW_ERROR("failed to allocate virtual services");
		return -1;
	}
	SET_OFFSET_OF(&handler->vs, virtual_services);

	// allocate virtual services index
	handler->vs_index_count = balancer_state_vs_count(state);
	uint32_t *vs_index = memory_balloc(mctx, sizeof(uint32_t) * handler->vs_index_count);
	if (vs_index == NULL) {
		memory_bfree(mctx, virtual_services, sizeof(struct vs) * config->vs_count);
		NEW_ERROR("failed to allocate virtual services index");
		return -1;
	}
	SET_OFFSET_OF(&handler->vs_index, vs_index);

	memset(vs_index, (uint32_t)-1, sizeof(uint32_t) * config->vs_count);

	size_t reals_idx = 0;
	struct real *reals = ADDR_OF(&handler->reals);
	for (size_t i = 0; i < config->vs_count; ++i) {
		if (vs_view_init(
			    &virtual_services[i],
			    reals + reals_idx,
			    state,
			    &config->vs[i],
			    registry,
			    mctx
		    ) != 0) {
			PUSH_ERROR(
				"failed to setup virtual service at index %zu",
				i
			);
			for (size_t j = 0; j < i; ++j) {
				vs_view_free(&virtual_services[j], mctx);
			}
			return -1;
		}
		reals_idx += config->vs[i].config.real_count;
		struct vs_state *vs_state = ADDR_OF(&virtual_services[i].state);
		vs_index[vs_state->registry_idx] = i;
	}
	return 0;
}

struct packet_handler *
packet_handler_setup(
	struct agent *agent,
	const char *name,
	struct packet_handler_config *config,
	struct balancer_state *state
) {
	struct memory_context *mctx = &agent->memory_context;
	struct packet_handler *handler =
		memory_balloc(mctx, sizeof(struct packet_handler));
	if (handler == NULL) {
		NEW_ERROR("failed to allocate packet handler");
		return NULL;
	}
	memset(handler, 0, sizeof(struct packet_handler));
	SET_OFFSET_OF(&handler->state, state);

	memcpy(&handler->sessions_timeouts,
	       &config->sessions_timeouts,
	       sizeof(struct sessions_timeouts));

	if (cp_module_init(&handler->cp_module, agent, "balancer", name) != 0) {
		PUSH_ERROR("failed to initialize controlplane module");
		goto free_handler;
	}

	struct counter_registry *counter_registry =
		&handler->cp_module.counter_registry;

	if (init_counters(handler, counter_registry) != 0) {
		PUSH_ERROR("failed to setup balancer counters");
		goto free_handler;
	}

	if (init_sources(handler, mctx, config) != 0) {
		PUSH_ERROR("failed to setup source addresses");
		goto free_handler;
	}

	if (init_decaps(handler, mctx, config) != 0) {
		PUSH_ERROR("failed to setup decap addresses");
		goto free_handler;
	}

	if (init_reals(handler, state, mctx, config, counter_registry) != 0) {
		PUSH_ERROR("failed to setup reals");
		goto free_decap;
	}

	if (init_vs(handler, state, mctx, config, counter_registry) != 0) {
		PUSH_ERROR("failed to setup virtual services");
		goto free_reals;
	}

	struct cp_module *cp_module = &handler->cp_module;
	if (agent_update_modules(agent, 1, &cp_module) != 0) {
		PUSH_ERROR("failed to update controlplane modules");
		goto free_vs;
	}

	return handler;

free_vs:
	memory_bfree(
		mctx,
		ADDR_OF(&handler->vs),
		sizeof(struct vs) * handler->vs_count
	);
	memory_bfree(mctx, ADDR_OF(&handler->vs_index), sizeof(uint32_t) * handler->vs_index_count);

free_reals:
	memory_bfree(
		mctx,
		ADDR_OF(&handler->reals),
		sizeof(struct real) * handler->real_count
	);
	memory_bfree(mctx, ADDR_OF(&handler->reals_index), sizeof(uint32_t) * handler->reals_index_count);

free_decap:
	lpm_free(&handler->decap_ipv4);
	lpm_free(&handler->decap_ipv6);

free_handler:
	memory_bfree(mctx, handler, sizeof(struct packet_handler));

	return NULL;
}

////////////////////////////////////////////////////////////////////////////////

static void
fill_real_stats(
	size_t real_registry_idx,
	struct balancer_state *state,
	struct named_real_stats *real_stats,
	struct counter_handle *counter
) {
	struct real_state *real =
		balancer_state_get_real_by_idx(state, real_registry_idx);
	real_stats->identifier = real->identifier;
	counter_handle_accum(
		(uint64_t *)&real_stats->stats,
		state->workers,
		counter->size,
		counter->value_handle
	);
}

static void
fill_vs_stats(
	size_t vs_registry_idx,
	struct balancer_state *state,
	struct named_vs_stats *vs_stats,
	struct counter_handle *counter
) {
	struct vs_state *vs = balancer_state_get_vs_by_idx(state, vs_registry_idx);
	vs_stats->identifier = vs->identifier;
	counter_handle_accum(
		(uint64_t *)&vs_stats->stats,
		state->workers,
		counter->size,
		counter->value_handle
	);
}

static void
fill_balancer_stats(
	struct balancer_stats *stats,
	const size_t workers,
	struct counter_handle *counter,
	size_t *vs_count,
	size_t *real_count
) {
	if (strcmp(counter->name, common_module_counter_name) ==
	    0) { // common module counter
		counter_handle_accum(
			(uint64_t *)&stats->common,
			workers,
			counter->size,
			counter->value_handle
		);
	} else if (strcmp(counter->name, icmp_v4_module_counter_name) ==
		   0) { // icmp module counter
		counter_handle_accum(
			(uint64_t *)&stats->icmp_ipv4,
			workers,
			counter->size,
			counter->value_handle
		);
	} else if (strcmp(counter->name, icmp_v6_module_counter_name) == 0) {
		counter_handle_accum(
			(uint64_t *)&stats->icmp_ipv6,
			workers,
			counter->size,
			counter->value_handle
		);
	} else if (strcmp(counter->name, l4_module_counter_name) ==
		   0) { // l4 module counter
		counter_handle_accum(
			(uint64_t *)&stats->l4,
			workers,
			counter->size,
			counter->value_handle
		);
	} else if (counter_to_vs_registry_idx(counter) != -1) { // vs counter
		++*vs_count;
	} else if (counter_to_real_registry_idx(counter) !=
		   -1) { // real counter
		++*real_count;
	} else { // impossible
		assert(false);
	}
}

void
packet_handler_fill_stats(
	struct packet_handler *handler,
	struct balancer_stats *stats,
	struct packet_handler_ref *ref
) {
	struct agent *agent = ADDR_OF(&handler->cp_module.agent);
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);

	const char *module = handler->cp_module.name;

	struct counter_handle_list *counter_handles = yanet_get_module_counters(
		dp_config,
		ref->device,
		ref->pipeline,
		ref->function,
		ref->chain,
		"balancer",
		module
	);
	assert(counter_handles != NULL);

	const size_t instances = counter_handles->instance_count;

	// find common, icmp and l4 module counters
	// also, calculate number of vs and real counters.

	stats->vs_count = 0;
	stats->real_count = 0;

	for (size_t i = 0; i < counter_handles->count; ++i) {
		struct counter_handle *counter = &counter_handles->counters[i];
		fill_balancer_stats(
			stats,
			instances,
			counter,
			&stats->vs_count,
			&stats->real_count
		);
	}

	struct balancer_state *state = ADDR_OF(&handler->state);

	struct named_vs_stats *vs_stats =
		malloc(sizeof(struct named_vs_stats) * stats->vs_count);
	struct named_real_stats *real_stats =
		malloc(sizeof(struct named_real_stats) * stats->real_count);

	for (size_t i = 0; i < counter_handles->count; ++i) {
		struct counter_handle *counter = &counter_handles->counters[i];
		ssize_t vs_registry_idx = counter_to_vs_registry_idx(counter);
		if (vs_registry_idx != -1) {
			fill_vs_stats(
				vs_registry_idx,
				state,
				&vs_stats[vs_registry_idx],
				counter
			);
			continue;
		}
		ssize_t real_registry_idx =
			counter_to_real_registry_idx(counter);
		if (real_registry_idx != -1) {
			fill_real_stats(
				real_registry_idx,
				state,
				&real_stats[real_registry_idx],
				counter
			);
			continue;
		}
	}
}