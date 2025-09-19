#include <errno.h>

#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"

#include "controlplane/agent/agent.h"

struct cp_module *
acl_module_config_init(struct agent *agent, const char *name) {
	struct acl_module_config *config =
		(struct acl_module_config *)memory_balloc(
			&agent->memory_context, sizeof(struct acl_module_config)
		);
	if (config == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	if (cp_module_init(
		    &config->cp_module,
		    agent,
		    "acl",
		    name,
		    acl_module_config_free
	    )) {
		goto fail;
	}

	struct memory_context *memory_context =
		&config->cp_module.memory_context;

	(void)memory_context;

	return &config->cp_module;

fail: {
	int prev_errno = errno;
	acl_module_config_free(&config->cp_module);
	errno = prev_errno;
	return NULL;
}
}

void
acl_module_config_free(struct cp_module *cp_module) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);

	struct agent *agent = ADDR_OF(&cp_module->agent);

	(void)config;
	(void)agent;
}

int
acl_module_compile(
	struct cp_module *cp_module,
	struct filter_rule *actions,
	uint32_t action_count
) {
	struct acl_module_config *config =
		container_of(cp_module, struct acl_module_config, cp_module);

	return filter_compiler_init(
		&config->filter,
		&cp_module->memory_context,
		actions,
		action_count
	);
}
