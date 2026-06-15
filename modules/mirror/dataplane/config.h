#pragma once

#include <filter/filter.h>

#include "controlplane/config/cp_module.h"

#define MIRROR_MODE_NONE 0
#define MIRROR_MODE_IN 1
#define MIRROR_MODE_OUT 2

struct mirror_target {
	uint64_t device_id;
	uint64_t counter_id;
	uint8_t mode;
};

struct mirror_module_config {
	struct cp_module cp_module;

	struct filter filter_ip4;
	struct filter filter_ip6;
	struct filter filter_vlan;

	uint64_t target_count;
	struct mirror_target *targets;
};
