#include "balancer.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/rcu.h"

#include "filter/compiler.h"
#include "filter/rule.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/counters/counters.h"

#include "modules/balancer/dataplane/dataplane.h"
#include "modules/balancer/dataplane/types/real.h"
#include "modules/balancer/dataplane/types/stats.h"
#include "modules/balancer/dataplane/types/vs.h"

FILTER_COMPILER_DECLARE(
	vs_matcher_ipv4, net4_fast_dst, port_fast_dst, proto_range_fast
);
FILTER_COMPILER_DECLARE(
	vs_matcher_ipv6, net6_fast_dst, port_fast_dst, proto_range_fast
);

FILTER_COMPILER_DECLARE(decap_ipv4, net4_fast_dst);
FILTER_COMPILER_DECLARE(decap_ipv6, net6_fast_dst);

int
balancer_initial_setup(
	struct agent *agent,
	struct balancer_packet_handler *handler,
	const char *name,
	struct balancer_session_table *session_table
) {
	memset(handler, 0, sizeof(*handler));

	struct memory_context *mctx = &agent->memory_context;

	if (cp_module_init(&handler->cp_module, agent, "balancer", name) != 0) {
		memory_bfree(mctx, handler, sizeof(*handler));
		return -1;
	}

	SET_OFFSET_OF(&handler->session_table, session_table);
	rcu_init(&handler->rcu);

	return 0;
}

const char *
balancer_name(struct balancer_packet_handler *handler) {
	return handler->cp_module.name;
}

static int
register_vs_counter(struct balancer_vs *vs, struct counter_registry *registry) {
	char name[64];
	snprintf(name, sizeof(name), "vs_%zu", vs->stable_idx);

	vs->counter_id = counter_registry_register(
		registry,
		name,
		sizeof(struct balancer_vs_stats) / sizeof(uint64_t)
	);
	if (vs->counter_id == (uint64_t)-1) {
		return -1;
	}

	return 0;
}

static int
register_vs_rule_counters(
	struct balancer_vs *vs,
	struct counter_registry *registry,
	struct memory_context *mctx
) {
	uint32_t allowed_src_count = vs->allowed_sources_count;
	if (allowed_src_count == 0) {
		return 0;
	}

	struct balancer_vs_allowed_source *allowed_srcs =
		ADDR_OF(&vs->allowed_sources);

	uint64_t *ids =
		memory_balloc(mctx, allowed_src_count * sizeof(uint64_t));
	if (ids == NULL) {
		return -1;
	}

	for (uint32_t i = 0; i < allowed_src_count; ++i) {
		if (allowed_srcs[i].tag[0] == 0) {
			ids[i] = (uint64_t)-1;
			continue;
		}

		char name[64];
		snprintf(
			name,
			sizeof(name),
			"acl_%zu_%s",
			vs->stable_idx,
			allowed_srcs[i].tag
		);

		ids[i] = counter_registry_register(registry, name, 1);
		if (ids[i] == (uint64_t)-1) {
			memory_bfree(
				mctx, ids, allowed_src_count * sizeof(uint64_t)
			);
			return -1;
		}
	}

	SET_OFFSET_OF(&vs->rule_counter_ids, ids);

	return 0;
}

static int
register_real_counter(
	struct balancer_real *real,
	size_t vs_stable_ix,
	struct counter_registry *registry
) {
	char name[64];
	snprintf(
		name, sizeof(name), "rl_%zu_%zu", vs_stable_ix, real->stable_idx
	);

	real->counter_id = counter_registry_register(
		registry,
		name,
		sizeof(struct balancer_real_stats) / sizeof(uint64_t)
	);
	if (real->counter_id == (uint64_t)-1) {
		return -1;
	}

	return 0;
}

static int
register_module_counters(
	struct balancer_packet_handler *handler,
	struct counter_registry *registry
) {
	handler->common_counter_id = counter_registry_register(
		registry,
		"cmn",
		sizeof(struct balancer_common_stats) / sizeof(uint64_t)
	);
	if (handler->common_counter_id == (uint64_t)-1) {
		return -1;
	}

	handler->icmp_v4_counter_id = counter_registry_register(
		registry,
		"iv4",
		sizeof(struct balancer_icmp_stats) / sizeof(uint64_t)
	);
	if (handler->icmp_v4_counter_id == (uint64_t)-1) {
		return -1;
	}

	handler->icmp_v6_counter_id = counter_registry_register(
		registry,
		"iv6",
		sizeof(struct balancer_icmp_stats) / sizeof(uint64_t)
	);
	if (handler->icmp_v6_counter_id == (uint64_t)-1) {
		return -1;
	}

	handler->l4_counter_id = counter_registry_register(
		registry,
		"l4",
		sizeof(struct balancer_l4_stats) / sizeof(uint64_t)
	);
	if (handler->l4_counter_id == (uint64_t)-1) {
		return -1;
	}

	return 0;
}

int
balancer_register_counters(struct balancer_packet_handler *handler) {
	struct agent *agent = ADDR_OF(&handler->cp_module.agent);
	struct memory_context *mctx = &agent->memory_context;
	struct counter_registry *registry =
		&handler->cp_module.counter_registry;

	if (register_module_counters(handler, registry) != 0) {
		return -1;
	}

	struct balancer_vs *services = ADDR_OF(&handler->vs);
	for (uint32_t vs_idx = 0; vs_idx < handler->vs_count; ++vs_idx) {
		struct balancer_vs *vs = &services[vs_idx];
		if (vs->flags & balancer_vs_removed) {
			continue;
		}

		if (register_vs_counter(vs, registry) != 0) {
			return -1;
		}

		if (register_vs_rule_counters(vs, registry, mctx) != 0) {
			return -1;
		}

		struct balancer_real *reals = ADDR_OF(&vs->reals);
		for (uint32_t real_idx = 0; real_idx < vs->reals_count;
		     ++real_idx) {
			struct balancer_real *real = &reals[real_idx];
			if (real->flags & balancer_real_removed) {
				continue;
			}
			if (register_real_counter(
				    real, vs->stable_idx, registry
			    ) != 0) {
				return -1;
			}
		}
	}

	return 0;
}

static void
free_rules(struct filter_rule *rules, size_t count) {
	for (size_t i = 0; i < count; ++i) {
		free(rules[i].net4.dsts);
		free(rules[i].net6.dsts);
		free(rules[i].transport.srcs);
		free(rules[i].transport.dsts);
		free(rules[i].transport.protos);
	}
	free(rules);
}

void
balancer_free_decap_filters(struct balancer_packet_handler *handler) {
	struct agent *agent = ADDR_OF(&handler->cp_module.agent);
	struct memory_context *mctx = &agent->memory_context;

	struct filter *filter_ipv4 = ADDR_OF(&handler->decap_ipv4_filter);
	if (filter_ipv4 != NULL) {
		FILTER_FREE(filter_ipv4, decap_ipv4);
		memory_bfree(mctx, filter_ipv4, sizeof(struct filter));
		SET_OFFSET_OF(&handler->decap_ipv4_filter, NULL);
	}

	struct filter *filter_ipv6 = ADDR_OF(&handler->decap_ipv6_filter);
	if (filter_ipv6 != NULL) {
		FILTER_FREE(filter_ipv6, decap_ipv6);
		memory_bfree(mctx, filter_ipv6, sizeof(struct filter));
		SET_OFFSET_OF(&handler->decap_ipv6_filter, NULL);
	}
}

void
balancer_free_vs_matchers(struct balancer_packet_handler *handler) {
	struct agent *agent = ADDR_OF(&handler->cp_module.agent);
	struct memory_context *mctx = &agent->memory_context;

	struct filter *ipv4 = ADDR_OF(&handler->ipv4_vs_matcher);
	if (ipv4 != NULL) {
		FILTER_FREE(ipv4, vs_matcher_ipv4);
		memory_bfree(mctx, ipv4, sizeof(struct filter));
		SET_OFFSET_OF(&handler->ipv4_vs_matcher, NULL);
	}

	struct filter *ipv6 = ADDR_OF(&handler->ipv6_vs_matcher);
	if (ipv6 != NULL) {
		FILTER_FREE(ipv6, vs_matcher_ipv6);
		memory_bfree(mctx, ipv6, sizeof(struct filter));
		SET_OFFSET_OF(&handler->ipv6_vs_matcher, NULL);
	}
}

static int
make_dst_addr_rule(struct filter_rule *rule, uint8_t *dst, uint8_t ipproto) {
	if (ipproto == IPPROTO_IPV6) {
		rule->net6.dst_count = 1;
		rule->net6.dsts = malloc(sizeof(struct net6));
		if (rule->net6.dsts == NULL) {
			return -1;
		}
		memcpy(rule->net6.dsts[0].addr, dst, NET6_LEN);
		memset(rule->net6.dsts[0].mask, 0xFF, NET6_LEN);
	} else {
		rule->net4.dst_count = 1;
		rule->net4.dsts = malloc(sizeof(struct net4));
		if (rule->net4.dsts == NULL) {
			return -1;
		}
		memcpy(rule->net4.dsts[0].addr, dst, NET4_LEN);
		memset(rule->net4.dsts[0].mask, 0xFF, NET4_LEN);
	}
	return 0;
}

static ssize_t
make_decap_rules(
	struct balancer_packet_handler *handler,
	struct filter_rule **out,
	int is_ipv6
) {
	size_t count =
		is_ipv6 ? handler->decap_v6_count : handler->decap_v4_count;
	struct filter_rule *rules = calloc(count, sizeof(struct filter_rule));
	if (rules == NULL && count > 0) {
		return -1;
	}
	memset(rules, 0, count * sizeof(struct filter_rule));

	struct net4_addr *decap_v4 = ADDR_OF(&handler->decap_v4);
	struct net6_addr *decap_v6 = ADDR_OF(&handler->decap_v6);

	for (size_t i = 0; i < count; ++i) {
		struct filter_rule *rule = &rules[i];

		int res;
		if (is_ipv6) {
			res = make_dst_addr_rule(
				rule, decap_v6[i].bytes, IPPROTO_IPV6
			);
		} else {
			res = make_dst_addr_rule(
				rule, decap_v4[i].bytes, IPPROTO_IP
			);
		}
		if (res != 0) {
			free_rules(rules, count);
			return -1;
		}

		rule->action = i;
	}

	*out = rules;
	return count;
}

static int
build_decap_filter(struct balancer_packet_handler *handler, int is_ipv6) {
	struct agent *agent = ADDR_OF(&handler->cp_module.agent);
	struct memory_context *mctx = &agent->memory_context;

	struct filter *filter = memory_balloc(mctx, sizeof(struct filter));
	if (filter == NULL) {
		return -1;
	}

	struct filter_rule *rules = NULL;
	ssize_t res = make_decap_rules(handler, &rules, is_ipv6);
	if (res == -1) {
		return -2;
	}
	size_t count = (size_t)res;

	if (is_ipv6) {
		res = FILTER_INIT(filter, decap_ipv6, rules, count, mctx);
	} else {
		res = FILTER_INIT(filter, decap_ipv4, rules, count, mctx);
	}
	free_rules(rules, count);

	if (res != 0) {
		memory_bfree(mctx, filter, sizeof(struct filter));
		return -1;
	}

	if (is_ipv6) {
		SET_OFFSET_OF(&handler->decap_ipv6_filter, filter);
	} else {
		SET_OFFSET_OF(&handler->decap_ipv4_filter, filter);
	}

	return 0;
}

int
balancer_set_ipv4_decap_filter(struct balancer_packet_handler *handler) {
	return build_decap_filter(handler, 0);
}

int
balancer_set_ipv6_decap_filter(struct balancer_packet_handler *handler) {
	return build_decap_filter(handler, 1);
}

static int
make_transport_rule(struct filter_rule *rule, struct balancer_vs *vs) {
	rule->transport.dst_count = 1;
	rule->transport.dsts = malloc(sizeof(struct filter_port_range));
	if (rule->transport.dsts == NULL) {
		return -1;
	}

	if (vs->flags & balancer_vs_pure_l3) {
		rule->transport.dsts[0].from = 0;
		rule->transport.dsts[0].to = 65535;
	} else {
		rule->transport.dsts[0].from = vs->port;
		rule->transport.dsts[0].to = vs->port;
	}

	rule->transport.proto_count = 1;
	rule->transport.protos = calloc(1, sizeof(struct filter_proto_range));
	if (rule->transport.protos == NULL) {
		free(rule->transport.dsts);
		return -1;
	}

	rule->transport.protos[0].from = vs->transport_proto * 256;
	rule->transport.protos[0].to = vs->transport_proto * 256 + 255;

	return 0;
}

static int
make_vs_matcher_rule(struct filter_rule *rule, struct balancer_vs *vs) {
	if (make_dst_addr_rule(rule, vs->addr.v6.bytes, vs->ip_proto) != 0) {
		return -1;
	}

	if (make_transport_rule(rule, vs) != 0) {
		return -1;
	}

	return 0;
}

static ssize_t
make_vs_matcher_rules(
	struct filter_rule **out,
	uint8_t ipproto,
	struct balancer_vs *services,
	uint32_t service_count
) {
	size_t rule_count = 0;
	for (size_t i = 0; i < service_count; ++i) {
		if (services[i].ip_proto == ipproto &&
		    !(services[i].flags & balancer_vs_removed)) {
			++rule_count;
		}
	}

	struct filter_rule *rules =
		calloc(rule_count, sizeof(struct filter_rule));
	if (rules == NULL && rule_count > 0) {
		return -1;
	}
	memset(rules, 0, rule_count * sizeof(struct filter_rule));

	size_t rule_idx = 0;
	for (size_t i = 0; i < service_count; ++i) {
		struct balancer_vs *vs = &services[i];
		if (vs->ip_proto != ipproto ||
		    (vs->flags & balancer_vs_removed)) {
			continue;
		}

		struct filter_rule *rule = &rules[rule_idx];

		if (make_vs_matcher_rule(rule, vs) != 0) {
			free_rules(rules, rule_count);
			return -1;
		}

		rule->action = i;
		++rule_idx;
	}

	*out = rules;
	return rule_count;
}

static int
build_vs_matcher(
	struct balancer_packet_handler *handler,
	uint8_t ipproto,
	struct filter **result
) {
	struct agent *agent = ADDR_OF(&handler->cp_module.agent);
	struct memory_context *mctx = &agent->memory_context;
	struct balancer_vs *vs = ADDR_OF(&handler->vs);
	size_t vs_count = handler->vs_count;

	struct filter *filter = memory_balloc(mctx, sizeof(struct filter));
	if (filter == NULL) {
		return -1;
	}

	struct filter_rule *rules = NULL;
	ssize_t res = make_vs_matcher_rules(&rules, ipproto, vs, vs_count);
	if (res == -1) {
		return -2;
	}
	size_t count = (size_t)res;

	if (ipproto == IPPROTO_IPV6) {
		res = FILTER_INIT(filter, vs_matcher_ipv6, rules, count, mctx);
	} else {
		res = FILTER_INIT(filter, vs_matcher_ipv4, rules, count, mctx);
	}
	free_rules(rules, count);

	if (res != 0) {
		memory_bfree(mctx, filter, sizeof(struct filter));
		return -1;
	}

	*result = filter;

	return 0;
}

int
balancer_set_ipv4_vs_matcher(struct balancer_packet_handler *handler) {
	struct filter *filter;
	int res = build_vs_matcher(handler, IPPROTO_IP, &filter);
	if (res != 0) {
		return res;
	}

	SET_OFFSET_OF(&handler->ipv4_vs_matcher, filter);

	return 0;
}

int
balancer_set_ipv6_vs_matcher(struct balancer_packet_handler *handler) {
	struct filter *filter;
	int res = build_vs_matcher(handler, IPPROTO_IPV6, &filter);
	if (res != 0) {
		return res;
	}

	SET_OFFSET_OF(&handler->ipv6_vs_matcher, filter);

	return 0;
}