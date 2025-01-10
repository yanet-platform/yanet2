#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"

#include "controlplane/agent/agent.h"

struct module_data *
forward_module_config_init(struct agent *agent, const char *name) {
	uint64_t index;
	if (dp_config_lookup_module(agent->dp_config, "forward", &index)) {
		return NULL;
	}

	struct forward_module_config *config =
		(struct forward_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct forward_module_config)
		);
	if (config == NULL)
		return NULL;

	config->module_data.index = index;
	strncpy(config->module_data.name,
		name,
		sizeof(config->module_data.name) - 1);
	memory_context_init_from(
		&config->module_data.memory_context,
		&agent->memory_context,
		name
	);

	// From the point all allocations are made on local memory context
	struct memory_context *memory_context =
		&config->module_data.memory_context;
	lpm_init(&config->lpm_v4, memory_context);
	lpm_init(&config->lpm_v6, memory_context);

	return &config->module_data;
}

int
forward_module_config_enable_v4(
	struct module_data *module_data, const uint8_t *from, const uint8_t *to
) {
	struct forward_module_config *config = container_of(
		module_data, struct forward_module_config, module_data
	);

	return lpm_insert(&config->lpm_v4, 4, from, to, 1);
}

int
forward_module_config_enable_v6(
	struct module_data *module_data, const uint8_t *from, const uint8_t *to
) {
	struct forward_module_config *config = container_of(
		module_data, struct forward_module_config, module_data
	);

	return lpm_insert(&config->lpm_v6, 16, from, to, 1);
}
