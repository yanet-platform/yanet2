#include <errno.h>
#include <stdint.h>

#include "controlplane.h"

#include "common/memory_address.h"
#include "config.h"
#include "modules/fwstate/api/fwstate_cp.h"
#include "modules/fwstate/dataplane/config.h"

#include "common/container_of.h"

#include "controlplane/agent/agent.h"

#include <filter/compiler.h>

FILTER_COMPILER_DECLARE(ACL_FILTER_VLAN_TAG, device, vlan);

FILTER_COMPILER_DECLARE(
	ACL_FILTER_IP4_TAG, device, vlan, net4_src, net4_dst, proto_range
);

FILTER_COMPILER_DECLARE(
	ACL_FILTER_IP4_PROTO_PORT_TAG,
	device,
	vlan,
	net4_src,
	net4_dst,
	proto_range,
	port_src,
	port_dst
);

FILTER_COMPILER_DECLARE(
	ACL_FILTER_IP6_TAG, device, vlan, net6_src, net6_dst, proto_range
);

FILTER_COMPILER_DECLARE(
	ACL_FILTER_IP6_PROTO_PORT_TAG,
	device,
	vlan,
	net6_src,
	net6_dst,
	proto_range,
	port_src,
	port_dst
);

struct cp_module *
acl_module_config_init(struct agent *agent, const char *name) {
	struct acl_module_config *config =
		(struct acl_module_config *)memory_balloc(
			&agent->memory_context, sizeof(struct acl_module_config)
		);
	if (config == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "acl", name)) {
		int prev_errno = errno;
		acl_module_config_free(&config->cp_module);
		errno = prev_errno;
		return NULL;
	}

	SET_OFFSET_OF(&config->targets, NULL);
	config->target_count = 0;

	memset(&config->filter_vlan, 0, sizeof(config->filter_vlan));

	memset(&config->filter_ip4, 0, sizeof(config->filter_ip4));
	memset(&config->filter_ip4_port, 0, sizeof(config->filter_ip4_port));

	memset(&config->filter_ip6, 0, sizeof(config->filter_ip6));
	memset(&config->filter_ip6_port, 0, sizeof(config->filter_ip6_port));

	// Initialize fwstate_cfg with NULL pointers
	memset(&config->fwstate_cfg, 0, sizeof(struct fwstate_config));

	return &config->cp_module;
}

void
acl_module_config_free(struct cp_module *cp_module) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);

	struct agent *agent = ADDR_OF(&cp_module->agent);

	memory_bfree(
		&cp_module->memory_context,
		ADDR_OF(&config->targets),
		sizeof(struct acl_target *) * config->target_count
	);

	FILTER_FREE(&config->filter_vlan, ACL_FILTER_VLAN_TAG);
	FILTER_FREE(&config->filter_ip4, ACL_FILTER_IP4_TAG);
	FILTER_FREE(&config->filter_ip4_port, ACL_FILTER_IP4_PROTO_PORT_TAG);
	FILTER_FREE(&config->filter_ip6, ACL_FILTER_IP6_TAG);
	FILTER_FREE(&config->filter_ip6_port, ACL_FILTER_IP6_PROTO_PORT_TAG);

	// Note: We don't destroy fwstate_cfg maps here because they're owned by
	// the fwstate module. We only stored offsets to them.
	memory_bfree(
		&agent->memory_context,
		cp_module,
		sizeof(struct acl_module_config)
	);
}

typedef int (*acl_rule_check_func)(const struct acl_rule *acl_rule);

static uint32_t
filter_acl_rules(
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count,
	struct filter_rule *filter_rules,
	acl_rule_check_func check
	// TODO: should be there an instantiation callback??
) {
	uint32_t filter_rule_idx = 0;
	for (uint32_t acl_rule_idx = 0; acl_rule_idx < acl_rule_count;
	     ++acl_rule_idx) {
		struct acl_rule *acl_rule = acl_rules + acl_rule_idx;
		if (!check(acl_rule))
			continue;

		struct filter_rule *filter_rule =
			filter_rules + filter_rule_idx++;
		filter_rule->device_count = acl_rule->devices.count;
		filter_rule->devices = acl_rule->devices.items;

		filter_rule->vlan_range_count = acl_rule->vlan_ranges.count;
		filter_rule->vlan_ranges = acl_rule->vlan_ranges.items;

		filter_rule->net4.src_count = acl_rule->src_net4s.count;
		filter_rule->net4.srcs = acl_rule->src_net4s.items;
		filter_rule->net4.dst_count = acl_rule->dst_net4s.count;
		filter_rule->net4.dsts = acl_rule->dst_net4s.items;

		filter_rule->net6.src_count = acl_rule->src_net6s.count;
		filter_rule->net6.srcs = acl_rule->src_net6s.items;
		filter_rule->net6.dst_count = acl_rule->dst_net6s.count;
		filter_rule->net6.dsts = acl_rule->dst_net6s.items;

		filter_rule->transport.proto_count =
			acl_rule->proto_ranges.count;
		filter_rule->transport.protos = acl_rule->proto_ranges.items;

		filter_rule->transport.src_count =
			acl_rule->src_port_ranges.count;
		filter_rule->transport.srcs = acl_rule->src_port_ranges.items;

		filter_rule->transport.dst_count =
			acl_rule->dst_port_ranges.count;
		filter_rule->transport.dsts = acl_rule->dst_port_ranges.items;

		filter_rule->action = acl_rule_idx;
	}

	return filter_rule_idx;
}

static int
check_acl_rule_l2(const struct acl_rule *acl_rule) {
	return !acl_rule->src_net6s.count && !acl_rule->dst_net6s.count &&
	       !acl_rule->src_net4s.count && !acl_rule->dst_net4s.count;
}

static int
check_has_ip4(const struct acl_rule *acl_rule) {
	return acl_rule->src_net4s.count && acl_rule->dst_net4s.count;
}

static int
check_has_ip6(const struct acl_rule *acl_rule) {
	return acl_rule->src_net6s.count && acl_rule->dst_net6s.count;
}

static int
check_has_full_src_port_range(const struct acl_rule *acl_rule) {
	return acl_rule->src_port_ranges.count == 0 ||
	       (acl_rule->src_port_ranges.items[0].from == 0 &&
		acl_rule->src_port_ranges.items[0].to == 65535);
}

static int
check_has_full_dst_port_range(const struct acl_rule *acl_rule) {
	return acl_rule->dst_port_ranges.count == 0 ||
	       (acl_rule->dst_port_ranges.items[0].from == 0 &&
		acl_rule->dst_port_ranges.items[0].to == 65535);
}

static int
check_has_full_port_range(const struct acl_rule *acl_rule) {
	return check_has_full_src_port_range(acl_rule) &&
	       check_has_full_dst_port_range(acl_rule);
}

static int
check_acl_rule_ip4(const struct acl_rule *acl_rule) {
	return check_has_ip4(acl_rule) && check_has_full_port_range(acl_rule);
}

static int
check_acl_rule_ip6(const struct acl_rule *acl_rule) {
	return check_has_ip6(acl_rule) && check_has_full_port_range(acl_rule);
}

static int
check_acl_rule_ip4_port(const struct acl_rule *acl_rule) {
	return check_has_ip4(acl_rule) && !check_has_full_port_range(acl_rule);
}

static int
check_acl_rule_ip6_port(const struct acl_rule *acl_rule) {
	return check_has_ip6(acl_rule) && !check_has_full_port_range(acl_rule);
}

static int
acl_module_init_l2(
	struct cp_module *cp_module,
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count,
	struct filter_rule *filter_rules
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);

	uint32_t filter_rule_count = filter_acl_rules(
		acl_rules, acl_rule_count, filter_rules, check_acl_rule_l2
	);

	return FILTER_INIT(
		&config->filter_vlan,
		ACL_FILTER_VLAN_TAG,
		filter_rules,
		filter_rule_count,
		&cp_module->memory_context
	);
}

static int
acl_module_init_ip4(
	struct cp_module *cp_module,
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count,
	struct filter_rule *filter_rules
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);

	uint32_t filter_rule_count = filter_acl_rules(
		acl_rules, acl_rule_count, filter_rules, check_acl_rule_ip4
	);

	return FILTER_INIT(
		&config->filter_ip4,
		ACL_FILTER_IP4_TAG,
		filter_rules,
		filter_rule_count,
		&cp_module->memory_context
	);
}

static int
acl_module_init_ip4_port(
	struct cp_module *cp_module,
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count,
	struct filter_rule *filter_rules
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);

	uint32_t filter_rule_count = filter_acl_rules(
		acl_rules, acl_rule_count, filter_rules, check_acl_rule_ip4_port
	);

	return FILTER_INIT(
		&config->filter_ip4_port,
		ACL_FILTER_IP4_PROTO_PORT_TAG,
		filter_rules,
		filter_rule_count,
		&cp_module->memory_context
	);
}

static int
acl_module_init_ip6(
	struct cp_module *cp_module,
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count,
	struct filter_rule *filter_rules
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);

	uint32_t filter_rule_count = filter_acl_rules(
		acl_rules, acl_rule_count, filter_rules, check_acl_rule_ip6
	);

	return FILTER_INIT(
		&config->filter_ip6,
		ACL_FILTER_IP6_TAG,
		filter_rules,
		filter_rule_count,
		&cp_module->memory_context
	);
}

static int
acl_module_init_ip6_port(
	struct cp_module *cp_module,
	struct acl_rule *acl_rules,
	uint32_t acl_rule_count,
	struct filter_rule *filter_rules
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);

	uint32_t filter_rule_count = filter_acl_rules(
		acl_rules, acl_rule_count, filter_rules, check_acl_rule_ip6_port
	);

	return FILTER_INIT(
		&config->filter_ip6_port,
		ACL_FILTER_IP6_PROTO_PORT_TAG,
		filter_rules,
		filter_rule_count,
		&cp_module->memory_context
	);
}

int
acl_module_config_update(
	struct cp_module *cp_module,
	struct acl_rule *acl_rules,
	uint32_t rule_count
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);

	for (uint64_t idx = 0; idx < rule_count; ++idx) {
		struct acl_rule *rule = acl_rules + idx;
		for (uint64_t idx = 0; idx < rule->devices.count; ++idx) {
			if (cp_module_link_device(
				    cp_module,
				    rule->devices.items[idx].name,
				    &rule->devices.items[idx].id
			    )) {
				goto error;
			}
		}
	}

	struct acl_target *targets = (struct acl_target *)memory_balloc(
		&cp_module->memory_context,
		sizeof(struct acl_target) * rule_count
	);
	if (targets == NULL) {
		goto error;
	}

	SET_OFFSET_OF(&config->targets, targets);
	config->target_count = rule_count;

	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		struct acl_rule *acl_rule = acl_rules + idx;
		targets[idx].action = acl_rule->action;
		if (acl_rule->counter[0] == '\0')
			snprintf(
				acl_rule->counter,
				sizeof(acl_rule->counter),
				"rule %d",
				idx
			);
		if ((targets[idx].counter_id = counter_registry_register(
			     &cp_module->counter_registry, acl_rule->counter, 2
		     )) == (uint64_t)-1) {
			goto error_target;
		}
	}

	// Create per filter rule list
	struct filter_rule *filter_rules = (struct filter_rule *)malloc(
		sizeof(struct filter_rule) * rule_count
	);
	if (filter_rules == NULL) {
		goto error_target;
	}

	if (acl_module_init_l2(cp_module, acl_rules, rule_count, filter_rules))
		goto error_target;

	if (acl_module_init_ip4(cp_module, acl_rules, rule_count, filter_rules))
		goto error_target;

	if (acl_module_init_ip4_port(
		    cp_module, acl_rules, rule_count, filter_rules
	    ))
		goto error_target;

	if (acl_module_init_ip6(cp_module, acl_rules, rule_count, filter_rules))
		goto error_target;

	if (acl_module_init_ip6_port(
		    cp_module, acl_rules, rule_count, filter_rules
	    ))
		goto error_target;

	free(filter_rules);

	return 0;

error_target:
	memory_bfree(
		&cp_module->memory_context,
		targets,
		sizeof(struct acl_target) * rule_count
	);

error:
	return -1;
}

void
acl_module_config_set_fwstate_config(
	struct cp_module *cp_module, struct cp_module *fwstate_cp_module
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);

	struct fwstate_module_config *fwstate_config = container_of(
		fwstate_cp_module, struct fwstate_module_config, cp_module
	);

	config->fwstate_cfg.sync_config = fwstate_config->cfg.sync_config;
	EQUATE_OFFSET(
		&config->fwstate_cfg.fw4state, &fwstate_config->cfg.fw4state
	);
	EQUATE_OFFSET(
		&config->fwstate_cfg.fw6state, &fwstate_config->cfg.fw6state
	);
}

void
acl_module_config_transfer_fwstate_config(
	struct cp_module *new_cp_module, struct cp_module *old_cp_module
) {
	struct acl_module_config *new_config = container_of(
		new_cp_module, struct acl_module_config, cp_module
	);

	struct acl_module_config *old_config = container_of(
		old_cp_module, struct acl_module_config, cp_module
	);

	new_config->fwstate_cfg.sync_config =
		old_config->fwstate_cfg.sync_config;
	EQUATE_OFFSET(
		&new_config->fwstate_cfg.fw4state,
		&old_config->fwstate_cfg.fw4state
	);
	EQUATE_OFFSET(
		&new_config->fwstate_cfg.fw6state,
		&old_config->fwstate_cfg.fw6state
	);
}
