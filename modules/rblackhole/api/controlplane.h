#pragma once

#include <stdint.h>

#include "lib/errors/errors.h"

struct agent;
struct cp_module;
struct rblackhole_module_config;

struct cp_module *
rblackhole_module_config_new(
	struct agent *agent, const char *name, yanet_error **err
);

// Destroy the module when it is dangling, per cp_module_try_destroy.
//
// Returns -1 with errno EAGAIN while a live generation still references
// the module; the caller must keep its handle and retry later.
int
rblackhole_module_config_free(struct cp_module *cp_module, yanet_error **err);

int
rblackhole_module_config_add_prefix_v4(
	struct cp_module *cp_module, const uint8_t *from, const uint8_t *to
);

int
rblackhole_module_config_add_prefix_v6(
	struct cp_module *cp_module, const uint8_t *from, const uint8_t *to
);

// Link a device to the config, so passed packets can be routed to its
// output entry. Returns the link index through `index`.
int
rblackhole_module_config_add_device(
	struct cp_module *cp_module,
	const char *name,
	uint64_t *index,
	yanet_error **err
);
