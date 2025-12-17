#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "fwstate/fwmap.h"

struct agent;
struct cp_module;
struct fwstate_sync_config;

struct cp_module *
fwstate_module_config_init(
	struct agent *agent, const char *name, struct cp_module *old_cp_module
);

void
fwstate_module_config_free(struct cp_module *cp_module);

// Create firewall state maps for the configuration
int
fwstate_config_create_maps(
	struct cp_module *cp_module,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
);

void
fwstate_module_config_set_sync_config(
	struct cp_module *cp_module, struct fwstate_sync_config *sync_config
);

struct fwmap_stats
fwstate_config_get_map_stats(const struct cp_module *cp_module, bool is_ipv6);

struct fwstate_sync_config
fwstate_config_get_sync_config(const struct cp_module *cp_module);
