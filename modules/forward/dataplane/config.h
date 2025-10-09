#pragma once

#include "common/lpm.h"

#include "controlplane/config/zone.h"

struct forward_target {
	uint64_t device_id;
	uint64_t counter_id;
};

struct forward_device_config {
	uint32_t l2_target_id;
	struct lpm lpm_v4;
	struct lpm lpm_v6;
	uint64_t target_count;
	struct forward_target *targets;
};

struct forward_module_config {
	struct cp_module cp_module;

	uint64_t device_count;
	struct forward_device_config **devices;
};
