#pragma once

#include "config.h"

////////////////////////////////////////////////////////////////////////////////

struct my_module_config *
my_module_config_create(struct agent *agent, const char *name);

void
my_module_config_free(struct my_module_config *config);
