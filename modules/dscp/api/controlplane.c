#include <errno.h>

#include "common/lpm.h"
#include "config.h"
#include "controlplane.h"

#include "common/container_of.h"
#include "common/memory_address.h"
#include "common/strutils.h"

#include "controlplane/agent/agent.h"
#include "dataplane/config/zone.h"

struct cp_module *
dscp_module_config_create(struct agent *agent, const char *name) {
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

	if (cp_module_init(
		    &config->cp_module,
		    agent,
		    "dscp",
		    name,
		    dscp_module_config_free
	    )) {
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct dscp_module_config)
		);

		return NULL;
	}

	if (dscp_module_config_data_init(
		    config, &config->cp_module.memory_context
	    )) {
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct dscp_module_config)
		);
		return NULL;
	}

	return &config->cp_module;
}

void
dscp_module_config_free(struct cp_module *cp_module) {
	struct dscp_module_config *config =
		container_of(cp_module, struct dscp_module_config, cp_module);

	dscp_module_config_data_destroy(config);

	struct agent *agent = ADDR_OF(&cp_module->agent);
	memory_bfree(
		&agent->memory_context,
		config,
		sizeof(struct dscp_module_config)
	);
};

int
dscp_module_config_data_init(
	struct dscp_module_config *config, struct memory_context *memory_context
) {
	if (lpm_init(&config->lpm_v4, memory_context))
		return -1;
	if (lpm_init(&config->lpm_v6, memory_context)) {
		lpm_free(&config->lpm_v4);
		return -1;
	}

	// Initialize default DSCP config
	config->dscp.flag = DSCP_MARK_NEVER;
	config->dscp.mark = 0;

	return 0;
}

void
dscp_module_config_data_destroy(struct dscp_module_config *config) {
	lpm_free(&config->lpm_v4);
	lpm_free(&config->lpm_v6);
}

int
dscp_module_config_add_prefix_v4(
	struct cp_module *module, uint8_t *addr_start, uint8_t *addr_end
) {
	struct dscp_module_config *config =
		container_of(module, struct dscp_module_config, cp_module);

	return lpm_insert(&config->lpm_v4, 4, addr_start, addr_end, 1);
}

int
dscp_module_config_add_prefix_v6(
	struct cp_module *module, uint8_t *addr_start, uint8_t *addr_end
) {
	struct dscp_module_config *config =
		container_of(module, struct dscp_module_config, cp_module);

	return lpm_insert(&config->lpm_v6, 16, addr_start, addr_end, 1);
}

int
dscp_module_config_set_dscp_marking(
	struct cp_module *module, uint8_t flag, uint8_t mark
) {
	struct dscp_module_config *config =
		container_of(module, struct dscp_module_config, cp_module);

	config->dscp.flag = flag;
	config->dscp.mark = mark;

	return 0;
}
