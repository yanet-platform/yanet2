#pragma once

#include "defines.h"

#include "common/exp_array.h"
#include "common/lpm.h"

#include "dataplane/config/zone.h"

struct balancer_vs {
	uint64_t type;
	uint8_t address[16];
	uint64_t real_start;
	uint64_t real_count;
};

struct balancer_rs {
	uint64_t type;
	uint8_t dst_addr[16];
	uint8_t src_addr[16];
	uint8_t src_mask[16];
};

struct balancer_module_config {
	struct module_data module_data;

	struct lpm v4_service_lookup;
	struct lpm v6_service_lookup;

	uint64_t service_count;
	struct balancer_vs *services;

	uint64_t real_count;
	struct balancer_rs *reals;
};
