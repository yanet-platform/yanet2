#pragma once

#include <stddef.h>
#include <stdint.h>

#include "rule.h"

struct agent;
struct cp_module;

struct cp_module *
acl_module_config_create(
	struct agent *agent,
	const char *name,
	size_t rule_count,
	acl_rule_t *rules
);

void
acl_module_config_free(struct cp_module *cp_module);