#pragma once

#include <stdint.h>

struct agent;
struct cp_module;
struct memory_context;
struct decap_module_config;

struct cp_module *
decap_module_config_create(struct agent *agent, const char *name);

void
decap_module_config_free(struct cp_module *cp_module);

int
decap_module_config_data_init(
	struct decap_module_config *config,
	struct memory_context *memory_context
);

void
decap_module_config_data_destroy(struct decap_module_config *config);

int
decap_module_config_add_prefix_v4(
	struct cp_module *cp_module, const uint8_t *from, const uint8_t *to
);

int
decap_module_config_add_prefix_v6(
	struct cp_module *cp_module, const uint8_t *from, const uint8_t *to
);
