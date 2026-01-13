#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "lib/fwstate/fwmap.h"

struct agent;
struct cp_module;
struct fwstate_sync_config;
struct layermap_list;

// Opaque handle for outdated layers that need to be freed
typedef struct fwstate_outdated_layers fwstate_outdated_layers_t;

struct cp_module *
fwstate_module_config_init(struct agent *agent, const char *name);

void
fwstate_module_config_propogate(
	struct cp_module *new_cp_module, struct cp_module *old_cp_module
);

void
fwstate_module_config_free(struct cp_module *cp_module);

void
fwstate_module_config_detach_maps(struct cp_module *cp_module);

// Create firewall state maps for the configuration
int
fwstate_config_create_maps(
	struct cp_module *cp_module,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
);

// Insert new layer to existing firewall state maps
int
fwstate_config_insert_new_layer(
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

// Trim stale layers from both IPv4 and IPv6 maps
// Returns handle to outdated layers that should be freed after UpdateModules
// Returns NULL on error (errno is set)
fwstate_outdated_layers_t *
fwstate_config_trim_stale_layers(struct cp_module *cp_module, uint64_t now);

// Free outdated layers after successful UpdateModules
void
fwstate_outdated_layers_free(
	fwstate_outdated_layers_t *outdated, struct cp_module *cp_module
);
