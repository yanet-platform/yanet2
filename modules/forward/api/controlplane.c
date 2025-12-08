#include <errno.h>

#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"

#include "controlplane/agent/agent.h"

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

	if (cp_module_init(
		    &config->cp_module,
		    agent,
		    "forward",
		    name,
		    forward_module_config_free
	    )) {
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

FILTER_DECLARE(FWD_FILTER_VLAN_TAG, &attribute_device, &attribute_vlan);

FILTER_DECLARE(
	FWD_FILTER_IP4_TAG,
	&attribute_device,
	&attribute_vlan,
	&attribute_net4_src,
	&attribute_net4_dst
);

FILTER_DECLARE(
	FWD_FILTER_IP6_TAG,
	&attribute_device,
	&attribute_vlan,
	&attribute_net6_src,
	&attribute_net6_dst
);

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

		for (uint32_t idx = 0; idx < rule->device_count; ++idx) {
			if (cp_module_link_device(
				    cp_module,
				    rule->devices[idx].name,
				    &rule->devices[idx].id
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
	uint64_t filter_rule_idx;

	// Build vlan rules
	filter_rule_idx = 0;
	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		struct forward_rule *forward_rule = forward_rules + idx;
		if (forward_rule->net6.src_count ||
		    forward_rule->net6.dst_count ||
		    forward_rule->net4.src_count ||
		    forward_rule->net4.dst_count) {
			continue;
		}

		struct filter_rule *filter_rule =
			filter_rules + filter_rule_idx++;
		filter_rule->device_count = forward_rule->device_count;
		filter_rule->devices = forward_rule->devices;

		filter_rule->vlan_range_count = forward_rule->vlan_range_count;
		filter_rule->vlan_ranges = forward_rule->vlan_ranges;

		filter_rule->action = idx;
	}

	if (FILTER_INIT(
		    &config->filter_vlan,
		    FWD_FILTER_VLAN_TAG,
		    filter_rules,
		    filter_rule_idx,
		    &cp_module->memory_context
	    )) {
	}

	// Build ip4 rules
	filter_rule_idx = 0;
	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		struct forward_rule *forward_rule = forward_rules + idx;
		if (!forward_rule->net4.src_count ||
		    !forward_rule->net4.dst_count) {
			continue;
		}

		struct filter_rule *filter_rule =
			filter_rules + filter_rule_idx++;
		filter_rule->device_count = forward_rule->device_count;
		filter_rule->devices = forward_rule->devices;

		filter_rule->vlan_range_count = forward_rule->vlan_range_count;
		filter_rule->vlan_ranges = forward_rule->vlan_ranges;

		filter_rule->net4 = forward_rule->net4;

		filter_rule->action = idx;
	}

	if (FILTER_INIT(
		    &config->filter_ip4,
		    FWD_FILTER_IP4_TAG,
		    filter_rules,
		    filter_rule_idx,
		    &cp_module->memory_context
	    )) {
	}

	// Build ip6 rules
	filter_rule_idx = 0;
	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		struct forward_rule *forward_rule = forward_rules + idx;
		if (!forward_rule->net6.src_count ||
		    !forward_rule->net6.dst_count) {
			continue;
		}

		struct filter_rule *filter_rule =
			filter_rules + filter_rule_idx++;
		filter_rule->device_count = forward_rule->device_count;
		filter_rule->devices = forward_rule->devices;

		filter_rule->vlan_range_count = forward_rule->vlan_range_count;
		filter_rule->vlan_ranges = forward_rule->vlan_ranges;

		filter_rule->net6 = forward_rule->net6;

		filter_rule->action = idx;
	}

	if (FILTER_INIT(
		    &config->filter_ip6,
		    FWD_FILTER_IP6_TAG,
		    filter_rules,
		    filter_rule_idx,
		    &cp_module->memory_context
	    )) {
	}

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
