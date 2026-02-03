#pragma once

#include <stdint.h>

struct agent;
struct cp_mocule;

struct cp_module *
proxy_module_config_init(struct agent *agent, const char *name);

void
proxy_module_config_free(struct cp_module *cp_module);

int
proxy_module_config_delete(struct cp_module *cp_module);

int proxy_module_config_set_conn_table_size(struct cp_module *cp_module, uint32_t size);