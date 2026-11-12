#include <errno.h>
#include <string.h>

#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"
#include "common/fast_lpm_build.h"
#include "common/memory_address.h"
#include "common/strutils.h"
#include "lib/errors/errors.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/dataplane/config/zone.h"

static void
decap_module_config_destroy(struct cp_module *cp_module) {
	struct decap_module_config *config =
		container_of(cp_module, struct decap_module_config, cp_module);

	decap_module_config_data_fini(config);

	struct agent *agent = ADDR_OF(&cp_module->entity.agent);

	cp_module_fini(cp_module);

	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct decap_module_config)
	);
}

struct cp_module *
decap_module_config_new(
	struct agent *agent, const char *name, yanet_error **err
) {
	struct decap_module_config *config =
		(struct decap_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct decap_module_config)
		);
	if (config == NULL) {
		yanet_error_add(err, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "decap", name, err)) {
		yanet_error_add(err, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct decap_module_config)
		);

		return NULL;
	}

	if (decap_module_config_data_init(
		    config, &config->cp_module.entity.memory_context
	    )) {
		yanet_error_add(err, "failed to init config data");
		// Frees directly instead of going through the type destructor.
		//
		// A failed configuration-data setup never reaches a state its
		// own teardown could safely walk. No reference beyond the
		// caller's own has been taken, and no registry has observed
		// the module yet, so nothing is lost by freeing the block
		// here.
		cp_module_fini(&config->cp_module);
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct decap_module_config)
		);
		return NULL;
	}

	return &config->cp_module;
}

int
decap_module_config_free(struct cp_module *cp_module, yanet_error **err) {
	if (cp_module_try_destroy(cp_module, err)) {
		return -1;
	}

	struct agent *owner = ADDR_OF(&cp_module->entity.agent);

	decap_module_config_destroy(cp_module);
	agent_destroy_released(owner);
	return 0;
}

int
decap_module_config_data_init(
	struct decap_module_config *config,
	struct memory_context *memory_context
) {
	// A failed init leaves the other prefix set safe to free.
	memset(&config->prefixes4, 0, sizeof(config->prefixes4));
	memset(&config->prefixes6, 0, sizeof(config->prefixes6));
	if (fast_lpm_init(&config->prefixes4, memory_context, "prefixes4", 4) ||
	    fast_lpm_init(
		    &config->prefixes6, memory_context, "prefixes6", 16
	    )) {
		decap_module_config_data_fini(config);
		return -1;
	}

	return 0;
}

void
decap_module_config_data_fini(struct decap_module_config *config) {
	fast_lpm_free(&config->prefixes4);
	fast_lpm_free(&config->prefixes6);
}

int
decap_module_config_add_prefix_v4(
	struct cp_module *cp_module, const uint8_t *from, const uint8_t *to
) {
	struct decap_module_config *config =
		container_of(cp_module, struct decap_module_config, cp_module);
	return fast_lpm_insert(&config->prefixes4, from, to, 1);
}

int
decap_module_config_add_prefix_v6(
	struct cp_module *cp_module, const uint8_t *from, const uint8_t *to
) {
	struct decap_module_config *config =
		container_of(cp_module, struct decap_module_config, cp_module);
	return fast_lpm_insert(&config->prefixes6, from, to, 1);
}

int
decap_module_config_build(struct cp_module *cp_module) {
	struct decap_module_config *config =
		container_of(cp_module, struct decap_module_config, cp_module);
	if (fast_lpm_build(&config->prefixes4) ||
	    fast_lpm_build(&config->prefixes6)) {
		return -1;
	}
	return 0;
}
