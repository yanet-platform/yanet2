#include <errno.h>

#include "common/lpm.h"
#include "config.h"
#include "controlplane.h"

#include "common/container_of.h"
#include "common/memory_address.h"
#include "common/strutils.h"

#include "controlplane/agent/agent.h"
#include "dataplane/config/zone.h"

struct module_data *
dscp_module_config_init(struct agent *agent, const char *name) {
	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);

	uint64_t index;
	if (dp_config_lookup_module(dp_config, "dscp", &index)) {
		errno = ENXIO;
		return NULL;
	}

	// Allocate a new dscp module config
	struct dscp_module_config *config =
		(struct dscp_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct dscp_module_config)
		);
	if (config == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	// Initialize the module data
	config->module_data.index = index;
	strtcpy(config->module_data.name, name, sizeof(config->module_data.name)
	);

	memory_context_init_from(
		&config->module_data.memory_context,
		&agent->memory_context,
		name
	);
	SET_OFFSET_OF(&config->module_data.agent, agent);
	config->module_data.free_handler = dscp_module_config_free;

	// Initialize the LPMs
	struct memory_context *memory_context =
		&config->module_data.memory_context;
	if (lpm_init(&config->lpm_v4, memory_context))
		goto error_lpm_v4;
	if (lpm_init(&config->lpm_v6, memory_context))
		goto error_lpm_v6;

	// Initialize default DSCP config
	config->dscp.flag = DSCP_MARK_NEVER;
	config->dscp.mark = 0;

	return &config->module_data;

error_lpm_v6:
	lpm_free(&config->lpm_v4);

error_lpm_v4:
	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct dscp_module_config)
	);
	return NULL;
}

void
dscp_module_config_free(struct module_data *module_data) {
	struct dscp_module_config *config = container_of(
		module_data, struct dscp_module_config, module_data
	);

	lpm_free(&config->lpm_v4);
	lpm_free(&config->lpm_v6);

	struct agent *agent = ADDR_OF(&module_data->agent);
	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct dscp_module_config)
	);
};

int
dscp_module_config_add_prefix_v4(
	struct module_data *module, uint8_t *addr_start, uint8_t *addr_end
) {
	struct dscp_module_config *config =
		container_of(module, struct dscp_module_config, module_data);

	return lpm_insert(&config->lpm_v4, 4, addr_start, addr_end, 1);
}

int
dscp_module_config_add_prefix_v6(
	struct module_data *module, uint8_t *addr_start, uint8_t *addr_end
) {
	struct dscp_module_config *config =
		container_of(module, struct dscp_module_config, module_data);

	return lpm_insert(&config->lpm_v6, 16, addr_start, addr_end, 1);
}

int
dscp_module_config_set_dscp_marking(
	struct module_data *module, uint8_t flag, uint8_t mark
) {
	struct dscp_module_config *config =
		container_of(module, struct dscp_module_config, module_data);

	config->dscp.flag = flag;
	config->dscp.mark = mark;

	return 0;
}
