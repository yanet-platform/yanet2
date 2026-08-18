#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "lib/errors/errors.h"
#include "lib/fwstate/fwmap.h"
#include "lib/fwstate/fwstate_cursor.h"

struct agent;
struct cp_module;
struct fwstate_sync_config;
struct layermap_list;

// Opaque handle for outdated layers that need to be freed
typedef struct fwstate_outdated_layers fwstate_outdated_layers_t;

// Set the C-side default receive-side sync timeouts.
void
fwstate_config_set_defaults(struct fwstate_sync_config *config);

// Allocate an fwstate module config fully built in one step: a config
// handle is created once and never updated afterwards.
//
// old names the config this one replaces, or NULL for a fresh config.
// From old the sync config and the borrowed map offsets propagate; with
// old NULL the maps are created fresh from index_size and
// extra_bucket_count (zero falls back to the fwmap defaults) and the
// sync config starts from fwstate_config_set_defaults.
//
// When old is given, its maps are kept unless the aligned
// index_size/extra_bucket_count pair differs from the sizes the
// propagated maps carry, in which case a new layer with the requested
// sizes is prepended to both chains.
//
// sync_config is the final receive-side sync config to install, or NULL
// to keep the propagated or default one. A zero worker_count leaves the
// config unmapped: no maps are created and the propagated chain, if
// any, is kept as is.
//
// Returns NULL with err set on failure; nothing is left allocated.
struct cp_module *
fwstate_module_config_new(
	struct agent *agent,
	const char *name,
	struct cp_module *old,
	const struct fwstate_sync_config *sync_config,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count,
	yanet_error **err
);

void
fwstate_module_config_free(struct cp_module *cp_module);

void
fwstate_module_config_detach_maps(struct cp_module *cp_module);

// Prepend a new layer to the config's existing maps. This grows the map
// chain only: the config's sync rules and links never change after
// construction.
int
fwstate_config_insert_new_layer(
	struct cp_module *cp_module,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
);

struct fwmap_stats
fwstate_config_get_map_stats(const struct cp_module *cp_module, bool is_ipv6);

struct fwstate_sync_config
fwstate_config_get_sync_config(const struct cp_module *cp_module);

// Trims stale layers from both the IPv4 and IPv6 maps.
//
// Returns 0 when both maps were fully trimmed, or -1 with errno set when
// trimming stopped early because a bookkeeping-node allocation failed. On
// success *outdated is always non-NULL, though it holds no layers when
// nothing was stale. On failure it is non-NULL only when at least one layer
// was collected, which marks a genuine partial trim. It is NULL when the
// outdated-layers structure itself cannot be allocated, and when trimming
// failed before collecting anything - in both cases the layer chain is left
// untouched. Collected layers must still be released with
// fwstate_outdated_layers_free after the new config is published, regardless
// of the return value.
int
fwstate_config_trim_stale_layers(
	struct cp_module *cp_module,
	uint64_t now,
	fwstate_outdated_layers_t **outdated
);

// Free outdated layers after successful UpdateModules
void
fwstate_outdated_layers_free(
	fwstate_outdated_layers_t *outdated, struct cp_module *cp_module
);

// Resolve a specific layer's map pointer from the config.
// Returns NULL if maps don't exist or layer_index is out of range.
fwmap_t *
fwstate_config_resolve_map(
	const struct cp_module *cp_module, bool is_ipv6, uint32_t layer_index
);

// Construct a cursor for a specific layer.
// Copies timeouts from the current config into the cursor.
// Sets key_pos from the provided `index` parameter.
// Returns 0 on success, -1 if map/layer cannot be resolved.
int
fwstate_config_cursor_init(
	struct cp_module *cp_module,
	fwstate_cursor_t *cursor,
	bool is_ipv6,
	uint32_t layer_index,
	int64_t index,
	bool include_expired
);
