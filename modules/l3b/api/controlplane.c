#include "controlplane.h"

#include "config.h"

#include <string.h>

#include "common/container_of.h"
#include "common/memory.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"

struct cp_module *
l3b_module_config_new(
	struct agent *agent, const char *name, yanet_error **error
) {
	struct l3b_module_config *config =
		(struct l3b_module_config *)memory_balloc(
			&agent->memory_context, sizeof(struct l3b_module_config)
		);
	if (config == NULL) {
		yanet_error_add(error, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(&config->cp_module, agent, "l3b", name, error)) {
		yanet_error_add(error, "failed to init module");
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct l3b_module_config)
		);
		return NULL;
	}

	config->virtual_service_count = 0;
	SET_OFFSET_OF(&config->virtual_services, NULL);

	memset(&config->filter_ip6, 0, sizeof(config->filter_ip6));
	memset(&config->filter_ip4, 0, sizeof(config->filter_ip4));

	return &config->cp_module;
}

void
l3b_module_config_free(struct cp_module *cp_module) {
	struct l3b_module_config *config =
		container_of(cp_module, struct l3b_module_config, cp_module);

	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&config->cp_module.agent);

	cp_module_fini(&config->cp_module);
	memory_bfree(
		&agent->memory_context, config, sizeof(struct l3b_module_config)
	);
}
