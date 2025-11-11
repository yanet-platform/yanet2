#pragma once

#include "filter/filter.h"

#include "controlplane/config/zone.h"

#define FORWARD_DIRECTION_IN 0
#define FORWARD_DIRECTION_OUT 1

struct forward_target {
	uint64_t device_id;
	uint64_t counter_id;
	uint8_t direction;
};

struct forward_module_config {
	struct cp_module cp_module;

	struct filter filter_ip4;
	struct filter filter_ip6;
	struct filter filter_vlan;

	uint64_t target_count;
	struct forward_target *targets;
};
