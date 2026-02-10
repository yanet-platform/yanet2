#pragma once

#include <stdint.h>

struct agent;
struct cp_module;
struct proxy_state;

struct cp_module *
proxy_module_config_init(struct agent *agent, const char *name, struct proxy_state *state);

void
proxy_module_config_free(struct cp_module *cp_module);

int
proxy_module_config_delete(struct cp_module *cp_module);

struct proxy_state *
proxy_state_create(struct agent *agent, uint32_t size_conn_table);

void
proxy_state_destroy(struct proxy_state *state);