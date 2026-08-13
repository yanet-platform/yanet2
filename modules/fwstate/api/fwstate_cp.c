#include <string.h>

#include "fwstate_cp.h"

#include "common/container_of.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "lib/errors/errors.h"
#include "lib/fwstate/config.h"
#include "modules/fwstate/dataplane/config.h"
#include "objects/fwstate/api/fwstate_map_v4_object.h"
#include "objects/fwstate/api/fwstate_map_v6_object.h"

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

// Declare the module's link to one family's fwstate-map object and
// record the link index.
//
// Construction only records the declaration: the name is validated when
// the generation carrying this module is installed, and the dataplane
// reads the fwtable through the link at packet time. A stale raw table
// pointer here would outlive the object across generations, which is why
// the config stores an index instead.
static int
fwstate_module_link_table(
	struct cp_module *cp_module,
	const char *map_name,
	bool is_ipv6,
	uint64_t *link_idx,
	yanet_error **err
) {
	const char *object_type = is_ipv6 ? FWSTATE_MAP_V6_OBJECT_TYPE
					  : FWSTATE_MAP_V4_OBJECT_TYPE;

	return cp_module_link_object(
		cp_module, object_type, map_name, link_idx, err
	);
}

// One-shot construction: install the caller's sync config (or the
// defaults) and declare the named map-object links. A config handle is
// built exactly once and never updated afterwards.
static int
fwstate_module_setup(
	struct fwstate_module_config *config,
	const struct fwstate_sync_config *sync_config,
	const char *fw4_map_name,
	const char *fw6_map_name,
	yanet_error **err
) {
	struct cp_module *cp_module = &config->cp_module;

	if (sync_config != NULL) {
		config->sync_config = *sync_config;
	}

	if (fw4_map_name != NULL && fw4_map_name[0] != '\0') {
		if (fwstate_module_link_table(
			    cp_module,
			    fw4_map_name,
			    false,
			    &config->v4_object_link_idx,
			    err
		    )) {
			return -1;
		}
	}

	if (fw6_map_name != NULL && fw6_map_name[0] != '\0') {
		if (fwstate_module_link_table(
			    cp_module,
			    fw6_map_name,
			    true,
			    &config->v6_object_link_idx,
			    err
		    )) {
			return -1;
		}
	}

	return 0;
}

struct cp_module *
fwstate_module_config_new(
	struct agent *agent,
	const char *name,
	const struct fwstate_sync_config *sync_config,
	const char *fw4_map_name,
	const char *fw6_map_name,
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
	// balloc does not zero, and a reader before any link is declared
	// must see both slots marked absent.
	config->v4_object_link_idx = FWSTATE_OBJECT_LINK_NONE;
	config->v6_object_link_idx = FWSTATE_OBJECT_LINK_NONE;
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

	// A config handle is built exactly once: the sync config is
	// installed and the map-object links declared before the module is
	// ever visible to a registry. A failure tears the whole module down;
	// the config owns no table memory, so nothing beyond the module
	// itself is freed here.
	if (fwstate_module_setup(
		    config, sync_config, fw4_map_name, fw6_map_name, err
	    )) {
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

	// The linked fwstate-map objects own the tables and the config holds
	// only link indices, so there is no table memory to free here.
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

struct fwstate_sync_config
fwstate_config_get_sync_config(const struct cp_module *cp_module) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	return config->sync_config;
}
