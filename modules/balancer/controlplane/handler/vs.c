#include "api/vs.h"
#include "api/counter.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/network.h"

#include "compiler/declare.h"
#include "lib/controlplane/diag/diag.h"

#include "selector.h"
#include "vs.h"

#include "state/state.h"
#include "state/vs.h"

#include <assert.h>
#include <netinet/in.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>

#include "filter/compiler.h"

static int
setup_reals(
	struct vs *vs,
	struct vs_config *config,
	size_t first_real_idx,
	struct real *reals
) {
	vs->reals_count = config->real_count;
	vs->first_real_idx = first_real_idx;
	SET_OFFSET_OF(&vs->reals, reals);
	return 0;
}

static int
setup_selector(
	struct vs *vs,
	struct balancer_state *state,
	struct memory_context *mctx,
	struct vs_config *config
) {
	const struct real *reals = ADDR_OF(&vs->reals);
	if (selector_init(&vs->selector, state, mctx, config->scheduler) != 0) {
		PUSH_ERROR("failed to setup selector");
		return -1;
	}
	if (selector_update(&vs->selector, vs->reals_count, reals) != 0) {
		selector_free(&vs->selector);
		PUSH_ERROR("failed to setup selector reals");
		return -1;
	}
	return 0;
}

static int
register_counter(struct vs *vs, struct counter_registry *registry) {
	char name[60];
	sprintf(name, "vs_%zu", vs->registry_idx);
	vs->counter_id = counter_registry_register(
		registry, name, sizeof(struct vs_stats) / sizeof(uint64_t)
	);
	if (vs->counter_id == (size_t)-1) {
		PUSH_ERROR("failed to register counter in the counter registry"
		);
		return -1;
	}
	return 0;
}

static int
setup_peers(
	struct vs *vs, struct memory_context *mctx, struct vs_config *config
) {
	vs->peers_v4_count = config->peers_v4_count;
	vs->peers_v6_count = config->peers_v6_count;

	void *peers_v4_ptr = memory_balloc(
		mctx, sizeof(struct net4_addr) * vs->peers_v4_count
	);
	if (peers_v4_ptr == NULL && vs->peers_v4_count > 0) {
		NEW_ERROR("failed to allocate memory for IPv4 peers");
		return -1;
	}
	SET_OFFSET_OF(&vs->peers_v4, peers_v4_ptr);

	// Copy IPv4 peer addresses from config (config uses normal pointers)
	if (vs->peers_v4_count > 0) {
		memcpy(peers_v4_ptr,
		       config->peers_v4,
		       sizeof(struct net4_addr) * vs->peers_v4_count);
	}

	void *peers_v6_ptr = memory_balloc(
		mctx, sizeof(struct net6_addr) * vs->peers_v6_count
	);
	if (peers_v6_ptr == NULL && vs->peers_v6_count > 0) {
		NEW_ERROR("failed to allocate memory for IPv6 peers");
		memory_bfree(
			mctx,
			peers_v4_ptr,
			sizeof(struct net4_addr) * vs->peers_v4_count
		);
		return -1;
	}
	SET_OFFSET_OF(&vs->peers_v6, peers_v6_ptr);

	// Copy IPv6 peer addresses from config (config uses normal pointers)
	if (vs->peers_v6_count > 0) {
		memcpy(peers_v6_ptr,
		       config->peers_v6,
		       sizeof(struct net6_addr) * vs->peers_v6_count);
	}

	return 0;
}

static int
setup_state(
	struct vs *vs,
	struct balancer_state *balancer_state,
	struct named_vs_config *config
) {
	struct vs_state *vs_state = balancer_state_find_or_insert_vs(
		balancer_state, &config->identifier
	);
	if (!vs_state) {
		PUSH_ERROR(
			"failed to find or insert virtual service into registry"
		);
		return -1;
	}
	vs->registry_idx = vs_state->registry_idx;
	vs->identifier = config->identifier;
	return 0;
}

static int
setup_flags(struct vs *vs, struct named_vs_config *config) {
	if ((config->config.flags & VS_PURE_L3_FLAG) &&
	    config->identifier.port != 0) {
		NEW_ERROR(
			"PureL3 mode "
			"requires port=0, but port=%u was specified",
			config->identifier.port
		);
		return -1;
	}
	vs->flags = config->config.flags;
	return 0;
}

static void
free_rule(struct filter_rule *rule) {
	free(rule->net4.dsts);
	free(rule->net6.dsts);
	free(rule->transport.dsts);
	free(rule->transport.protos);
}

static void
free_rules(struct filter_rule *rules, size_t count) {
	for (size_t rule_idx = 0; rule_idx < count; ++rule_idx) {
		free_rule(&rules[rule_idx]);
	}
	free(rules);
}

static int
validate_net4(struct net4 *net4) {
	int prev = 1;
	for (int bit = 31; bit >= 0; --bit) {
		int byte = (31 - bit) / 8;
		int inner_bit = bit % 8;
		int cur = net4->mask[byte] & (1 << inner_bit);
		if (cur && !prev) {
			NEW_ERROR("mask bits must be consecutive");
			return -1;
		}
		prev = cur != 0;
	}

	return 0;
}

static int
validate_net6_half(const uint8_t *mask) { // bytes are in big-endian
	int prev = 1;
	for (int bit = 63; bit >= 0; --bit) {
		int byte = (63 - bit) / 8;
		int inner_bit = bit % 8;
		int cur = mask[byte] & (1 << inner_bit);
		if (cur && !prev) {
			NEW_ERROR("mask bits must be consecutive");
			return -1;
		}
		prev = cur != 0;
	}
	return 0;
}

static int
validate_net6(struct net6 *net6) {
	if (validate_net6_half(net6->mask) != 0) {
		PUSH_ERROR("high mask bits are invalid");
		return -1;
	}
	if (validate_net6_half(net6->mask + 8) != 0) {
		PUSH_ERROR("low mask bits are invalid");
		return -1;
	}
	return 0;
}

static int
fill_rule(struct vs *vs, struct filter_rule *rule, struct allowed_src *src) {
	rule->action = 1;
	if (vs->identifier.ip_proto == IPPROTO_IP) {
		rule->net4.dst_count = 1;
		rule->net4.dsts = NULL;

		if (validate_net4(&src->net.v4) != 0) {
			PUSH_ERROR("IPv4 network is invalid");
			return -1;
		}

		rule->net4.src_count = 1;
		rule->net4.srcs = malloc(sizeof(struct net4));
		rule->net4.srcs[0] = src->net.v4;
	} else if (vs->identifier.ip_proto == IPPROTO_IPV6) {
		rule->net6.dst_count = 1;
		rule->net6.dsts = NULL;

		if (validate_net6(&src->net.v6) != 0) {
			PUSH_ERROR("IPv6 network is invalid");
			return -1;
		}

		rule->net6.src_count = 1;
		rule->net6.srcs = malloc(sizeof(struct net6));
		rule->net6.srcs[0] = src->net.v6;
	}

	// Handle port ranges: if none specified, use default [0, 65535]
	if (src->port_ranges_count == 0) {
		rule->transport.src_count = 1;
		rule->transport.srcs = malloc(sizeof(struct filter_port_range));
		rule->transport.srcs[0].from = 0;
		rule->transport.srcs[0].to = 65535;
	} else {
		rule->transport.src_count = src->port_ranges_count;
		rule->transport.srcs =
			malloc(sizeof(struct filter_port_range) *
			       src->port_ranges_count);
		for (size_t port_range_idx = 0;
		     port_range_idx < src->port_ranges_count;
		     ++port_range_idx) {
			struct filter_port_range *filter_port_range =
				&rule->transport.srcs[port_range_idx];
			struct ports_range *port_range =
				&src->port_ranges[port_range_idx];
			filter_port_range->from = port_range->from;
			filter_port_range->to = port_range->to;
			if (filter_port_range->from > filter_port_range->to) {
				PUSH_ERROR("port range is invalid");
				return -1;
			}
		}
	}
	return 0;
}

static int
src_filter_rules(
	struct vs *vs,
	struct vs_config *config,
	struct filter_rule **rules,
	size_t *rule_count
) {
	if (vs->identifier.ip_proto != IPPROTO_IP &&
	    vs->identifier.ip_proto != IPPROTO_IPV6) {
		NEW_ERROR(
			"virtual service IP protocol is incorrect: %u "
			"(expected IPv4 %u or IPv6 %u)",
			vs->identifier.ip_proto,
			IPPROTO_IP,
			IPPROTO_IPV6
		);
		return -1;
	}

	size_t count = config->allowed_src_count;
	struct filter_rule *r = malloc(sizeof(struct filter_rule) * count);
	memset(r, 0, sizeof(struct filter_rule) * count);
	for (size_t rule_idx = 0; rule_idx < config->allowed_src_count;
	     ++rule_idx) {
		if (fill_rule(
			    vs, &r[rule_idx], &config->allowed_src[rule_idx]
		    ) != 0) {
			PUSH_ERROR("rule at index %zu is invalid", rule_idx);
			free_rules(r, count);
			return -1;
		}
	}

	*rule_count = count;
	*rules = r;
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

FILTER_COMPILER_DECLARE(vs_acl_ipv4, net4_fast_src, port_src);
FILTER_COMPILER_DECLARE(vs_acl_ipv6, net6_fast_src, port_src);

////////////////////////////////////////////////////////////////////////////////

static int
setup_acl(
	struct vs *vs, struct vs_config *config, struct memory_context *mctx
) {
	struct filter_rule *rules = NULL;
	size_t rule_count = 0;
	if (src_filter_rules(vs, config, &rules, &rule_count) != 0) {
		PUSH_ERROR("failed to setup src filter rules");
		return -1;
	}
	int res;
	if (vs->identifier.ip_proto == IPPROTO_IP) {
		res = FILTER_INIT(
			&vs->acl, vs_acl_ipv4, rules, rule_count, mctx
		);
	} else { // IPPROTO_IPV6
		res = FILTER_INIT(
			&vs->acl, vs_acl_ipv6, rules, rule_count, mctx
		);
	}
	if (res != 0) {
		NEW_ERROR("failed to initialize filter");
		free_rules(rules, rule_count);
		return -1;
	}
	return 0;
}

int
vs_init(struct vs *vs,
	size_t first_real_idx,
	struct real *reals,
	struct balancer_state *balancer_state,
	struct named_vs_config *config,
	struct counter_registry *registry,
	struct memory_context *mctx) {
	if (setup_state(vs, balancer_state, config) != 0) {
		PUSH_ERROR("failed to setup state");
		return -1;
	}

	if (setup_flags(vs, config) != 0) {
		PUSH_ERROR("failed to setup flags");
		return -1;
	}

	if (setup_peers(vs, mctx, &config->config) != 0) {
		PUSH_ERROR("failed to setup peers");
		return -1;
	}

	if (setup_reals(vs, &config->config, first_real_idx, reals) != 0) {
		PUSH_ERROR("failed to setup reals");
		goto free_peers;
	}

	if (setup_selector(vs, balancer_state, mctx, &config->config) != 0) {
		PUSH_ERROR("failed to setup selector");
		goto free_peers;
	}

	if (register_counter(vs, registry) != 0) {
		PUSH_ERROR("failed to register counter");
		goto free_selector;
	}

	if (setup_acl(vs, &config->config, mctx) != 0) {
		PUSH_ERROR("failed to setup acl");
		goto free_selector;
	}

	return 0;

free_selector:
	selector_free(&vs->selector);

free_peers:
	memory_bfree(
		mctx,
		ADDR_OF(&vs->peers_v4),
		sizeof(struct net4_addr) * vs->peers_v4_count
	);
	memory_bfree(
		mctx,
		ADDR_OF(&vs->peers_v6),
		sizeof(struct net6_addr) * vs->peers_v6_count
	);

	return -1;
}

void
vs_free(struct vs *vs, struct memory_context *mctx) {
	memory_bfree(
		mctx,
		ADDR_OF(&vs->peers_v4),
		sizeof(struct net4_addr) * vs->peers_v4_count
	);
	memory_bfree(
		mctx,
		ADDR_OF(&vs->peers_v6),
		sizeof(struct net6_addr) * vs->peers_v6_count
	);
	selector_free(&vs->selector);
}

int
vs_update_reals(struct vs *vs) {
	if (selector_update(
		    &vs->selector, vs->reals_count, ADDR_OF(&vs->reals)
	    ) != 0) {
		PUSH_ERROR("failed to update real selector");
		return -1;
	}
	return 0;
}

ssize_t
counter_to_vs_registry_idx(struct counter_handle *counter) {
	if (strncmp(counter->name, "vs_", 3) == 0) { // vs counter
		return atoi(counter->name + 3);
	} else {
		return -1;
	}
}