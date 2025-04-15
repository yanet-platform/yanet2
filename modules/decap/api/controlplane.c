#include <errno.h>

#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"
#include "common/memory_address.h"
#include "common/strutils.h"

#include "controlplane/agent/agent.h"
#include "dataplane/config/zone.h"

struct cp_module *
decap_module_config_create(struct agent *agent, const char *name) {
	struct decap_module_config *config =
		(struct decap_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct decap_module_config)
		);
	if (config == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	if (cp_module_init(
		    &config->cp_module,
		    agent,
		    "decap",
		    name,
		    decap_module_config_free
	    )) {
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct decap_module_config)
		);

		return NULL;
	}

	if (decap_module_config_data_init(
		    config, &config->cp_module.memory_context
	    )) {

		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct decap_module_config)
		);
		return NULL;
	}

	return &config->cp_module;
}

void
decap_module_config_free(struct cp_module *cp_module) {
	struct decap_module_config *config =
		container_of(cp_module, struct decap_module_config, cp_module);

	decap_module_config_data_destroy(config);

	struct agent *agent = ADDR_OF(&cp_module->agent);
	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct decap_module_config)
	);
}

int
decap_module_config_data_init(
	struct decap_module_config *config,
	struct memory_context *memory_context
) {
	if (lpm_init(&config->prefixes4, memory_context))
		return -1;
	if (lpm_init(&config->prefixes6, memory_context))
		goto error_lpm_v6;

	return 0;

error_lpm_v6:
	lpm_free(&config->prefixes4);

	return -1;
}

void
decap_module_config_data_destroy(struct decap_module_config *config) {
	lpm_free(&config->prefixes4);
	lpm_free(&config->prefixes6);
}

int
decap_module_config_add_prefix_v4(
	struct cp_module *cp_module, const uint8_t *from, const uint8_t *to
) {
	struct decap_module_config *config =
		container_of(cp_module, struct decap_module_config, cp_module);
	return lpm_insert(&config->prefixes4, 4, from, to, 1);
}

int
decap_module_config_add_prefix_v6(
	struct cp_module *cp_module, const uint8_t *from, const uint8_t *to
) {
	struct decap_module_config *config =
		container_of(cp_module, struct decap_module_config, cp_module);
	return lpm_insert(&config->prefixes6, 16, from, to, 1);
}
