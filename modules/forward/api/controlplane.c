#include <errno.h>

#include "controlplane.h"

#include "config.h"

#include <filter/compiler.h>

#include "common/container_of.h"

#include "controlplane/agent/agent.h"

FILTER_COMPILER_DECLARE(FWD_FILTER_VLAN_TAG, device, vlan);

FILTER_COMPILER_DECLARE(FWD_FILTER_IP4_TAG, device, vlan, net4_src, net4_dst);

FILTER_COMPILER_DECLARE(FWD_FILTER_IP6_TAG, device, vlan, net6_src, net6_dst);

struct cp_module *
forward_module_config_init(struct agent *agent, const char *name) {
	struct forward_module_config *config =
		(struct forward_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct forward_module_config)
		);
	if (config == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "forward", name)) {
		goto fail;
	}

	SET_OFFSET_OF(&config->targets, NULL);
	config->target_count = 0;

	memset(&config->filter_vlan, 0, sizeof(config->filter_vlan));

	memset(&config->filter_ip4, 0, sizeof(config->filter_ip4));

	memset(&config->filter_ip6, 0, sizeof(config->filter_ip6));

	return &config->cp_module;

fail: {
	int prev_errno = errno;
	forward_module_config_free(&config->cp_module);
	errno = prev_errno;
	return NULL;
}
}

void
forward_module_config_free(struct cp_module *cp_module) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	memory_bfree(
		&cp_module->memory_context,
		ADDR_OF(&config->targets),
		sizeof(struct forward_target *) * config->target_count
	);

	FILTER_FREE(&config->filter_vlan, FWD_FILTER_VLAN_TAG);
	FILTER_FREE(&config->filter_ip4, FWD_FILTER_IP4_TAG);
	FILTER_FREE(&config->filter_ip6, FWD_FILTER_IP6_TAG);

	struct agent *agent = ADDR_OF(&cp_module->agent);
	// FIXME: remove the check as agent should be assigned
	if (agent != NULL) {
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct forward_module_config)
		);
	}
}

typedef int (*forward_rule_check_func)(const struct forward_rule *forward_rule);

static uint32_t
filter_forward_rules(
	struct forward_rule *forward_rules,
	uint32_t forward_rule_count,
	struct filter_rule *filter_rules,
	forward_rule_check_func check
	// TODO: should be there an instantiation callback??
) {
	uint32_t filter_rule_idx = 0;
	for (uint32_t forward_rule_idx = 0;
	     forward_rule_idx < forward_rule_count;
	     ++forward_rule_idx) {
		struct forward_rule *forward_rule =
			forward_rules + forward_rule_idx;
		if (!check(forward_rule))
			continue;

		struct filter_rule *filter_rule =
			filter_rules + filter_rule_idx++;
		filter_rule->device_count = forward_rule->devices.count;
		filter_rule->devices = forward_rule->devices.items;

		filter_rule->vlan_range_count = forward_rule->vlan_ranges.count;
		filter_rule->vlan_ranges = forward_rule->vlan_ranges.items;

		filter_rule->net4.src_count = forward_rule->src_net4s.count;
		filter_rule->net4.srcs = forward_rule->src_net4s.items;
		filter_rule->net4.dst_count = forward_rule->dst_net4s.count;
		filter_rule->net4.dsts = forward_rule->dst_net4s.items;

		filter_rule->net6.src_count = forward_rule->src_net6s.count;
		filter_rule->net6.srcs = forward_rule->src_net6s.items;
		filter_rule->net6.dst_count = forward_rule->dst_net6s.count;
		filter_rule->net6.dsts = forward_rule->dst_net6s.items;

		filter_rule->action = forward_rule_idx;
	}

	return filter_rule_idx;
}

static int
check_forward_rule_l2(const struct forward_rule *forward_rule) {
	return !forward_rule->src_net6s.count &&
	       !forward_rule->dst_net6s.count &&
	       !forward_rule->src_net4s.count && !forward_rule->dst_net4s.count;
}

static int
check_has_ip4(const struct forward_rule *forward_rule) {
	return forward_rule->src_net4s.count && forward_rule->dst_net4s.count;
}

static int
check_has_ip6(const struct forward_rule *forward_rule) {
	return forward_rule->src_net6s.count && forward_rule->dst_net6s.count;
}

static int
check_forward_rule_ip4(const struct forward_rule *forward_rule) {
	return check_has_ip4(forward_rule);
	;
}

static int
check_forward_rule_ip6(const struct forward_rule *forward_rule) {
	return check_has_ip6(forward_rule);
}

static int
forward_module_init_l2(
	struct cp_module *cp_module,
	struct forward_rule *forward_rules,
	uint32_t forward_rule_count,
	struct filter_rule *filter_rules
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	uint32_t filter_rule_count = filter_forward_rules(
		forward_rules,
		forward_rule_count,
		filter_rules,
		check_forward_rule_l2
	);

	return FILTER_INIT(
		&config->filter_vlan,
		FWD_FILTER_VLAN_TAG,
		filter_rules,
		filter_rule_count,
		&cp_module->memory_context
	);
}

static int
forward_module_init_ip4(
	struct cp_module *cp_module,
	struct forward_rule *forward_rules,
	uint32_t forward_rule_count,
	struct filter_rule *filter_rules
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	uint32_t filter_rule_count = filter_forward_rules(
		forward_rules,
		forward_rule_count,
		filter_rules,
		check_forward_rule_ip4
	);

	return FILTER_INIT(
		&config->filter_ip4,
		FWD_FILTER_IP4_TAG,
		filter_rules,
		filter_rule_count,
		&cp_module->memory_context
	);
}

static int
forward_module_init_ip6(
	struct cp_module *cp_module,
	struct forward_rule *forward_rules,
	uint32_t forward_rule_count,
	struct filter_rule *filter_rules
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	uint32_t filter_rule_count = filter_forward_rules(
		forward_rules,
		forward_rule_count,
		filter_rules,
		check_forward_rule_ip6
	);

	return FILTER_INIT(
		&config->filter_ip6,
		FWD_FILTER_IP6_TAG,
		filter_rules,
		filter_rule_count,
		&cp_module->memory_context
	);
}

int
forward_module_config_update(
	struct cp_module *cp_module,
	struct forward_rule *forward_rules,
	uint32_t rule_count
) {
	struct forward_module_config *config = container_of(
		cp_module, struct forward_module_config, cp_module
	);

	struct forward_target *targets = (struct forward_target *)memory_balloc(
		&cp_module->memory_context,
		sizeof(struct forward_target) * rule_count
	);
	if (targets == NULL) {
		goto error;
	}

	SET_OFFSET_OF(&config->targets, targets);
	config->target_count = rule_count;

	// Just collect and link all devices
	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		struct forward_rule *rule = forward_rules + idx;

		if (cp_module_link_device(
			    cp_module, rule->target, &targets[idx].device_id
		    )) {
			goto error_target;
		}

		targets[idx].mode = rule->mode;

		if ((targets[idx].counter_id = counter_registry_register(
			     &cp_module->counter_registry, rule->counter, 2
		     )) == (uint64_t)-1) {
			goto error_target;
		}

		for (uint32_t idx = 0; idx < rule->devices.count; ++idx) {
			if (cp_module_link_device(
				    cp_module,
				    rule->devices.items[idx].name,
				    &rule->devices.items[idx].id
			    )) {
				goto error_target;
			}
		}
	}

	// Create per filter rule list
	struct filter_rule *filter_rules = (struct filter_rule *)malloc(
		sizeof(struct filter_rule) * rule_count
	);
	if (filter_rules == NULL) {
		goto error_target;
	}

	if (forward_module_init_l2(
		    cp_module, forward_rules, rule_count, filter_rules
	    ))
		goto error_target;

	if (forward_module_init_ip4(
		    cp_module, forward_rules, rule_count, filter_rules
	    ))
		goto error_target;

	if (forward_module_init_ip6(
		    cp_module, forward_rules, rule_count, filter_rules
	    ))
		goto error_target;

	free(filter_rules);

	return 0;

error_target:
	memory_bfree(
		&cp_module->memory_context,
		targets,
		sizeof(struct forward_target) * rule_count
	);

error:

	return -1;
}

int
forward_module_config_delete(struct cp_module *cp_module) {
	return agent_delete_module(
		cp_module->agent, "forward", cp_module->name
	);
}
