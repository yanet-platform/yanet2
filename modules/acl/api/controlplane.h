#pragma once

#include <stdint.h>

#define ACTION_LOG_FLAG ((uint64_t)(1ull << 0))
#define ACTION_KEEP_STATE_FLAG ((uint64_t)(1ull << 1))

struct agent;
struct cp_module;

struct cp_module *
acl_module_config_init(struct agent *agent, const char *name);

void
acl_module_config_free(struct cp_module *cp_module);

struct filter_rule;

int
acl_module_compile(
	struct cp_module *cp_module,
	struct filter_rule *actions,
	uint32_t action_count
);
