#include <errno.h>
#include <string.h>

#include "fwstate_cp.h"

#include "common/container_of.h"
#include "controlplane/agent/agent.h"
#include "lib/fwstate/config.h"
#include "lib/fwstate/layermap.h"
#include "modules/fwstate/dataplane/config.h"

static void
fwstate_config_destroy(struct fwstate_config *config, struct agent *agent) {
	if (config->fw4state != NULL) {
		fwmap_t *fw4 = ADDR_OF(&config->fw4state);
		fwmap_destroy(fw4, &agent->memory_context);
		config->fw4state = NULL;
	}
	if (config->fw6state != NULL) {
		fwmap_t *fw6 = ADDR_OF(&config->fw6state);
		fwmap_destroy(fw6, &agent->memory_context);
		config->fw6state = NULL;
	}
}

// Set default timeout values for fwstate configuration
static void
fwstate_config_set_defaults(struct fwstate_config *config) {
	memset(config, 0, sizeof(struct fwstate_config));
	config->sync_config.timeouts.tcp_syn_ack = FW_STATE_DEFAULT_TIMEOUT;
	config->sync_config.timeouts.tcp_syn = FW_STATE_DEFAULT_TIMEOUT;
	config->sync_config.timeouts.tcp_fin = FW_STATE_DEFAULT_TIMEOUT;
	config->sync_config.timeouts.tcp = FW_STATE_DEFAULT_TIMEOUT;
	config->sync_config.timeouts.udp = 30e9;      // 30 seconds
	config->sync_config.timeouts.default_ = 16e9; // 16 seconds
}

struct cp_module *
fwstate_module_config_init(struct agent *agent, const char *name) {
	struct fwstate_module_config *config =
		(struct fwstate_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct fwstate_module_config)
		);
	if (config == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	if (cp_module_init(
		    &config->cp_module, agent, FWSTATE_MODULE_NAME, name
	    )) {
		int prev_errno = errno;
		fwstate_module_config_free(&config->cp_module);
		errno = prev_errno;
		return NULL;
	}
	fwstate_config_set_defaults(&config->cfg);
	return &config->cp_module;
}

void
fwstate_module_config_propogate(
	struct cp_module *new_cp_module, struct cp_module *old_cp_module
) {
	struct fwstate_module_config *new = container_of(
		new_cp_module, struct fwstate_module_config, cp_module
	);

	struct fwstate_module_config *old = container_of(
		old_cp_module, struct fwstate_module_config, cp_module
	);

	new->cfg = old->cfg; // copy sync config
	EQUATE_OFFSET(&new->cfg.fw4state, &old->cfg.fw4state);
	EQUATE_OFFSET(&new->cfg.fw6state, &old->cfg.fw6state);
}

void
fwstate_module_config_free(struct cp_module *cp_module) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	struct agent *agent = ADDR_OF(&cp_module->agent);
	if (agent) {
		fwstate_config_destroy(&config->cfg, agent);
	}

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct fwstate_module_config)
	);
}

void
fwstate_module_config_detach_maps(struct cp_module *cp_module) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	config->cfg.fw4state = NULL;
	config->cfg.fw6state = NULL;
}

// Helper function to initialize fwmap config
static inline void
fwstate_init_config(
	fwmap_config_t *config,
	uint16_t key_size,
	fwmap_func_id_t key_equal_fn_id,
	fwmap_func_id_t copy_key_fn_id,
	uint32_t index_size,
	uint32_t extra_bucket_count,
	uint16_t worker_count

) {
	if (index_size == 0) {
		index_size = 1024 * 1024; // Default: 1M entries
	}
	if (extra_bucket_count == 0) {
		extra_bucket_count = 1024; // Default: 1024 extra buckets
	}

	config->key_size = key_size;
	config->key_equal_fn_id = key_equal_fn_id;
	config->copy_key_fn_id = copy_key_fn_id;

	config->value_size = sizeof(struct fw_state_value);
	config->copy_value_fn_id = FWMAP_COPY_VALUE_FWSTATE;
	config->merge_value_fn_id = FWMAP_MERGE_VALUE_FWSTATE;

	config->hash_seed = 0;
	config->hash_fn_id = FWMAP_HASH_FNV1A;

	config->worker_count = worker_count;
	config->index_size = index_size;
	config->extra_bucket_count = extra_bucket_count;
	config->rand_fn_id = FWMAP_RAND_DEFAULT;
}

int
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
	fwmap_config_t fw4_config;
	fwstate_init_config(
		&fw4_config,
		sizeof(struct fw4_state_key),
		FWMAP_KEY_EQUAL_FW4,
		FWMAP_COPY_KEY_FW4,
		index_size,
		extra_bucket_count,
		worker_count
	);

	fwmap_t *fw4state = fwmap_new(&fw4_config, &agent->memory_context);
	if (fw4state == NULL) {
		return -1;
	}
	SET_OFFSET_OF(&config->cfg.fw4state, fw4state);

	// Configure and create IPv6 firewall state map
	fwmap_config_t fw6_config;
	fwstate_init_config(
		&fw6_config,
		sizeof(struct fw6_state_key),
		FWMAP_KEY_EQUAL_FW6,
		FWMAP_COPY_KEY_FW6,
		index_size,
		extra_bucket_count,
		worker_count
	);

	fwmap_t *fw6state = fwmap_new(&fw6_config, &agent->memory_context);
	if (fw6state == NULL) {
		fwmap_t *fw4 = ADDR_OF(&config->cfg.fw4state);
		fwmap_destroy(fw4, &agent->memory_context);
		config->cfg.fw4state = NULL;
		return -1;
	}
	SET_OFFSET_OF(&config->cfg.fw6state, fw6state);

	return 0;
}

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
	fwmap_config_t fw4_config;
	fwstate_init_config(
		&fw4_config,
		sizeof(struct fw4_state_key),
		FWMAP_KEY_EQUAL_FW4,
		FWMAP_COPY_KEY_FW4,
		index_size,
		extra_bucket_count,
		worker_count
	);

	if (layermap_insert_new_layer_cp(
		    &config->cfg.fw4state, &fw4_config, &agent->memory_context
	    )) {
		return -1;
	}

	// Configure and insert new layer for IPv6
	fwmap_config_t fw6_config;
	fwstate_init_config(
		&fw6_config,
		sizeof(struct fw6_state_key),
		FWMAP_KEY_EQUAL_FW6,
		FWMAP_COPY_KEY_FW6,
		index_size,
		extra_bucket_count,
		worker_count
	);

	if (layermap_insert_new_layer_cp(
		    &config->cfg.fw6state, &fw6_config, &agent->memory_context
	    )) {
		// Rollback: remove the IPv4 layer we just added
		fwmap_t *fw4_active = ADDR_OF(&config->cfg.fw4state);
		fwmap_t *fw4_old = ADDR_OF(&fw4_active->next);
		SET_OFFSET_OF(&config->cfg.fw4state, fw4_old);
		fwmap_destroy(fw4_active, &agent->memory_context);
		return -1;
	}

	return 0;
}

void
fwstate_module_config_set_sync_config(
	struct cp_module *cp_module, struct fwstate_sync_config *sync_config
) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);
	config->cfg.sync_config = *sync_config;
}

struct fwmap_stats
fwstate_config_get_map_stats(const struct cp_module *cp_module, bool is_ipv6) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	fwmap_t *map;
	if (is_ipv6) {
		if (config->cfg.fw6state == NULL) {
			return (fwmap_stats_t){0};
		}
		map = ADDR_OF(&config->cfg.fw6state);
	} else {
		if (config->cfg.fw4state == NULL) {
			return (fwmap_stats_t){0};
		}
		map = ADDR_OF(&config->cfg.fw4state);
	}

	return fwmap_get_stats(map);
}

struct fwstate_sync_config
fwstate_config_get_sync_config(const struct cp_module *cp_module) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	return config->cfg.sync_config;
}

// Structure to hold outdated layers from both IPv4 and IPv6 maps
struct fwstate_outdated_layers {
	layermap_list_t *v4_layers;
	layermap_list_t *v6_layers;
};

fwstate_outdated_layers_t *
fwstate_config_trim_stale_layers(struct cp_module *cp_module, uint64_t now) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);
	struct agent *agent = ADDR_OF(&cp_module->agent);

	// Allocate structure to hold outdated layers
	fwstate_outdated_layers_t *outdated =
		memory_balloc(&agent->memory_context, sizeof(*outdated));
	if (!outdated) {
		errno = ENOMEM;
		return NULL;
	}
	outdated->v4_layers = NULL;
	outdated->v6_layers = NULL;

	// Trim IPv4 layers if map exists
	// Always return the structure even if trim fails, to avoid leaking
	// already collected layers
	if (config->cfg.fw4state) {
		layermap_trim_stale_layers_cp(
			&config->cfg.fw4state,
			&agent->memory_context,
			now,
			&outdated->v4_layers
		);
		// Ignore errors - we'll return what we collected
	}

	// Trim IPv6 layers if map exists
	if (config->cfg.fw6state) {
		layermap_trim_stale_layers_cp(
			&config->cfg.fw6state,
			&agent->memory_context,
			now,
			&outdated->v6_layers
		);
		// Ignore errors - we'll return what we collected
	}

	return outdated;
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
		fwmap_destroy(layer, &agent->memory_context);

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
		fwmap_destroy(layer, &agent->memory_context);

		// Free the list node
		memory_bfree(&agent->memory_context, v6_node, sizeof(*v6_node));

		v6_node = next;
	}

	// Free the outdated structure itself
	memory_bfree(&agent->memory_context, outdated, sizeof(*outdated));
}
