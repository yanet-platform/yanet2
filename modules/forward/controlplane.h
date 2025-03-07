#pragma once

#include "common/network.h"

struct agent;
struct module_data;

struct module_data *
forward_module_config_init(
	struct agent *agent, const char *name, uint16_t device_count
);

int
forward_module_config_enable_v4(
	struct module_data *module_data,
	const uint8_t *from,
	const uint8_t *to,
	uint16_t src_device_id,
	uint16_t dst_device_id
);

int
forward_module_config_enable_v6(
	struct module_data *module_data,
	const uint8_t *from,
	const uint8_t *to,
	uint16_t src_device_id,
	uint16_t dst_device_id
);

int
forward_module_config_enable_l2(
	struct module_data *module_data,
	uint16_t src_device_id,
	uint16_t dst_device_id
);
