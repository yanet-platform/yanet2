#pragma once

#include "common/lpm.h"

#include "dataplane/config/zone.h"

struct forward_device_config {
	uint16_t l2_dst_device_id;
	struct lpm lpm_v4;
	struct lpm lpm_v6;
};

struct forward_module_config {
	struct module_data module_data;

	uint64_t device_count;
	struct forward_device_config device_forwards[];
};
