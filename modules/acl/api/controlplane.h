#pragma once

#include "fwstate/config.h"
#include <stdint.h>

#include "filter/rule.h"

#include "counters/counters.h"

#include "controlplane/config/defines.h"

#define ACL_ACTION_ALLOW 0
#define ACL_ACTION_DENY 1

struct agent;
struct cp_module;

struct cp_module *
acl_module_config_init(struct agent *agent, const char *name);

void
acl_module_config_free(struct cp_module *cp_module);

struct acl_rule {
	uint64_t action;
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
	struct cp_module *cp_module, struct acl_rule *rules, uint32_t rule_count
);

void
acl_module_config_set_fwstate_config(
	struct cp_module *cp_module, struct cp_module *fwstate_cp_module
);
