#include <errno.h>

#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"
#include "common/memory_address.h"
#include "common/strutils.h"

#include "controlplane/agent/agent.h"
#include "dataplane/config/zone.h"

struct module_data *
decap_module_config_init(struct agent *agent, const char *name) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);

	uint64_t index;
	if (dp_config_lookup_module(dp_config, "decap", &index)) {
		errno = ENOENT;
		return NULL;
	}

	struct decap_module_config *config =
		(struct decap_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct decap_module_config)
		);
	if (config == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	config->module_data.index = index;
	strtcpy(config->module_data.name, name, sizeof(config->module_data.name)
	);
	memory_context_init_from(
		&config->module_data.memory_context,
		&agent->memory_context,
		name
	);
	SET_OFFSET_OF(&config->module_data.agent, agent);
	config->module_data.free_handler = decap_module_config_free;

	// From the point all allocations are made on local memory context
	struct memory_context *memory_context =
		&config->module_data.memory_context;
	if (lpm_init(&config->prefixes4, memory_context))
		goto error_lpm_v4;
	if (lpm_init(&config->prefixes6, memory_context))
		goto error_lpm_v6;

	return &config->module_data;

error_lpm_v6:
	lpm_free(&config->prefixes4);

error_lpm_v4:
	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct decap_module_config)
	);
	return NULL;
}

void
decap_module_config_free(struct module_data *module_data) {
	struct decap_module_config *config = container_of(
		module_data, struct decap_module_config, module_data
	);

	lpm_free(&config->prefixes4);
	lpm_free(&config->prefixes6);

	struct agent *agent = ADDR_OF(&module_data->agent);
	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct decap_module_config)
	);
};

int
decap_module_config_add_prefix_v4(
	struct module_data *module_data, const uint8_t *from, const uint8_t *to
) {
	struct decap_module_config *config = container_of(
		module_data, struct decap_module_config, module_data
	);
	return lpm_insert(&config->prefixes4, 4, from, to, 1);
}

int
decap_module_config_add_prefix_v6(
	struct module_data *module_data, const uint8_t *from, const uint8_t *to
) {
	struct decap_module_config *config = container_of(
		module_data, struct decap_module_config, module_data
	);
	return lpm_insert(&config->prefixes6, 16, from, to, 1);
}
