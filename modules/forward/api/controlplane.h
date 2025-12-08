#pragma once

#include <stdint.h>

#include "filter/rule.h"

#include "counters/counters.h"

#include "controlplane/config/defines.h"

#define FORWARD_MODE_NONE 0
#define FORWARD_MODE_IN 1
#define FORWARD_MODE_OUT 2

struct agent;
struct cp_module;

struct cp_module *
forward_module_config_init(struct agent *agent, const char *name);

void
forward_module_config_free(struct cp_module *cp_module);

struct forward_rule {
	struct filter_net6 net6;
	struct filter_net4 net4;

	uint16_t device_count;
	struct filter_device *devices;

	uint16_t vlan_range_count;
	struct filter_vlan_range *vlan_ranges;

	char target[CP_DEVICE_NAME_LEN];
	char counter[COUNTER_NAME_LEN];

	uint8_t mode;
};

int
forward_module_config_update(
	struct cp_module *cp_module,
	struct forward_rule *rules,
	uint32_t rule_count
);

// Enables deletion of configurations for the forwarding module.
// @return Returns -1 on error and 0 on success.
int
forward_module_config_delete(struct cp_module *cp_module);
