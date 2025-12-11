#include "controlplane.h"
#include "common/container_of.h"
#include "config.h"
#include "controlplane/config/cp_module.h"
#include <stdlib.h>

////////////////////////////////////////////////////////////////////////////////

void
my_module_config_free(struct my_module_config *config) {
	free(config);
}

void
my_module_free(struct cp_module *cp_module) {
	struct my_module_config *config =
		container_of(cp_module, struct my_module_config, cp_module);
	my_module_config_free(config);
}

struct my_module_config *
my_module_config_create(struct agent *agent, const char *name) {
	struct my_module_config *config =
		malloc(sizeof(struct my_module_config));
	cp_module_init(
		&config->cp_module, agent, "balancer", name, my_module_free
	);
	return config;
}