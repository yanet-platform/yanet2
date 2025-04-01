#pragma once

#include <stdint.h>

struct agent;
struct module_data;

struct module_data *
decap_module_config_init(struct agent *agent, const char *name);

void
decap_module_config_free(struct module_data *module_data);

int
decap_module_config_add_prefix_v4(
	struct module_data *module_data, const uint8_t *from, const uint8_t *to
);

int
decap_module_config_add_prefix_v6(
	struct module_data *module_data, const uint8_t *from, const uint8_t *to
);
