#pragma once

#include "module.h"

#include "filter/ipfw.h"

#define VS_OPT_ENCAP 0x01
#define VS_OPT_GRE 0x02

#define RS_TYPE_V4 0x01
#define RS_TYPE_V6 0x02

struct balancer_vs {
	uint32_t options;
	uint32_t real_start;
	uint32_t real_count;
};

struct balancer_rs {
	uint32_t type;
	uint8_t *dst_addr;
};

struct balancer_module_config {
	struct module_config config;

	struct filter_compiler filter;

	struct balancer_vs *services;
	uint32_t vs_count;

	struct balancer_rs *reals;
	uint32_t rs_count;

	uint8_t source_v4[4];
	uint8_t source_mask_v4[4];

	uint8_t source_v6[16];
	uint8_t source_mask_v6[16];
};

struct balancer_module {
	struct module module;
};

struct module *
new_module_balancer();
