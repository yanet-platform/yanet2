#include "handler.h"
#include "api/balancer.h"
#include "api/vs.h"
#include "common/lpm.h"
#include "common/memory.h"

#include "common/memory_address.h"
#include "common/network.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/diag/diag.h"

#include <assert.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "api/handler.h"
#include "counters/counters.h"
#include "state/state.h"

#include "real.h"
#include "vs.h"

#include "filter/compiler.h"
#include "filter/rule.h"

////////////////////////////////////////////////////////////////////////////////

// Declare filter compiler signatures for VS lookup tables
FILTER_COMPILER_DECLARE(vs_v4_sig, net4_dst, port_dst, proto);
FILTER_COMPILER_DECLARE(vs_v6_sig, net6_dst, port_dst, proto);

////////////////////////////////////////////////////////////////////////////////

extern uint64_t
register_common_counter(struct counter_registry *registry);

extern uint64_t
register_icmp_v4_counter(struct counter_registry *registry);

extern uint64_t
register_icmp_v6_counter(struct counter_registry *registry);

extern uint64_t
register_l4_counter(struct counter_registry *registry);

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
	uint32_t *reals_index =
		memory_balloc(mctx, sizeof(uint32_t) * registry_reals_count);
	if (reals_index == NULL && registry_reals_count > 0) {
		NEW_ERROR("failed to allocate memory for reals index");
		return -1;
	}

	memset(reals_index,
	       INDEX_INVALID,
	       sizeof(uint32_t) * registry_reals_count);
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
	handler->reals_count = real_count;
	struct real *reals =
		memory_balloc(mctx, sizeof(struct real) * real_count);
	if (reals == NULL && real_count > 0) {
		NEW_ERROR("failed to allocate memory for reals");
		return -1;
	}
	memset(reals, 0, sizeof(struct real) * real_count);
	SET_OFFSET_OF(&handler->reals, reals);

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
				    &vs_config->identifier,
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
			++real_ph_idx;
		}
	}

	// setup reals index
	if (setup_reals_index(handler, mctx) != 0) {
		PUSH_ERROR("failed to setup reals index");
		memory_bfree(mctx, reals, sizeof(struct real) * real_count);
		return -1;
	}

	uint32_t *reals_index = ADDR_OF(&handler->reals_index);

	real_ph_idx = 0;
	for (size_t i = 0; i < config->vs_count; ++i) {
		struct named_vs_config *vs_config = &config->vs[i];
		for (size_t j = 0; j < vs_config->config.real_count; ++j) {
			struct real *real = &reals[real_ph_idx];
			reals_index[real->registry_idx] = real_ph_idx;
			++real_ph_idx;
		}
	}

	return 0;
}

static int
init_announce_lpms(
	struct packet_handler *handler,
	struct memory_context *mctx,
	struct packet_handler_config *config
) {
	// init ipv4 announce addresses
	if (lpm_init(&handler->announce_ipv4, mctx) != 0) {
		NEW_ERROR("failed to allocate container for announce IPv4 "
			  "addresses");
		return -1;
	}

	// Populate announce_ipv4 with all IPv4 virtual service addresses
	for (size_t i = 0; i < config->vs_count; ++i) {
		struct named_vs_config *vs_config = &config->vs[i];
		if (vs_config->identifier.ip_proto == IPPROTO_IP) {
			struct net4_addr *addr =
				(struct net4_addr *)&vs_config->identifier.addr;
			if (lpm4_insert(
				    &handler->announce_ipv4,
				    addr->bytes,
				    addr->bytes,
				    1
			    ) != 0) {
				lpm_free(&handler->announce_ipv4);
				NEW_ERROR(
					"failed to insert announce IPv4 "
					"address for VS at index %zu",
					i
				);
				return -1;
			}
		}
	}

	// init ipv6 announce addresses
	if (lpm_init(&handler->announce_ipv6, mctx) != 0) {
		NEW_ERROR("failed to allocate container for announce IPv6 "
			  "addresses");
		lpm_free(&handler->announce_ipv4);
		return -1;
	}

	// Populate announce_ipv6 with all IPv6 virtual service addresses
	for (size_t i = 0; i < config->vs_count; ++i) {
		struct named_vs_config *vs_config = &config->vs[i];
		if (vs_config->identifier.ip_proto == IPPROTO_IPV6) {
			struct net6_addr *addr =
				(struct net6_addr *)&vs_config->identifier.addr;
			if (lpm8_insert(
				    &handler->announce_ipv6,
				    addr->bytes,
				    addr->bytes,
				    1
			    ) != 0) {
				lpm_free(&handler->announce_ipv4);
				lpm_free(&handler->announce_ipv6);
				NEW_ERROR(
					"failed to insert announce IPv6 "
					"address for VS at index %zu",
					i
				);
				return -1;
			}
		}
	}

	return 0;
}

static int
init_vs_filters(
	struct packet_handler *handler,
	struct memory_context *mctx,
	struct packet_handler_config *config
) {
	// Build filter rules for IPv4 and IPv6 virtual services
	struct filter_rule *v4_rules = NULL;
	struct filter_rule *v6_rules = NULL;
	size_t v4_count = 0;
	size_t v6_count = 0;

	// Count IPv4 and IPv6 virtual services
	for (size_t i = 0; i < config->vs_count; ++i) {
		if (config->vs[i].identifier.ip_proto == IPPROTO_IP) {
			v4_count++;
		} else {
			v6_count++;
		}
	}

	// Allocate rule arrays
	if (v4_count > 0) {
		v4_rules = calloc(v4_count, sizeof(struct filter_rule));
		if (v4_rules == NULL) {
			NEW_ERROR("failed to allocate IPv4 filter rules");
			return -1;
		}
	}

	if (v6_count > 0) {
		v6_rules = calloc(v6_count, sizeof(struct filter_rule));
		if (v6_rules == NULL) {
			free(v4_rules);
			NEW_ERROR("failed to allocate IPv6 filter rules");
			return -1;
		}
	}

	// Build filter rules
	size_t v4_idx = 0;
	size_t v6_idx = 0;
	for (size_t i = 0; i < config->vs_count; ++i) {
		struct named_vs_config *vs_config = &config->vs[i];

		struct filter_rule *rule;
		size_t *idx;

		if (vs_config->identifier.ip_proto == IPPROTO_IP) {
			rule = &v4_rules[v4_idx];
			idx = &v4_idx;
		} else {
			rule = &v6_rules[v6_idx];
			idx = &v6_idx;
		}

		memset(rule, 0, sizeof(struct filter_rule));

		// Set net4 or net6 destination
		if (vs_config->identifier.ip_proto == IPPROTO_IP) {
			rule->net4.dst_count = 1;
			rule->net4.dsts = calloc(1, sizeof(struct net4));
			if (rule->net4.dsts == NULL) {
				goto cleanup_error;
			}
			struct net4_addr *addr =
				(struct net4_addr *)&vs_config->identifier.addr;
			memcpy(rule->net4.dsts[0].addr, addr->bytes, NET4_LEN);
			memset(rule->net4.dsts[0].mask, 0xFF, NET4_LEN);
		} else {
			rule->net6.dst_count = 1;
			rule->net6.dsts = calloc(1, sizeof(struct net6));
			if (rule->net6.dsts == NULL) {
				goto cleanup_error;
			}
			struct net6_addr *addr =
				(struct net6_addr *)&vs_config->identifier.addr;
			memcpy(rule->net6.dsts[0].addr, addr->bytes, NET6_LEN);
			memset(rule->net6.dsts[0].mask, 0xFF, NET6_LEN);
		}

		// Set transport (port_dst and proto)
		rule->transport.dst_count = 1;
		rule->transport.dsts =
			calloc(1, sizeof(struct filter_port_range));
		if (rule->transport.dsts == NULL) {
			goto cleanup_error;
		}

		// For PureL3 mode, match all ports (0-65535)
		// Otherwise, match only the specific port
		if (vs_config->config.flags & VS_PURE_L3_FLAG) {
			rule->transport.dsts[0].from = 0;
			rule->transport.dsts[0].to = 65535;
		} else {
			rule->transport.dsts[0].from =
				vs_config->identifier.port;
			rule->transport.dsts[0].to = vs_config->identifier.port;
		}

		rule->transport.proto.proto =
			vs_config->identifier.transport_proto;
		rule->transport.proto.enable_bits = 0;
		rule->transport.proto.disable_bits = 0;

		// Action: VS index in handler
		rule->action = i;

		(*idx)++;
	}

	// Compile filters
	int res = 0;
	res = FILTER_INIT(&handler->vs_v4, vs_v4_sig, v4_rules, v4_count, mctx);
	if (res != 0) {
		NEW_ERROR("failed to compile IPv4 VS filter");
	}

	if (res == 0) {
		res = FILTER_INIT(
			&handler->vs_v6, vs_v6_sig, v6_rules, v6_count, mctx
		);
		if (res != 0) {
			NEW_ERROR("failed to compile IPv6 VS filter");
			if (v4_count > 0) {
				FILTER_FREE(&handler->vs_v4, vs_v4_sig);
			}
		}
	}

	// Cleanup rule arrays
	for (size_t i = 0; i < v4_count; ++i) {
		free(v4_rules[i].net4.dsts);
		free(v4_rules[i].transport.dsts);
	}
	free(v4_rules);

	for (size_t i = 0; i < v6_count; ++i) {
		free(v6_rules[i].net6.dsts);
		free(v6_rules[i].transport.dsts);
	}
	free(v6_rules);

	return res;

cleanup_error:
	// Cleanup on error
	for (size_t i = 0; i < v4_idx; ++i) {
		free(v4_rules[i].net4.dsts);
		free(v4_rules[i].transport.dsts);
	}
	for (size_t i = 0; i < v6_idx; ++i) {
		free(v6_rules[i].net6.dsts);
		free(v6_rules[i].transport.dsts);
	}
	free(v4_rules);
	free(v6_rules);

	NEW_ERROR("failed to allocate filter rule components");

	return -1;
}

static int
init_vs(struct packet_handler *handler,
	struct balancer_state *state,
	struct memory_context *mctx,
	struct packet_handler_config *config,
	struct counter_registry *registry) {
	// Initialize announce LPMs
	if (init_announce_lpms(handler, mctx, config) != 0) {
		PUSH_ERROR("failed to initialize announce LPMs");
		return -1;
	}

	// Initialize VS filters
	if (init_vs_filters(handler, mctx, config) != 0) {
		PUSH_ERROR("failed to initialize VS filters");
		lpm_free(&handler->announce_ipv4);
		lpm_free(&handler->announce_ipv6);
		return -1;
	}

	// create virtual services
	handler->vs_count = config->vs_count;
	struct vs *vs =
		memory_balloc(mctx, sizeof(struct vs) * config->vs_count);
	if (vs == NULL && config->vs_count > 0) {
		NEW_ERROR("failed to allocate virtual services");
		goto free_filters;
	}
	SET_OFFSET_OF(&handler->vs, vs);

	size_t reals_idx = 0;
	struct real *reals = ADDR_OF(&handler->reals);
	for (size_t i = 0; i < config->vs_count; ++i) {
		if (vs_init(&vs[i],
			    reals_idx,
			    reals + reals_idx,
			    state,
			    &config->vs[i],
			    registry,
			    mctx) != 0) {
			PUSH_ERROR(
				"failed to setup virtual service at index %zu",
				i
			);
			for (size_t j = 0; j < i; ++j) {
				vs_free(&vs[j], mctx);
			}
			goto free_vs_array;
		}
		reals_idx += config->vs[i].config.real_count;
	}

	// allocate virtual services index
	handler->vs_index_count = balancer_state_vs_count(state);
	uint32_t *vs_index =
		memory_balloc(mctx, sizeof(uint32_t) * handler->vs_index_count);
	if (vs_index == NULL && handler->vs_index_count > 0) {
		NEW_ERROR("failed to allocate virtual services index");
		goto free_vs_array;
	}
	SET_OFFSET_OF(&handler->vs_index, vs_index);

	memset(vs_index, INDEX_INVALID, sizeof(uint32_t) * config->vs_count);

	// init virtual service index
	for (size_t i = 0; i < config->vs_count; ++i) {
		vs_index[vs[i].registry_idx] = i;
	}

	return 0;

free_vs_array:
	memory_bfree(mctx, vs, sizeof(struct vs) * config->vs_count);

free_filters:
	if (config->vs_count > 0) {
		// Free filters if they were initialized
		size_t v4_count = 0, v6_count = 0;
		for (size_t i = 0; i < config->vs_count; ++i) {
			if (config->vs[i].identifier.ip_proto == IPPROTO_IP) {
				v4_count++;
			} else {
				v6_count++;
			}
		}
		if (v4_count > 0) {
			FILTER_FREE(&handler->vs_v4, vs_v4_sig);
		}
		if (v6_count > 0) {
			FILTER_FREE(&handler->vs_v6, vs_v6_sig);
		}
	}
	lpm_free(&handler->announce_ipv4);
	lpm_free(&handler->announce_ipv6);
	return -1;
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
	memory_bfree(
		mctx,
		ADDR_OF(&handler->vs_index),
		sizeof(uint32_t) * handler->vs_index_count
	);

free_reals:
	memory_bfree(
		mctx,
		ADDR_OF(&handler->reals),
		sizeof(struct real) * handler->reals_count
	);
	memory_bfree(
		mctx,
		ADDR_OF(&handler->reals_index),
		sizeof(uint32_t) * handler->reals_index_count
	);

free_decap:
	lpm_free(&handler->decap_ipv4);
	lpm_free(&handler->decap_ipv6);

free_handler:
	memory_bfree(mctx, handler, sizeof(struct packet_handler));

	return NULL;
}

int
packet_handler_real_idx(
	struct packet_handler *handler,
	struct real_identifier *real,
	struct real_ph_index *real_ph_index
) {
	struct balancer_state *state = ADDR_OF(&handler->state);

	struct real_state *real_state = balancer_state_find_real(state, real);
	if (real_state == NULL) {
		return -1;
	}

	uint32_t *vs_idx = ADDR_OF(&handler->vs_index);
	real_ph_index->vs_idx = vs_idx[real_state->vs_registry_idx];

	struct vs *vss = ADDR_OF(&handler->vs);
	struct vs *vs = &vss[real_ph_index->vs_idx];

	uint32_t *reals_idx = ADDR_OF(&handler->reals_index);
	real_ph_index->real_idx =
		reals_idx[real_state->registry_idx] - vs->first_real_idx;

	return 0;
}