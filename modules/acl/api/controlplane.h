#pragma once

#include <stdint.h>

#include "filter/rule.h"

#include "counters/counters.h"

#include "lib/errors/errors.h"

enum acl_rule_action_kind {
	ACL_RULE_ACTION_KIND_ALLOW,
	ACL_RULE_ACTION_KIND_DENY,
	ACL_RULE_ACTION_KIND_COUNT,
	ACL_RULE_ACTION_KIND_CHECK_STATE,
	ACL_RULE_ACTION_KIND_CREATE_STATE,
	ACL_RULE_ACTION_KIND_LOG
};

struct agent;
struct cp_module;

struct cp_module *
acl_module_config_init(
	struct agent *agent, const char *name, yanet_error **err
);

void
acl_module_config_free(struct cp_module *cp_module);

struct acl_action {
	enum acl_rule_action_kind kind;
};

struct acl_rule {
	struct acl_action *actions;
	uint64_t action_count;

	char counter[COUNTER_NAME_LEN];

	struct filter_devices devices;
	struct filter_vlan_ranges vlan_ranges;

	struct filter_net4s src_net4s;
	struct filter_net4s dst_net4s;

	struct filter_net6s src_net6s;
	struct filter_net6s dst_net6s;

	struct filter_proto_ranges proto_ranges;

	struct filter_port_ranges src_port_ranges;
	struct filter_port_ranges dst_port_ranges;
};

int
acl_module_config_update(
	struct cp_module *cp_module,
	struct acl_rule *rules,
	uint32_t rule_count,
	yanet_error **err
);

void
acl_module_config_set_fwstate_config(
	struct cp_module *cp_module, struct cp_module *fwstate_cp_module
);

void
acl_module_config_transfer_fwstate_config(
	struct cp_module *new_cp_module, struct cp_module *old_cp_module
);

struct acl_config_info {
	uint64_t compilation_time_ns;
	uint64_t filter_rule_count_ip4;
	uint64_t filter_rule_count_ip4_port;
	uint64_t filter_rule_count_ip6;
	uint64_t filter_rule_count_ip6_port;
	uint64_t filter_rule_count_vlan;
};

void
acl_module_config_get_info(
	struct cp_module *cp_module, struct acl_config_info *info
);
