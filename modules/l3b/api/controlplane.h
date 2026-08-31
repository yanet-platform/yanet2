#pragma once

struct cp_module;
struct agent;

#include "lib/errors/errors.h"

struct cp_module *
l3b_module_config_new(
	struct agent *agent, const char *name, yanet_error **error
);

void
l3b_module_config_free(struct cp_module *config);
