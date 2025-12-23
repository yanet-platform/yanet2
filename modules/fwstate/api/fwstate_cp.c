#include <errno.h>
#include <string.h>

#include "fwstate_cp.h"

#include "common/container_of.h"
#include "controlplane/agent/agent.h"
#include "lib/fwstate/config.h"
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
fwstate_module_config_transfer(
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

	fwstate_config_destroy(&config->cfg, agent);

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

	// Apply defaults if parameters are not provided
	if (index_size == 0) {
		index_size = 1024 * 1024; // Default: 1M entries
	}
	if (extra_bucket_count == 0) {
		extra_bucket_count = 1024; // Default: 1024 extra buckets
	}
	if (worker_count == 0) {
		errno = EINVAL;
		return -1;
	}

	// Configure IPv4 firewall state map
	fwmap_config_t fw4_config = {
		.key_size = sizeof(struct fw4_state_key),
		.value_size = sizeof(struct fw_state_value),
		.hash_seed = 0,
		.worker_count = worker_count,
		.index_size = index_size,
		.extra_bucket_count = extra_bucket_count,
		.hash_fn_id = FWMAP_HASH_FNV1A,
		.key_equal_fn_id = FWMAP_KEY_EQUAL_FW4,
		.rand_fn_id = FWMAP_RAND_DEFAULT,
		.copy_key_fn_id = FWMAP_COPY_KEY_FW4,
		.copy_value_fn_id = FWMAP_COPY_VALUE_FWSTATE,
		.merge_value_fn_id = FWMAP_MERGE_VALUE_FWSTATE,
	};

	fwmap_t *fw4state = fwmap_new(&fw4_config, &agent->memory_context);
	if (fw4state == NULL) {
		return -1;
	}
	SET_OFFSET_OF(&config->cfg.fw4state, fw4state);

	// Configure IPv6 firewall state map
	fwmap_config_t fw6_config = {
		.key_size = sizeof(struct fw6_state_key),
		.value_size = sizeof(struct fw_state_value),
		.hash_seed = 0,
		.worker_count = worker_count,
		.index_size = index_size,
		.extra_bucket_count = extra_bucket_count,
		.hash_fn_id = FWMAP_HASH_FNV1A,
		.key_equal_fn_id = FWMAP_KEY_EQUAL_FW6,
		.rand_fn_id = FWMAP_RAND_DEFAULT,
		.copy_key_fn_id = FWMAP_COPY_KEY_FW6,
		.copy_value_fn_id = FWMAP_COPY_VALUE_FWSTATE,
		.merge_value_fn_id = FWMAP_MERGE_VALUE_FWSTATE,
	};

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
