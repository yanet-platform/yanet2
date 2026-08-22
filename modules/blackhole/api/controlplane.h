#pragma once

struct cp_module;
struct agent;

#include "lib/errors/errors.h"

struct cp_module *
blackhole_module_config_new(
	struct agent *agent, const char *name, yanet_error **error
);

// Destroy the module when it is dangling, per cp_module_try_destroy.
//
// Returns -1 with errno EAGAIN while a live generation still references
// the module; the caller must keep its handle and retry later.
int
blackhole_module_config_free(struct cp_module *cp_module, yanet_error **err);
