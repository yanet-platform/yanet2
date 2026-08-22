#pragma once

#include <stdint.h>

#include "lib/errors/errors.h"

struct agent;
struct cp_module;
struct memory_context;
struct decap_module_config;

struct cp_module *
decap_module_config_new(
	struct agent *agent, const char *name, yanet_error **err
);

// Destroy the module when it is dangling, per cp_module_try_destroy.
//
// Returns -1 with errno EAGAIN while a live generation still references
// the module; the caller must keep its handle and retry later.
int
decap_module_config_free(struct cp_module *cp_module, yanet_error **err);

int
decap_module_config_data_init(
	struct decap_module_config *config,
	struct memory_context *memory_context
);

void
decap_module_config_data_fini(struct decap_module_config *config);

int
decap_module_config_add_prefix_v4(
	struct cp_module *cp_module, const uint8_t *from, const uint8_t *to
);

int
decap_module_config_add_prefix_v6(
	struct cp_module *cp_module, const uint8_t *from, const uint8_t *to
);
