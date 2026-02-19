#include "handler.h"
#include "api/balancer.h"
#include "api/vs.h"
#include "common/lpm.h"
#include "common/memory.h"
#include "common/swap.h"

#include "common/memory_address.h"
#include "common/network.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/diag/diag.h"

#include <assert.h>
#include <netinet/in.h>
#include <sched.h>
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
FILTER_COMPILER_DECLARE(vs_lookup_ipv4, net4_fast_dst, port_dst, proto);
FILTER_COMPILER_DECLARE(vs_lookup_ipv6, net6_fast_dst, port_dst, proto);

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
	struct counter_registry *registry,
	size_t *initial_vs_idx
) {
	size_t real_count = 0;
	for (size_t i = 0; i < config->vs_count; ++i) {
		real_count += config->vs[i].config.real_count;
	}
	handler->reals_count = real_count;
	struct real *reals =
		memory_balloc(mctx, sizeof(struct real) * real_count);
	if (reals == NULL && real_count > 0) {
		NEW_ERROR("no memory");
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
					"service at index %zu: real at index "
					"%zu",
					initial_vs_idx[i],
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

////////////////////////////////////////////////////////////////////////////////

static int
init_transport_rule(
	struct filter_rule *rule, struct named_vs_config *vs_config
) {
	rule->transport.dst_count = 1;
	rule->transport.dsts = calloc(1, sizeof(struct filter_port_range));

	// For PureL3 mode, match all ports (0-65535)
	// Otherwise, match only the specific port
	if (vs_config->config.flags & VS_PURE_L3_FLAG) {
		rule->transport.dsts[0].from = 0;
		rule->transport.dsts[0].to = 65535;
	} else {
		rule->transport.dsts[0].from = vs_config->identifier.port;
		rule->transport.dsts[0].to = vs_config->identifier.port;
	}

	if (vs_config->identifier.transport_proto != IPPROTO_TCP ||
	    vs_config->identifier.transport_proto != IPPROTO_UDP) {
		NEW_ERROR(
			"unsupported transport protocol %d (only TCP (%d) and "
			"UDP (%d) are supported)",
			vs_config->identifier.transport_proto,
			IPPROTO_TCP,
			IPPROTO_UDP
		);
		return -1;
	}

	rule->transport.proto.proto = vs_config->identifier.transport_proto;
	rule->transport.proto.enable_bits = 0;
	rule->transport.proto.disable_bits = 0;
	return 0;
}

static void
init_dst_rule(struct filter_rule *rule, struct named_vs_config *vs_config) {
	memset(&rule->net6, 0, sizeof(rule->net6));
	memset(&rule->net4, 0, sizeof(rule->net4));
	if (vs_config->identifier.ip_proto == IPPROTO_IPV6) {
		rule->net6.dst_count = 1;
		rule->net6.dsts = malloc(sizeof(struct net6));
		struct net6 *n = &rule->net6.dsts[0];
		memcpy(n->addr, vs_config->identifier.addr.v6.bytes, NET6_LEN);
		memset(n->mask, 0xFF, NET6_LEN);
	} else { // ipv4
		rule->net4.dst_count = 1;
		rule->net4.dsts = malloc(sizeof(struct net4));
		struct net4 *n = &rule->net4.dsts[0];
		memcpy(n->addr, vs_config->identifier.addr.v4.bytes, NET4_LEN);
		memset(n->mask, 0xFF, NET4_LEN);
	}
}

static int
make_filter_rules(
	struct filter_rule **result_rules,
	size_t count,
	struct named_vs_config *vs_configs,
	size_t *vs_initial_idx
) {
	*result_rules = NULL;
	struct filter_rule *rules = malloc(sizeof(struct filter_rule) * count);
	for (size_t rule_idx = 0; rule_idx < count; ++rule_idx) {
		const size_t vs_idx = rule_idx;
		init_dst_rule(rules + rule_idx, vs_configs + vs_idx);
		if (init_transport_rule(
			    rules + rule_idx, vs_configs + vs_idx
		    ) != 0) {
			free(rules);
			PUSH_ERROR(
				"service at index %zu", vs_initial_idx[vs_idx]
			);
			return -1;
		}
		rules[rule_idx].action = rule_idx;
	}
	*result_rules = rules;
	return 0;
}

static inline struct vs *
find_vs_in_packet_handler_vs(
	struct packet_handler_vs *packet_handler_vs, struct vs *vs
) {
	if (packet_handler_vs == NULL) {
		return NULL;
	}

	uint32_t *vs_index = ADDR_OF(&packet_handler_vs->vs_index);
	size_t vs_index_count = packet_handler_vs->vs_index_size;

	if (vs->registry_idx >= vs_index_count) {
		return NULL;
	}

	struct vs *services = ADDR_OF(&packet_handler_vs->vs);

	uint32_t vs_idx = vs_index[vs->registry_idx];
	if (vs_idx == INDEX_INVALID) {
		return NULL;
	}

	return services;
}

static int
register_virtual_services(
	size_t vs_count,
	const size_t *inital_vs_idx,
	struct named_vs_config *configs,
	struct balancer_state *state,
	struct packet_handler *prev_handler,
	size_t *match
) {
	uint32_t *prev_vs_index =
		prev_handler != NULL ? ADDR_OF(&prev_handler->vs_index) : NULL;
	size_t prev_vs_index_count =
		prev_handler != NULL ? prev_handler->vs_index_size : 0;

	for (size_t vs_idx = 0; vs_idx < vs_count; ++vs_idx) {
		struct named_vs_config *vs_config = &configs[vs_idx];
		struct vs_state *vs_state = balancer_state_find_or_insert_vs(
			state, &vs_config->identifier
		);
		if (vs_state == NULL) {
			PUSH_ERROR("at index %zu", inital_vs_idx[vs_idx]);
			return -1;
		}

		size_t vs_registry_idx = vs_state->registry_idx;
		if (vs_registry_idx < prev_vs_index_count &&
		    prev_vs_index[vs_registry_idx] != INDEX_INVALID) {
			*match += 1;
		}
	}

	return 0;
}

static int
validate_vs_config(struct named_vs_config *config) {
	int proto = config->identifier.ip_proto;
	if (proto != IPPROTO_IP && proto != IPPROTO_IPV6) {
		NEW_ERROR(
			"network protocol is invalid: got %d, but only IPv4 "
			"(%d) and IPv6 (%d) are supported",
			proto,
			IPPROTO_IP,
			IPPROTO_IPV6
		);
		return -1;
	}

	if (config->identifier.transport_proto != IPPROTO_TCP &&
	    config->identifier.transport_proto != IPPROTO_UDP) {
		NEW_ERROR(
			"transport protocol is invalid: got %d, but only TCP "
			"(%d) and UDP (%d) are supported",
			config->identifier.transport_proto,
			IPPROTO_TCP,
			IPPROTO_UDP
		);
		return -1;
	}

	// TODO: better validation

	return 0;
}

static void
swap_vs_configs(
	size_t *initial_vs_idx,
	struct named_vs_config *configs,
	size_t left_idx,
	size_t right_idx
) {
	SWAP(configs + left_idx, configs + right_idx);
	SWAP(initial_vs_idx + left_idx, initial_vs_idx + right_idx);
}

static int
validate_and_reorder_vs_configs(
	size_t *initial_vs_idx,
	size_t count,
	struct named_vs_config *configs,
	size_t *ipv4_count,
	size_t *ipv6_count
) {
	// move ipv4 services first, and ipv6 then.

	ssize_t last_ipv6 = -1;
	for (size_t idx = 0; idx < count; ++idx) {
		struct named_vs_config *current = &configs[idx];

		// validate service
		if (validate_vs_config(current) != 0) {
			PUSH_ERROR("at index %zu", idx);
			return -1;
		}

		int proto = current->identifier.ip_proto;

		if (proto == IPPROTO_IPV6) {
			// IPv6 service
			*ipv6_count += 1;
			if (last_ipv6 == -1) {
				last_ipv6 = idx;
			}
			continue;
		}

		// IPv4 service
		*ipv4_count += 1;
		if (last_ipv6 == -1) {
			continue;
		}

		swap_vs_configs(initial_vs_idx, configs, idx, last_ipv6);

		last_ipv6 += 1;
	}

	return 0;
}

// for network proto (IPv4 or IPv6), VS filter can be reused
// if and only if the virtual services from current config
// and the previous config matches as sets
static int
can_reuse_filter(int current_vs_count, int prev_vs_count, int match_count) {
	// all virtual services are unique, it is validated on packet handler
	// update
	return current_vs_count == prev_vs_count &&
	       current_vs_count == match_count;
}

static struct packet_handler_vs *
get_packet_handler_vs(struct packet_handler *handler, int proto) {
	return handler == NULL ? NULL
			       : (proto == IPPROTO_IP ? &handler->vs_ipv4
						      : &handler->vs_ipv6);
}

// register virtual services and checks if VS filter
// can be reused
static int
register_and_prepare_vs(
	struct packet_handler *handler,
	struct packet_handler *prev_handler,
	int proto,
	size_t vs_count,
	struct named_vs_config *vs_configs,
	size_t *initial_vs_idx,
	struct vs *virtual_services,
	struct balancer_state *state,
	struct balancer_update_info *update_info,
	int *reuse_filter
) {
	// only IPv4 and IPv6 are supported
	assert(proto == IPPROTO_IP || proto == IPPROTO_IPV6);

	// first, register virtual services in balancer state registry
	// and get number of services matching with
	// services from the previous config
	size_t match = 0;
	if (register_virtual_services(
		    vs_count,
		    initial_vs_idx,
		    vs_configs,
		    state,
		    prev_handler,
		    &match
	    ) != 0) {
		PUSH_ERROR("registration failed");
		return -1;
	}

	// init some fields of the packet_handler_vs for this protocol:
	// - vs_count
	// - vs
	struct packet_handler_vs *packet_handler_vs =
		get_packet_handler_vs(handler, proto);
	packet_handler_vs->vs_count = vs_count;
	SET_OFFSET_OF(&packet_handler_vs->vs, virtual_services);

	// prev handler is optional
	struct packet_handler_vs *prev_packet_handler_vs =
		get_packet_handler_vs(prev_handler, proto);

	// check if VS filter for this protocol can be reused
	*reuse_filter = can_reuse_filter(
		vs_count,
		prev_packet_handler_vs == NULL
			? 0
			: prev_packet_handler_vs->vs_count,
		match
	);
	if (update_info != NULL) {
		*(proto == IPPROTO_IPV6 ? &update_info->vs_ipv6_matcher_reused
					: &update_info->vs_ipv4_matcher_reused
		) = *reuse_filter;
	}

	// to reuse filter for network protocol, the VS indices in
	// packet_handler_vs MUST match with the corresponding indices in the
	// previous config. this is because the VS matching mechanism
	if (*reuse_filter) {
		// permute VS configs according to indices in the previous
		// config

		uint32_t *prev_vs_index =
			ADDR_OF(&prev_packet_handler_vs->vs_index);
		size_t prev_vs_index_size =
			prev_packet_handler_vs->vs_index_size;
		(void)prev_vs_index_size;

		for (size_t vs_idx = 0; vs_idx < vs_count; ++vs_idx) {
			struct vs_state *vs_state = balancer_state_find_vs(
				state, &vs_configs[vs_idx].identifier
			);
			assert(vs_state != NULL);

			size_t vs_registry_idx = vs_state->registry_idx;
			assert(vs_registry_idx < prev_vs_index_size);

			uint32_t position = prev_vs_index[vs_registry_idx];

			swap_vs_configs(
				initial_vs_idx, vs_configs, vs_idx, position
			);
		}
	}

	return 0;
}

static void
free_rules(size_t rules_count, struct filter_rule *rules) {
	for (size_t rule_idx = 0; rule_idx < rules_count; ++rule_idx) {
		struct filter_rule *rule = rules + rule_idx;
		free(rule->net4.dsts);
		free(rule->net6.dsts);
		free(rule->transport.dsts);
	}
	free(rules);
}

static int
build_filter(
	struct packet_handler_vs *packet_handler_vs,
	size_t *initial_vs_idx,
	struct named_vs_config *vs_configs,
	struct memory_context *mctx,
	int proto
) {
	struct filter *filter = memory_balloc(mctx, sizeof(struct filter));
	if (filter == NULL) {
		NEW_ERROR("no memory");
		return -1;
	}

	struct filter_rule *rules = NULL;
	const size_t vs_count = packet_handler_vs->vs_count;
	if (make_filter_rules(&rules, vs_count, vs_configs, initial_vs_idx) !=
	    0) {
		PUSH_ERROR("invalid VS configs");
		memory_bfree(mctx, filter, sizeof(struct filter));
		return -1;
	}

	const size_t rules_count = vs_count;
	if (proto == IPPROTO_IPV6) {
		if (FILTER_INIT(
			    filter, vs_lookup_ipv6, rules, rules_count, mctx
		    ) != 0) {
			memory_bfree(mctx, filter, sizeof(struct filter));
			free_rules(rules_count, rules);
			NEW_ERROR("no memory");
			return -1;
		}
	} else {
		if (FILTER_INIT(
			    filter, vs_lookup_ipv4, rules, rules_count, mctx
		    ) != 0) {
			memory_bfree(mctx, filter, sizeof(struct filter));
			free_rules(rules_count, rules);
			NEW_ERROR("no memory");
			return -1;
		}
	}

	free_rules(rules_count, rules);

	return 0;
}

static int
init_packet_handler_vs(
	struct packet_handler *handler,
	int proto,
	struct balancer_state *state,
	struct memory_context *mctx,
	struct named_vs_config *vs_configs,
	struct counter_registry *registry,
	struct packet_handler *prev_handler,
	struct real *reals,
	size_t *reals_counter,
	struct balancer_update_info *update_info,
	size_t *initial_vs_idx
) {
	// only IPv4 and IPv6 are supported
	assert(proto == IPPROTO_IP || proto == IPPROTO_IPV6);

	// prev packet handler is optional
	struct packet_handler_vs *prev_packet_handler_vs =
		get_packet_handler_vs(prev_handler, proto);

	// find packet handler vs for this protocol
	struct packet_handler_vs *packet_handler_vs =
		get_packet_handler_vs(handler, proto);
	size_t vs_count = handler->vs_count;
	struct vs *virtual_services = ADDR_OF(&packet_handler_vs->vs);

	// initialize virtual services
	for (size_t vs_idx = 0; vs_idx < vs_count; ++vs_idx) {
		struct vs *current_vs = virtual_services + vs_idx;
		struct named_vs_config *current_vs_config = vs_configs + vs_idx;

		// set identifier
		current_vs->identifier = current_vs_config->identifier;
		struct vs_state *current_vs_state =
			balancer_state_find_vs(state, &current_vs->identifier);
		assert(current_vs_state != NULL);

		// set registry idx
		current_vs->registry_idx = current_vs_state->registry_idx;

		// try to find this virtual service in previous config, can be
		// null
		struct vs *prev_vs = find_vs_in_packet_handler_vs(
			prev_packet_handler_vs, current_vs
		);

		if (vs_with_identifier_and_registry_idx_init(
			    current_vs,
			    prev_vs,
			    *reals_counter,
			    reals,
			    state,
			    current_vs_config,
			    registry,
			    mctx,
			    update_info
		    ) != 0) {
			PUSH_ERROR(
				"service at index %zu", initial_vs_idx[vs_idx]
			);
			// TODO: free allocated memory
			return -1;
		}

		// increase reals counter
		*reals_counter += current_vs->reals_count;
	}

	return 0;
}

static int
init_vs_filter(
	struct packet_handler_vs *packet_handler_vs,
	struct packet_handler_vs *prev_packet_handler_vs,
	struct named_vs_config *vs_configs,
	int reuse_filter,
	struct memory_context *mctx,
	size_t *initial_vs_idx,
	int proto
) {
	packet_handler_vs->filter_used = 0;
	if (reuse_filter) {
		// just reuse filter from the current packet handler
		EQUATE_OFFSET(
			&packet_handler_vs->filter,
			&prev_packet_handler_vs->filter
		);
		prev_packet_handler_vs->filter_used = 1;
	} else {
		if (build_filter(
			    packet_handler_vs,
			    initial_vs_idx,
			    vs_configs,
			    mctx,
			    proto
		    ) != 0) {
			PUSH_ERROR("build failed");
			return -1;
		}
	}
	return 0;
}

static int
init_announce(
	struct packet_handler_vs *handler,
	struct memory_context *mctx,
	struct named_vs_config *vs_configs,
	int proto
) {
	struct lpm *lpm = &handler->announce;
	if (lpm_init(lpm, mctx) != 0) {
		NEW_ERROR("no memory");
		return -1;
	}

	for (size_t vs_idx = 0; vs_idx < handler->vs_count; ++vs_idx) {
		struct named_vs_config *vs_config = vs_configs + vs_idx;
		int res;
		if (proto == IPPROTO_IP) {
			res = lpm4_insert(
				lpm,
				vs_config->identifier.addr.v4.bytes,
				vs_config->identifier.addr.v4.bytes,
				1
			);
		} else {
			res = lpm8_insert(
				lpm,
				vs_config->identifier.addr.v6.bytes,
				vs_config->identifier.addr.v6.bytes,
				1
			);
		}
		if (res != 0) {
			lpm_free(lpm);
			NEW_ERROR("no memory");
			return -1;
		}
	}

	return 0;
}

static int
init_vs_and_reals(
	struct packet_handler *handler,
	struct balancer_state *state,
	struct memory_context *mctx,
	struct packet_handler_config *config,
	struct counter_registry *registry,
	struct packet_handler *prev_handler,
	struct balancer_update_info *update_info
) {
	// store initial indices of the virtual services
	// for informative errors output
	size_t *initial_vs_idx = malloc(handler->vs_count * sizeof(size_t));
	for (size_t idx = 0; idx < handler->vs_count; ++idx) {
		initial_vs_idx[idx] = idx;
	}

	// reorder vs configs: after that it contains from
	// prefix of IPv4 VS and suffix of IPv6 VS.
	size_t ipv4_count = 0;
	size_t ipv6_count = 0;
	if (validate_and_reorder_vs_configs(
		    initial_vs_idx,
		    config->vs_count,
		    config->vs,
		    &ipv4_count,
		    &ipv6_count
	    ) != 0) {
		PUSH_ERROR("invalid service config");
		goto free_initial_vs_idx_on_error;
	}

	// setup reals
	if (init_reals(
		    handler, state, mctx, config, registry, initial_vs_idx
	    ) != 0) {
		PUSH_ERROR("init reals");
		goto free_initial_vs_idx_on_error;
	}

	struct real *reals = ADDR_OF(&handler->reals);

	// allocate virtual services
	handler->vs_count = config->vs_count;
	struct vs *virtual_services =
		memory_balloc(mctx, sizeof(struct vs) * config->vs_count);
	if (virtual_services == NULL) {
		NEW_ERROR("no memory");
		goto free_initial_vs_idx_on_error;
	}

	// register and prepare IPv4 services
	int reuse_ipv4_filter = 0;
	if (register_and_prepare_vs(
		    handler,
		    prev_handler,
		    IPPROTO_IP,
		    ipv4_count,
		    config->vs,
		    initial_vs_idx,
		    virtual_services,
		    state,
		    update_info,
		    &reuse_ipv4_filter
	    ) != 0) {
		PUSH_ERROR("prepare IPv4 services");
		goto free_virtual_services_on_error;
	}

	// register and prepare IPv6 services
	int reuse_ipv6_filter = 0;
	if (register_and_prepare_vs(
		    handler,
		    prev_handler,
		    IPPROTO_IPV6,
		    ipv6_count,
		    config->vs + ipv4_count,
		    initial_vs_idx + ipv4_count,
		    virtual_services + ipv4_count,
		    state,
		    update_info,
		    &reuse_ipv6_filter
	    ) != 0) {
		PUSH_ERROR("prepare IPv6 services");
		goto free_virtual_services_on_error;
	}

	size_t reals_counter = 0;
	if (init_packet_handler_vs(
		    handler,
		    IPPROTO_IP,
		    state,
		    mctx,
		    config->vs,
		    registry,
		    prev_handler,
		    reals,
		    &reals_counter,
		    update_info,
		    initial_vs_idx
	    ) != 0) {
		PUSH_ERROR("initialize IPv4 services");
		goto free_virtual_services_on_error;
	}

	if (init_packet_handler_vs(
		    handler,
		    IPPROTO_IPV6,
		    state,
		    mctx,
		    config->vs + ipv4_count,
		    registry,
		    prev_handler,
		    reals,
		    &reals_counter,
		    update_info,
		    initial_vs_idx + ipv4_count
	    ) != 0) {
		PUSH_ERROR("initialize IPv6 services");
		goto free_virtual_services_on_error;
	}

	if (init_vs_filter(
		    &handler->vs_ipv4,
		    get_packet_handler_vs(prev_handler, IPPROTO_IP),
		    config->vs,
		    reuse_ipv4_filter,
		    mctx,
		    initial_vs_idx,
		    IPPROTO_IP
	    ) != 0) {
		PUSH_ERROR("initialize IPv4 VS matcher");
		goto free_virtual_services_on_error;
	}

	if (init_vs_filter(
		    &handler->vs_ipv6,
		    get_packet_handler_vs(prev_handler, IPPROTO_IPV6),
		    config->vs + ipv4_count,
		    reuse_ipv6_filter,
		    mctx,
		    initial_vs_idx + ipv4_count,
		    IPPROTO_IPV6
	    ) != 0) {
		PUSH_ERROR("initialize IPv6 VS matcher");
		goto free_virtual_services_on_error;
	}

	if (init_announce(
		    get_packet_handler_vs(handler, IPPROTO_IP),
		    mctx,
		    config->vs,
		    IPPROTO_IP
	    ) != 0) {
		PUSH_ERROR("initialize IPv4 announce");
		goto free_virtual_services_on_error;
	}

	if (init_announce(
		    get_packet_handler_vs(handler, IPPROTO_IPV6),
		    mctx,
		    config->vs + ipv4_count,
		    IPPROTO_IPV6
	    ) != 0) {
		PUSH_ERROR("initialize IPv6 announce");
		goto free_virtual_services_on_error;
	}

	// setup vs index
	size_t vs_index_size = balancer_state_vs_count(state);
	uint32_t *vs_index =
		memory_balloc(mctx, sizeof(uint32_t) * vs_index_size);
	if (vs_index == NULL && handler->vs_count > 0) {
		PUSH_ERROR("no memory");
		goto free_virtual_services_on_error;
	}
	memset(vs_index, INDEX_INVALID, sizeof(uint32_t) * vs_index_size);

	for (size_t vs_idx = 0; vs_idx < vs_index_size; vs_idx++) {
		struct vs *vs = virtual_services + vs_idx;
		if (vs_index[vs->registry_idx] != INDEX_INVALID) {
			NEW_ERROR(
				"service at index %zu matches with service at "
				"index %zu",
				initial_vs_idx[vs_idx],
				initial_vs_idx[vs_index[vs->registry_idx]]
			);
			goto free_initial_vs_idx_on_error;
		}
		vs_index[vs->registry_idx] = vs_idx;
	}

	free(initial_vs_idx);

	return 0;

free_virtual_services_on_error:
	memory_bfree(
		mctx, virtual_services, sizeof(struct vs) * config->vs_count
	);

free_initial_vs_idx_on_error:
	free(initial_vs_idx);

	return -1;
}

struct packet_handler *
packet_handler_setup(
	struct agent *agent,
	const char *name,
	struct packet_handler_config *config,
	struct balancer_state *state,
	struct packet_handler *prev_handler,
	struct balancer_update_info *update_info
) {
	// Initialize update_info if provided
	if (update_info != NULL) {
		memset(update_info, 0, sizeof(*update_info));
	}

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

	if (init_vs_and_reals(
		    handler,
		    state,
		    mctx,
		    config,
		    counter_registry,
		    prev_handler,
		    update_info
	    ) != 0) {
		PUSH_ERROR("failed to setup virtual services");
		goto free_decap;
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
		sizeof(uint32_t) * handler->vs_index_size
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
