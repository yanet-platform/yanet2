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

	struct filter_net6 net6;
	struct filter_net4 net4;

	uint16_t device_count;
	struct filter_device *devices;

	uint16_t vlan_range_count;
	struct filter_vlan_range *vlan_ranges;

	uint16_t proto_range_count;
	struct filter_proto_range *proto_ranges;

	uint16_t src_port_range_count;
	struct filter_port_range *src_port_ranges;

	uint16_t dst_port_range_count;
	struct filter_port_range *dst_port_ranges;

	char counter[COUNTER_NAME_LEN];
};

int
acl_module_config_update(
	struct cp_module *cp_module, struct acl_rule *rules, uint32_t rule_count
);

void
acl_module_config_set_fwstate_config(
	struct cp_module *cp_module, struct cp_module *fwstate_cp_module
);
