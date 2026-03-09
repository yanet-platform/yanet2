#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"
#include "common/memory_address.h"

#include "controlplane/agent/agent.h"

#include "filter/compiler.h"

FILTER_COMPILER_DECLARE(FILTER_IP4_TAG, net4_dst);
FILTER_COMPILER_DECLARE(FILTER_IP6_TAG, net6_dst);

struct cp_module *
route_mpls_module_config_create(struct agent *agent, const char *name) {
	struct module_config *config = (struct module_config *)memory_balloc(
		&agent->memory_context, sizeof(struct module_config)
	);
	if (config == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "route-mpls", name)) {
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct module_config)
		);
		return 0;
	}

	memset(&config->filter_ip4, 0, sizeof(config->filter_ip4));
	memset(&config->filter_ip6, 0, sizeof(config->filter_ip6));
	config->target_count = 0;
	SET_OFFSET_OF(&config->targets, NULL);

	return &config->cp_module;
}

static inline uint64_t
target_memory_size(uint64_t nexthop_map_size) {
	return sizeof(struct target) + sizeof(uint64_t) * nexthop_map_size;
}

static void
route_mpls_module_config_destroy(struct module_config *config) {
	struct memory_context *memory_context =
		&config->cp_module.memory_context;

	FILTER_FREE(&config->filter_ip6, FILTER_IP6_TAG);
	memset(&config->filter_ip6, 0, sizeof(config->filter_ip6));
	FILTER_FREE(&config->filter_ip4, FILTER_IP4_TAG);
	memset(&config->filter_ip4, 0, sizeof(config->filter_ip4));

	struct target **targets = ADDR_OF(&config->targets);
	for (uint64_t idx = 0; idx < config->target_count; ++idx) {
		struct target *target = ADDR_OF(targets + idx);
		if (target == NULL)
			continue;

		if (ADDR_OF(&target->nexthops) != NULL) {
			memory_bfree(
				memory_context,
				ADDR_OF(&target->nexthops),
				sizeof(struct nexthop) * target->nexthop_count
			);
		}
		SET_OFFSET_OF(&target->nexthops, NULL);

		memory_bfree(
			memory_context,
			target,
			target_memory_size(target->nexthop_map_size)
		);
	}
	memory_bfree(
		memory_context,
		targets,
		sizeof(struct target *) * config->target_count
	);
	SET_OFFSET_OF(&config->targets, NULL);
}

void
route_mpls_module_config_free(struct cp_module *cp_module) {
	struct module_config *config =
		container_of(cp_module, struct module_config, cp_module);

	route_mpls_module_config_destroy(config);

	struct agent *agent = ADDR_OF(&cp_module->agent);
	memory_bfree(
		&agent->memory_context, config, sizeof(struct module_config)
	);
}

typedef int (*route_mpls_rule_check_func)(
	const struct route_mpls_rule *route_mpls_rule
);

static uint32_t
filter_route_mpls_rules(
	struct route_mpls_rule *route_mpls_rules,
	uint32_t route_mpls_rule_count,
	struct filter_rule *filter_rules,
	route_mpls_rule_check_func check
	// TODO: should be there an instantiation callback??
) {
	uint32_t filter_rule_idx = 0;
	for (uint32_t route_mpls_rule_idx = 0;
	     route_mpls_rule_idx < route_mpls_rule_count;
	     ++route_mpls_rule_idx) {
		struct route_mpls_rule *route_mpls_rule =
			route_mpls_rules + route_mpls_rule_idx;
		if (!check(route_mpls_rule))
			continue;

		struct filter_rule *filter_rule =
			filter_rules + filter_rule_idx++;

		memset(filter_rule, 0, sizeof(struct filter_rule));

		filter_rule->net4.dst_count = route_mpls_rule->net4s.count;
		filter_rule->net4.dsts = route_mpls_rule->net4s.items;

		filter_rule->net6.dst_count = route_mpls_rule->net6s.count;
		filter_rule->net6.dsts = route_mpls_rule->net6s.items;

		filter_rule->action = route_mpls_rule_idx;
	}

	return filter_rule_idx;
}

static int
check_route_mpls_rule_ip4(const struct route_mpls_rule *route_mpls_rule) {
	return route_mpls_rule->net4s.count;
}

static int
check_route_mpls_rule_ip6(const struct route_mpls_rule *route_mpls_rule) {
	return route_mpls_rule->net6s.count;
}

static int
route_mpls_module_init_ip4(
	struct cp_module *cp_module,
	struct route_mpls_rule *route_mpls_rules,
	uint64_t route_mpls_rule_count,
	struct filter_rule *filter_rules
) {
	struct module_config *config =
		container_of(cp_module, struct module_config, cp_module);

	uint32_t filter_rule_count = filter_route_mpls_rules(
		route_mpls_rules,
		route_mpls_rule_count,
		filter_rules,
		check_route_mpls_rule_ip4
	);

	return FILTER_INIT(
		&config->filter_ip4,
		FILTER_IP4_TAG,
		filter_rules,
		filter_rule_count,
		&cp_module->memory_context
	);
}

static int
route_mpls_module_init_ip6(
	struct cp_module *cp_module,
	struct route_mpls_rule *route_mpls_rules,
	uint64_t route_mpls_rule_count,
	struct filter_rule *filter_rules
) {
	struct module_config *config =
		container_of(cp_module, struct module_config, cp_module);

	uint32_t filter_rule_count = filter_route_mpls_rules(
		route_mpls_rules,
		route_mpls_rule_count,
		filter_rules,
		check_route_mpls_rule_ip6
	);

	return FILTER_INIT(
		&config->filter_ip6,
		FILTER_IP6_TAG,
		filter_rules,
		filter_rule_count,
		&cp_module->memory_context
	);
}

static struct target *
route_mpls_rule_target_create(
	struct cp_module *cp_module, struct route_mpls_rule *route_mpls_rule
) {
	struct memory_context *memory_context = &cp_module->memory_context;

	uint64_t map_size = 0;

	for (uint64_t nexthop_idx = 0;
	     nexthop_idx < route_mpls_rule->nexthop_count;
	     ++nexthop_idx) {
		struct route_mpls_nexthop *nexthop =
			route_mpls_rule->nexthops + nexthop_idx;
		map_size += nexthop->weight;
	}

	struct target *target = (struct target *)memory_balloc(
		memory_context, target_memory_size(map_size)
	);
	if (target == NULL)
		return NULL;

	memset(target, 0, target_memory_size(map_size));
	target->nexthop_map_size = map_size;

	struct nexthop *nexthops = (struct nexthop *)memory_balloc(
		memory_context,
		sizeof(struct nexthop) * route_mpls_rule->nexthop_count
	);
	if (nexthops == NULL)
		goto error_target;

	memset(nexthops,
	       0,
	       sizeof(struct nexthop) * route_mpls_rule->nexthop_count);
	SET_OFFSET_OF(&target->nexthops, nexthops);

	uint64_t map_pos = 0;

	for (uint64_t idx = 0; idx < route_mpls_rule->nexthop_count; ++idx) {
		struct route_mpls_nexthop *route_mpls_nexthop =
			route_mpls_rule->nexthops + idx;

		if ((nexthops[idx].counter_id = counter_registry_register(
			     &cp_module->counter_registry,
			     route_mpls_nexthop->counter,
			     2
		     )) == COUNTER_INVALID) {
			goto error_counter;
		}

		if (route_mpls_nexthop->kind == ROUTE_MPLS_TYPE_V4) {
			nexthops[idx].type = ROUTE_TYPE_V4;
			memcpy(nexthops[idx].ip4_tunnel.src,
			       route_mpls_nexthop->ip4_tunnel.src,
			       4);
			memcpy(nexthops[idx].ip4_tunnel.dst,
			       route_mpls_nexthop->ip4_tunnel.dst,
			       4);
		} else if (route_mpls_nexthop->kind == ROUTE_MPLS_TYPE_V6) {
			nexthops[idx].type = ROUTE_TYPE_V6;
			memcpy(nexthops[idx].ip6_tunnel.src,
			       route_mpls_nexthop->ip6_tunnel.src,
			       16);
			memcpy(nexthops[idx].ip6_tunnel.dst,
			       route_mpls_nexthop->ip6_tunnel.dst,
			       16);
		} else {
			nexthops[idx].type = ROUTE_TYPE_NONE;
		}

		nexthops[idx].mpls_label = route_mpls_nexthop->mpls_label;

		for (uint64_t weight_idx = 0;
		     weight_idx < route_mpls_nexthop->weight;
		     ++weight_idx) {
			target->nexthop_map[map_pos++] = idx;
		}
	}

	return target;

error_counter:
	memory_bfree(
		memory_context,
		ADDR_OF(&target->nexthops),
		sizeof(struct nexthop) * route_mpls_rule->nexthop_count
	);

error_target:
	memory_bfree(memory_context, target, target_memory_size(map_size));
	return NULL;
}

int
route_mpls_module_config_update(
	struct cp_module *cp_module,
	struct route_mpls_rule *route_mpls_rules,
	uint64_t route_mpls_rule_count
) {
	struct memory_context *memory_context = &cp_module->memory_context;

	struct module_config *config =
		container_of(cp_module, struct module_config, cp_module);

	struct target **targets = (struct target **)memory_balloc(
		memory_context, sizeof(struct target *) * route_mpls_rule_count
	);

	if (targets == NULL) {
		return -1;
	}
	memset(targets, 0, sizeof(struct target *) * route_mpls_rule_count);
	SET_OFFSET_OF(&config->targets, targets);
	config->target_count = route_mpls_rule_count;

	for (uint64_t idx = 0; idx < route_mpls_rule_count; ++idx) {
		struct target *target = route_mpls_rule_target_create(
			cp_module, route_mpls_rules + idx
		);

		if (target == NULL)
			goto error;

		SET_OFFSET_OF(targets + idx, target);
	}

	// Create per filter rule list
	struct filter_rule *filter_rules = (struct filter_rule *)malloc(
		sizeof(struct filter_rule) * route_mpls_rule_count
	);
	if (filter_rules == NULL) {
		goto error;
	}

	if (route_mpls_module_init_ip4(
		    cp_module,
		    route_mpls_rules,
		    route_mpls_rule_count,
		    filter_rules
	    ))
		goto error;

	if (route_mpls_module_init_ip6(
		    cp_module,
		    route_mpls_rules,
		    route_mpls_rule_count,
		    filter_rules
	    ))
		goto error;

	free(filter_rules);

	return 0;

error:

	route_mpls_module_config_destroy(config);

	return -1;
}
