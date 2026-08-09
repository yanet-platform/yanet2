#include <errno.h>
#include <string.h>

#include "fwstate_cp.h"

#include "common/container_of.h"
#include "controlplane/agent/agent.h"
#include "lib/errors/errors.h"
#include "lib/fwstate/config.h"
#include "modules/fwstate/dataplane/config.h"
#include "modules/fwstate/objects/fwstate_map_object.h"

// Set default timeout values for fwstate configuration.
static void
fwstate_config_set_defaults(struct fwstate_sync_config *config) {
	memset(config, 0, sizeof(struct fwstate_sync_config));
	config->timeouts.tcp_syn_ack = FW_STATE_DEFAULT_TIMEOUT;
	config->timeouts.tcp_syn = FW_STATE_DEFAULT_TIMEOUT;
	config->timeouts.tcp_fin = FW_STATE_DEFAULT_TIMEOUT;
	config->timeouts.tcp = FW_STATE_DEFAULT_TIMEOUT;
	config->timeouts.udp = 30e9;
	config->timeouts.default_ = 16e9;
}

struct cp_module *
fwstate_module_config_new(
	struct agent *agent, const char *name, yanet_error **err
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
		    &config->cp_module, agent, FWSTATE_MODULE_NAME, name, err
	    )) {
		yanet_error_add(err, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct fwstate_module_config)
		);
		return NULL;
	}
	fwstate_config_set_defaults(&config->sync_config);
	config->v4_object_link_idx = FWSTATE_OBJECT_LINK_NONE;
	config->v6_object_link_idx = FWSTATE_OBJECT_LINK_NONE;

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
			fwstate_module_config_free(&config->cp_module);
			return NULL;
		}
		*counters[i].dst = id;
	}

	return &config->cp_module;
}

void
fwstate_module_config_free(struct cp_module *cp_module) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&cp_module->agent);

	cp_module_fini(cp_module);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct fwstate_module_config)
	);
}

int
fwstate_module_config_set(
	struct cp_module *cp_module,
	const char *fw4_name,
	const char *fw6_name,
	const struct fwstate_sync_config *sync_config,
	yanet_error **err
) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	config->sync_config = *sync_config;

	config->v4_object_link_idx = FWSTATE_OBJECT_LINK_NONE;
	config->v6_object_link_idx = FWSTATE_OBJECT_LINK_NONE;

	if (fw4_name != NULL && fw4_name[0] != '\0') {
		if (cp_module_link_object(
			    cp_module,
			    FWSTATE_MAP_V4_OBJECT_TYPE,
			    fw4_name,
			    &config->v4_object_link_idx,
			    err
		    )) {
			return -1;
		}
	}

	if (fw6_name != NULL && fw6_name[0] != '\0') {
		if (cp_module_link_object(
			    cp_module,
			    FWSTATE_MAP_V6_OBJECT_TYPE,
			    fw6_name,
			    &config->v6_object_link_idx,
			    err
		    )) {
			return -1;
		}
	}

	return 0;
}

struct fwstate_sync_config
fwstate_config_get_sync_config(const struct cp_module *cp_module) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	return config->sync_config;
}
