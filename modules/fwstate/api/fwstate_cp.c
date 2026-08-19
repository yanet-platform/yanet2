#include <errno.h>
#include <string.h>

#include "fwstate_cp.h"

#include "common/container_of.h"
#include "common/numutils.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/errors/errors.h"
#include "lib/fwstate/config.h"
#include "lib/fwstate/fwmap_typed.h"
#include "lib/fwstate/layermap.h"
#include "modules/fwstate/dataplane/config.h"

static void
fwstate_config_fini(struct fwstate_config *config, struct agent *agent) {
	if (config->fw4state != NULL) {
		fwmap_t *node = ADDR_OF(&config->fw4state);
		while (node != NULL) {
			fwmap_t *next = (fwmap_t *)ADDR_OF(&node->next);
			fwmap4_free(
				fwmap4_from_raw(node), &agent->memory_context
			);
			node = next;
		}
		config->fw4state = NULL;
	}
	if (config->fw6state != NULL) {
		fwmap_t *node = ADDR_OF(&config->fw6state);
		while (node != NULL) {
			fwmap_t *next = (fwmap_t *)ADDR_OF(&node->next);
			fwmap6_free(
				fwmap6_from_raw(node), &agent->memory_context
			);
			node = next;
		}
		config->fw6state = NULL;
	}
}

// Set default timeout values for fwstate configuration
void
fwstate_config_set_defaults(struct fwstate_sync_config *config) {
	memset(config, 0, sizeof(struct fwstate_sync_config));
	config->timeouts.tcp_syn_ack = FW_STATE_DEFAULT_TIMEOUT;
	config->timeouts.tcp_syn = FW_STATE_DEFAULT_TIMEOUT;
	config->timeouts.tcp_fin = FW_STATE_DEFAULT_TIMEOUT;
	config->timeouts.tcp = FW_STATE_DEFAULT_TIMEOUT;
	config->timeouts.udp = 30e9;	  // 30 seconds
	config->timeouts.default_ = 16e9; // 16 seconds
}

static void
fwstate_module_config_destroy(struct cp_module *cp_module);

static int
fwstate_module_setup_maps(
	struct fwstate_module_config *config,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
);

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
) {
	struct fwstate_module_config *config =
		(struct fwstate_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct fwstate_module_config)
		);
	if (config == NULL) {
		yanet_error_add(err, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(
		    &config->cp_module,
		    agent,
		    FWSTATE_MODULE_NAME,
		    name,
		    fwstate_module_config_destroy,
		    err
	    )) {
		yanet_error_add(err, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct fwstate_module_config)
		);
		return NULL;
	}
	// balloc does not zero, and the map create path reads the raw map
	// pointers to reject double creation, so both halves start zeroed.
	memset(&config->cfg, 0, sizeof(struct fwstate_config));
	fwstate_config_set_defaults(&config->sync_config);

	// Register module-level counters.
	// size=2 counters hold [packets, bytes]; size=1 counters hold
	// [packets].
	struct {
		const char *name;
		uint64_t size;
		uint64_t *dst;
	} counters[] = {
		{"fwstate_sync", 2, &config->sync_packets_counter_id},
		{"fwstate_passthrough", 2, &config->passthrough_counter_id},
		{"fwstate_sync_v4_inserted",
		 1,
		 &config->sync_v4_inserted_counter_id},
		{"fwstate_sync_v6_inserted",
		 1,
		 &config->sync_v6_inserted_counter_id},
		{"fwstate_sync_v4_insert_failed",
		 1,
		 &config->sync_v4_insert_failed_counter_id},
		{"fwstate_sync_v6_insert_failed",
		 1,
		 &config->sync_v6_insert_failed_counter_id},
		{"fwstate_sync_v4_suppressed",
		 1,
		 &config->sync_v4_suppressed_counter_id},
		{"fwstate_sync_v6_suppressed",
		 1,
		 &config->sync_v6_suppressed_counter_id},
		{"fwstate_external_dropped",
		 2,
		 &config->external_dropped_counter_id},
		{"fwstate_internal_forwarded",
		 2,
		 &config->internal_forwarded_counter_id},
	};

	for (size_t i = 0; i < sizeof(counters) / sizeof(counters[0]); ++i) {
		uint64_t id = counter_registry_register(
			&config->cp_module.counter_registry,
			counters[i].name,
			counters[i].size,
			err
		);
		if (id == (uint64_t)-1) {
			yanet_error_add(
				err,
				"failed to register counter '%s'",
				counters[i].name
			);
			// Frees directly instead of going through the type
			// destructor.
			//
			// A failed configuration-data setup never reaches a
			// state its own teardown could safely walk. No
			// reference beyond the caller's own has been taken, and
			// no registry has observed the module yet, so nothing
			// is lost by freeing the block here.
			cp_module_fini(&config->cp_module);
			memory_bfree(
				&agent->memory_context,
				config,
				sizeof(struct fwstate_module_config)
			);
			return NULL;
		}
		*counters[i].dst = id;
	}

	// One-shot construction: propagate the replaced config's sync
	// config and borrowed map offsets (or start from defaults for a
	// fresh config), install the caller's final sync config, then
	// create or grow the maps. Nothing mutates the config afterwards.
	if (old != NULL) {
		struct fwstate_module_config *old_config = container_of(
			old, struct fwstate_module_config, cp_module
		);
		config->sync_config = old_config->sync_config;
		EQUATE_OFFSET(&config->cfg.fw4state, &old_config->cfg.fw4state);
		EQUATE_OFFSET(&config->cfg.fw6state, &old_config->cfg.fw6state);
	}

	if (sync_config != NULL) {
		config->sync_config = *sync_config;
	}

	if (fwstate_module_setup_maps(
		    config, index_size, extra_bucket_count, worker_count
	    )) {
		yanet_error_add(err, "failed to setup fwstate maps");
		// The propagated map offsets are borrowed from old: clear
		// them so the destructor frees no borrowed chain.
		config->cfg.fw4state = NULL;
		config->cfg.fw6state = NULL;
		fwstate_module_config_destroy(&config->cp_module);
		return NULL;
	}

	return &config->cp_module;
}

static void
fwstate_module_config_destroy(struct cp_module *cp_module) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&cp_module->agent);

	fwstate_config_fini(&config->cfg, agent);

	cp_module_fini(cp_module);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct fwstate_module_config)
	);
}

void
fwstate_module_config_free(struct cp_module *cp_module) {
	cp_module_release(cp_module);
}

void
fwstate_module_config_detach_maps(struct cp_module *cp_module) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	config->cfg.fw4state = NULL;
	config->cfg.fw6state = NULL;
}

// Create the config's firewall state maps. Static on purpose: the only
// entry point is fwstate_module_config_new.
static int
fwstate_config_create_maps(
	struct cp_module *cp_module,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);
	struct agent *agent = ADDR_OF(&cp_module->agent);

	// Verify maps do not already exist
	if (config->cfg.fw4state != NULL || config->cfg.fw6state != NULL) {
		errno = EEXIST;
		return -1;
	}
	if (worker_count == 0) {
		errno = EINVAL;
		return -1;
	}

	// Configure and create IPv4 firewall state map
	fwmap4_t fw4state = fwmap4_new(
		index_size,
		extra_bucket_count,
		worker_count,
		&agent->memory_context
	);
	if (fw4state.raw == NULL) {
		return -1;
	}
	SET_OFFSET_OF(&config->cfg.fw4state, fw4state.raw);

	// Configure and create IPv6 firewall state map
	fwmap6_t fw6state = fwmap6_new(
		index_size,
		extra_bucket_count,
		worker_count,
		&agent->memory_context
	);
	if (fw6state.raw == NULL) {
		fwmap_t *fw4 = ADDR_OF(&config->cfg.fw4state);
		fwmap4_free(fwmap4_from_raw(fw4), &agent->memory_context);
		config->cfg.fw4state = NULL;
		return -1;
	}
	SET_OFFSET_OF(&config->cfg.fw6state, fw6state.raw);

	return 0;
}

// Prepend a new layer to the config's maps: a map-chain operation, not
// a config update — the config's sync rules and links never change
// after construction.
int
fwstate_config_insert_new_layer(
	struct cp_module *cp_module,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);
	struct agent *agent = ADDR_OF(&cp_module->agent);

	// Verify maps already exist
	if (config->cfg.fw4state == NULL || config->cfg.fw6state == NULL) {
		errno = EINVAL;
		return -1;
	}
	if (worker_count == 0) {
		errno = EINVAL;
		return -1;
	}

	// Configure and insert new layer for IPv4
	if (fwmap4_insert_new_layer_cp(
		    &config->cfg.fw4state,
		    index_size,
		    extra_bucket_count,
		    worker_count,
		    &agent->memory_context
	    )) {
		return -1;
	}

	// Configure and insert new layer for IPv6
	if (fwmap6_insert_new_layer_cp(
		    &config->cfg.fw6state,
		    index_size,
		    extra_bucket_count,
		    worker_count,
		    &agent->memory_context
	    )) {
		// Rollback: remove the IPv4 layer we just added
		fwmap_t *fw4_active = ADDR_OF(&config->cfg.fw4state);
		fwmap_t *fw4_old = ADDR_OF(&fw4_active->next);
		SET_OFFSET_OF(&config->cfg.fw4state, fw4_old);
		fwmap4_free(
			fwmap4_from_raw(fw4_active), &agent->memory_context
		);
		return -1;
	}

	return 0;
}

// Decide between creating fresh maps and growing the propagated ones
// with a new layer: a requested size that differs from the propagated
// maps' aligned current size prepends a new layer carrying the raw
// requested sizes; equal or zero sizes keep the chain untouched. A zero
// worker count leaves the config unmapped — the module then counts and
// drops that family's sync frames without inserting.
static int
fwstate_module_setup_maps(
	struct fwstate_module_config *config,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count
) {
	struct cp_module *cp_module = &config->cp_module;

	if (worker_count == 0) {
		return 0;
	}

	if (config->cfg.fw4state == NULL) {
		return fwstate_config_create_maps(
			cp_module, index_size, extra_bucket_count, worker_count
		);
	}

	fwmap_stats_t stats_v4 = fwstate_config_get_map_stats(cp_module, false);
	fwmap_stats_t stats_v6 = fwstate_config_get_map_stats(cp_module, true);
	uint32_t current_index_size = stats_v4.index_size > stats_v6.index_size
					      ? stats_v4.index_size
					      : stats_v6.index_size;
	uint32_t current_extra_bucket_count =
		stats_v4.extra_bucket_count > stats_v6.extra_bucket_count
			? stats_v4.extra_bucket_count
			: stats_v6.extra_bucket_count;

	uint32_t requested_index_size =
		index_size ? (uint32_t)next_power_of_two(index_size) : 0;
	uint32_t requested_extra_bucket_count =
		extra_bucket_count
			? (uint32_t)next_power_of_two(extra_bucket_count)
			: 0;

	bool map_config_changed = false;
	if (requested_index_size != 0 &&
	    requested_index_size != current_index_size) {
		map_config_changed = true;
		current_index_size = index_size;
	}
	if (requested_extra_bucket_count != 0 &&
	    requested_extra_bucket_count != current_extra_bucket_count) {
		map_config_changed = true;
		current_extra_bucket_count = extra_bucket_count;
	}

	if (!map_config_changed) {
		return 0;
	}

	return fwstate_config_insert_new_layer(
		cp_module,
		current_index_size,
		current_extra_bucket_count,
		worker_count
	);
}

struct fwmap_stats
fwstate_config_get_map_stats(const struct cp_module *cp_module, bool is_ipv6) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	if (is_ipv6) {
		if (config->cfg.fw6state == NULL) {
			return (fwmap_stats_t){0};
		}
		fwmap_t *map = ADDR_OF(&config->cfg.fw6state);
		return fwmap6_get_stats(fwmap6_from_raw(map));
	}

	if (config->cfg.fw4state == NULL) {
		return (fwmap_stats_t){0};
	}
	fwmap_t *map = ADDR_OF(&config->cfg.fw4state);
	return fwmap4_get_stats(fwmap4_from_raw(map));
}

struct fwstate_sync_config
fwstate_config_get_sync_config(const struct cp_module *cp_module) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	return config->sync_config;
}

// Structure to hold outdated layers from both IPv4 and IPv6 maps
struct fwstate_outdated_layers {
	layermap_list_t *v4_layers;
	layermap_list_t *v6_layers;
};

int
fwstate_config_trim_stale_layers(
	struct cp_module *cp_module,
	uint64_t now,
	fwstate_outdated_layers_t **outdated
) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);
	struct agent *agent = ADDR_OF(&cp_module->agent);

	// Allocate structure to hold outdated layers
	fwstate_outdated_layers_t *layers =
		memory_balloc(&agent->memory_context, sizeof(*layers));
	if (!layers) {
		errno = ENOMEM;
		*outdated = NULL;
		return -1;
	}
	layers->v4_layers = NULL;
	layers->v6_layers = NULL;
	*outdated = layers;

	// Attempt both maps unconditionally so each collects independently -
	// a v4 allocation failure must not skip v6 trimming.
	bool failed = false;

	if (config->cfg.fw4state) {
		if (layermap_trim_stale_layers_cp(
			    &config->cfg.fw4state,
			    &agent->memory_context,
			    now,
			    &layers->v4_layers
		    )) {
			failed = true;
		}
	}

	if (config->cfg.fw6state) {
		if (layermap_trim_stale_layers_cp(
			    &config->cfg.fw6state,
			    &agent->memory_context,
			    now,
			    &layers->v6_layers
		    )) {
			failed = true;
		}
	}

	if (failed) {
		errno = ENOMEM;
		if (layers->v4_layers == NULL && layers->v6_layers == NULL) {
			// Nothing was collected: the reordered layermap trim
			// fails before mutating the chain, so this is
			// equivalent to the outer allocation failing.
			memory_bfree(
				&agent->memory_context, layers, sizeof(*layers)
			);
			*outdated = NULL;
		}
		return -1;
	}

	return 0;
}

void
fwstate_outdated_layers_free(
	fwstate_outdated_layers_t *outdated, struct cp_module *cp_module
) {
	if (!outdated) {
		return;
	}

	struct agent *agent = ADDR_OF(&cp_module->agent);

	// Free IPv4 outdated layers
	layermap_list_t *v4_node = outdated->v4_layers;
	while (v4_node) {
		fwmap_t *layer = ADDR_OF(&v4_node->layer);
		layermap_list_t *next =
			(layermap_list_t *)ADDR_OF(&v4_node->next);

		// Free the layer
		fwmap4_free(fwmap4_from_raw(layer), &agent->memory_context);

		// Free the list node
		memory_bfree(&agent->memory_context, v4_node, sizeof(*v4_node));

		v4_node = next;
	}

	// Free IPv6 outdated layers
	layermap_list_t *v6_node = outdated->v6_layers;
	while (v6_node) {
		fwmap_t *layer = ADDR_OF(&v6_node->layer);
		layermap_list_t *next =
			(layermap_list_t *)ADDR_OF(&v6_node->next);

		// Free the layer
		fwmap6_free(fwmap6_from_raw(layer), &agent->memory_context);

		// Free the list node
		memory_bfree(&agent->memory_context, v6_node, sizeof(*v6_node));

		v6_node = next;
	}

	// Free the outdated structure itself
	memory_bfree(&agent->memory_context, outdated, sizeof(*outdated));
}

fwmap_t *
fwstate_config_resolve_map(
	const struct cp_module *cp_module, bool is_ipv6, uint32_t layer_index
) {
	const struct fwstate_module_config *config = container_of(
		cp_module, const struct fwstate_module_config, cp_module
	);

	fwmap_t **map_offset;
	if (is_ipv6) {
		map_offset = (fwmap_t **)&config->cfg.fw6state;
	} else {
		map_offset = (fwmap_t **)&config->cfg.fw4state;
	}

	if (*map_offset == NULL) {
		return NULL;
	}

	fwmap_t *map = ADDR_OF(map_offset);

	for (uint32_t i = 0; i < layer_index; i++) {
		if (map->next == NULL) {
			return NULL;
		}
		map = (fwmap_t *)ADDR_OF(&map->next);
	}

	return map;
}

int
fwstate_config_cursor_init(
	struct cp_module *cp_module,
	fwstate_cursor_t *cursor,
	bool is_ipv6,
	uint32_t layer_index,
	int64_t index,
	bool include_expired
) {
	// Verify the map/layer exists
	fwmap_t *map =
		fwstate_config_resolve_map(cp_module, is_ipv6, layer_index);
	if (map == NULL) {
		errno = EINVAL;
		return -1;
	}

	cursor->key_pos = index;
	cursor->include_expired = include_expired;

	return 0;
}
