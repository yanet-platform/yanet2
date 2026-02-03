#include <errno.h>

#include "controlplane.h"

#include "config.h"

#include "common/container_of.h"

#include "controlplane/agent/agent.h"

struct cp_module *
proxy_module_config_init(struct agent *agent, const char *name) {
	struct proxy_module_config *config =
		(struct proxy_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct proxy_module_config)
		);
	if (config == NULL) {
		errno = ENOMEM;
		return NULL;
	}

	if (cp_module_init(
		    &config->cp_module,
		    agent,
		    "proxy",
		    name
	    )) {
		goto fail;
	}

	config->proxy_config.size_connections_table = 0;
	config->proxy_config.upstream_addr = 0;
	config->proxy_config.upstream_port = 0;
	config->proxy_config.proxy_addr = 0;
	config->proxy_config.proxy_port = 0;
	config->proxy_config.upstream_net.addr = 0;
	config->proxy_config.upstream_net.mask = 0;

	return &config->cp_module;

fail: {
	int prev_errno = errno;
	proxy_module_config_free(&config->cp_module);
	errno = prev_errno;
	return NULL;
}
}

void
proxy_module_config_free(struct cp_module *cp_module) {
	struct proxy_module_config *config = container_of(
		cp_module, struct proxy_module_config, cp_module
	);

	struct agent *agent = ADDR_OF(&cp_module->agent);
	// FIXME: remove the check as agent should be assigned
	if (agent != NULL) {
		memory_bfree(
			&agent->memory_context,
			config,
			sizeof(struct proxy_module_config)
		);
	}
}

int
proxy_module_config_delete(struct cp_module *cp_module) {
	return agent_delete_module(
		cp_module->agent, "proxy", cp_module->name
	);
}

int proxy_module_config_set_conn_table_size(struct cp_module *cp_module, uint32_t size) {
	printf("proxy_module_config_set_conn_table_size\n");
	struct proxy_module_config *config =
		container_of(cp_module, struct proxy_module_config, cp_module);

	config->proxy_config.size_connections_table = size;

	return 0;
}