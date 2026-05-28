#pragma once

#include <stdint.h>

#include <filter/rule.h>

#include "counters/counters.h"

#include "controlplane/config/defines.h"

#include "lib/errors/errors.h"

#define FORWARD_MODE_NONE 0
#define FORWARD_MODE_IN 1
#define FORWARD_MODE_OUT 2

struct agent;
struct cp_module;

struct cp_module *
forward_module_config_init(
	struct agent *agent, const char *name, yanet_error **err
);

void
forward_module_config_free(struct cp_module *cp_module);

struct forward_rule {
	char target[CP_DEVICE_NAME_LEN];
	char counter[COUNTER_NAME_LEN];

	uint8_t mode;

	struct filter_devices devices;
	struct filter_vlan_ranges vlan_ranges;

	struct filter_net4s src_net4s;
	struct filter_net4s dst_net4s;

	struct filter_net6s src_net6s;
	struct filter_net6s dst_net6s;
};

int
forward_module_config_update(
	struct cp_module *cp_module,
	struct forward_rule *rules,
	uint32_t rule_count,
	yanet_error **err
);
