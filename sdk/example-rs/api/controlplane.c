#include "controlplane.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"

#include "common/memory.h"

static void
example_module_config_destroy(struct cp_module *config) {
	// Capture agent before fini zeroes it.
	struct agent *agent = ADDR_OF(&config->agent);

	cp_module_fini(config);
	memory_bfree(&agent->memory_context, config, sizeof(*config));
}

struct cp_module *
example_rs_module_config_new(
	struct agent *agent, const char *name, yanet_error **error
) {
	struct cp_module *config = (struct cp_module *)memory_balloc(
		&agent->memory_context, sizeof(*config)
	);
	if (config == NULL) {
		yanet_error_add(error, "failed to allocate config");
		return NULL;
	}

	if (cp_module_init(config, agent, "example_rs", name, error)) {
		yanet_error_add(error, "failed to init module");
		memory_bfree(&agent->memory_context, config, sizeof(*config));
		return NULL;
	}

	return config;
}

int
example_rs_module_config_free(struct cp_module *config, yanet_error **err) {
	if (cp_module_try_destroy(config, err)) {
		return -1;
	}

	example_module_config_destroy(config);
	return 0;
}
