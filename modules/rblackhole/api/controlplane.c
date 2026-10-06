#include <errno.h>

#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"
#include "common/memory_address.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/counters/counters.h"
#include "lib/errors/errors.h"

static int
rblackhole_module_config_register_counters(
	struct rblackhole_module_config *config, yanet_error **err
) {
	// One packets+bytes pair, matching the dataplane's drop accounting.
	const char *name = "rblackhole_dropped";
	uint64_t id = counter_registry_register(
		&config->cp_module.counter_registry, name, 2, err
	);
	if (id == (uint64_t)-1) {
		yanet_error_add(err, "failed to register counter '%s'", name);
		return -1;
	}
	config->dropped_counter_id = id;

	return 0;
}

static void
rblackhole_module_config_destroy(struct cp_module *cp_module) {
	struct rblackhole_module_config *config = container_of(
		cp_module, struct rblackhole_module_config, cp_module
	);

	lpm_free(&config->prefixes4);
	lpm_free(&config->prefixes6);

	struct agent *agent = ADDR_OF(&cp_module->agent);

	cp_module_fini(cp_module);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct rblackhole_module_config)
	);
}

struct cp_module *
rblackhole_module_config_new(
	struct agent *agent, const char *name, yanet_error **err
) {
	struct rblackhole_module_config *config =
		(struct rblackhole_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct rblackhole_module_config)
		);
	if (config == NULL) {
		yanet_error_add(err, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(
		    &config->cp_module, agent, "rblackhole", name, err
	    )) {
		yanet_error_add(err, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct rblackhole_module_config)
		);

		return NULL;
	}

	struct memory_context *memory_context =
		&config->cp_module.memory_context;

	if (lpm_init(&config->prefixes4, memory_context, "prefixes4")) {
		yanet_error_add(err, "failed to init prefixes4");
		goto error;
	}
	if (lpm_init(&config->prefixes6, memory_context, "prefixes6")) {
		yanet_error_add(err, "failed to init prefixes6");
		goto error_prefixes6;
	}

	if (rblackhole_module_config_register_counters(config, err)) {
		yanet_error_add(err, "failed to register counters");
		goto error_counters;
	}

	return &config->cp_module;

error_counters:
	lpm_free(&config->prefixes6);
error_prefixes6:
	lpm_free(&config->prefixes4);
error:
	// Frees directly instead of going through the type destructor: no
	// reference beyond the caller's own has been taken, and no registry
	// has observed the module yet.
	cp_module_fini(&config->cp_module);
	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct rblackhole_module_config)
	);

	return NULL;
}

int
rblackhole_module_config_free(struct cp_module *cp_module, yanet_error **err) {
	if (cp_module_try_destroy(cp_module, err)) {
		return -1;
	}

	rblackhole_module_config_destroy(cp_module);
	return 0;
}

int
rblackhole_module_config_add_prefix_v4(
	struct cp_module *cp_module, const uint8_t *from, const uint8_t *to
) {
	struct rblackhole_module_config *config = container_of(
		cp_module, struct rblackhole_module_config, cp_module
	);
	return lpm_insert(&config->prefixes4, 4, from, to, 1);
}

int
rblackhole_module_config_add_prefix_v6(
	struct cp_module *cp_module, const uint8_t *from, const uint8_t *to
) {
	struct rblackhole_module_config *config = container_of(
		cp_module, struct rblackhole_module_config, cp_module
	);
	return lpm_insert(&config->prefixes6, 16, from, to, 1);
}

int
rblackhole_module_config_add_device(
	struct cp_module *cp_module,
	const char *name,
	uint64_t *index,
	yanet_error **err
) {
	struct rblackhole_module_config *config = container_of(
		cp_module, struct rblackhole_module_config, cp_module
	);

	uint64_t device_idx = 0;
	if (cp_module_link_device(cp_module, name, &device_idx, err)) {
		return -1;
	}
	if (index != NULL) {
		*index = device_idx;
	}
	config->pass_device_idx = device_idx;

	return 0;
}
