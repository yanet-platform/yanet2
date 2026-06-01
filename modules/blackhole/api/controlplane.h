#pragma once

struct cp_module;
struct agent;

#include "lib/errors/errors.h"

struct cp_module *
blackhole_module_config_init(
	struct agent *agent, const char *name, yanet_error **error
);

void
blackhole_module_config_free(struct cp_module *config);
