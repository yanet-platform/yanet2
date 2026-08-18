#include <dlfcn.h>
#include <stdio.h>
#include <string.h>

#include "lib/dataplane/config/plugin_abi.h"
#include "lib/dataplane/config/plugin_loader.h"
#include "lib/logging/log.h"

int
main(void) {
	log_enable_id(ERROR);

	struct plugin_registry registry;

	if (dp_load_plugins(ABI_TEST_GOOD_PLUGIN_DIR, &registry) != 0) {
		fprintf(stderr, "expected a matching-ABI plugin to load\n");
		return 1;
	}
	if (registry.count != 1) {
		fprintf(stderr, "expected exactly one loaded plugin\n");
		dp_unload_plugins(&registry);
		return 1;
	}
	if (strcmp(registry.plugins[0].name, "abihashtest") != 0) {
		fprintf(stderr,
			"unexpected plugin name %s\n",
			registry.plugins[0].name);
		dp_unload_plugins(&registry);
		return 1;
	}

	const size_t *plugin_count = (const size_t *)dlsym(
		registry.plugins[0].dl_handle, YANET_MODULE_ABI_COUNT_SYMBOL
	);
	if (plugin_count == NULL || *plugin_count <= 1 ||
	    *plugin_count >= yanet_dataplane_abi_count_v1) {
		fprintf(stderr,
			"expected the plugin's ABI table to be a proper, "
			"multi-row subset of the dataplane's own\n");
		dp_unload_plugins(&registry);
		return 1;
	}

	const struct yanet_abi_entity *plugin_entities =
		(const struct yanet_abi_entity *)dlsym(
			registry.plugins[0].dl_handle,
			YANET_MODULE_ABI_ENTITIES_SYMBOL
		);
	int saw_fn_row = 0;
	for (size_t i = 0; plugin_entities != NULL && i < *plugin_count; i++) {
		if (strncmp(plugin_entities[i].name, "fn ", 3) == 0) {
			saw_fn_row = 1;
			break;
		}
	}
	if (!saw_fn_row) {
		fprintf(stderr,
			"expected the plugin's ABI table to include at "
			"least one fn row\n");
		dp_unload_plugins(&registry);
		return 1;
	}

	dp_unload_plugins(&registry);

	if (dp_load_plugins(ABI_TEST_BAD_PLUGIN_DIR, &registry) == 0) {
		fprintf(stderr,
			"expected a mismatched-ABI plugin to be rejected\n");
		dp_unload_plugins(&registry);
		return 1;
	}
	if (registry.count != 0) {
		fprintf(stderr,
			"expected the registry to stay empty after "
			"rejection\n");
		return 1;
	}

	if (dp_load_plugins(ABI_TEST_MISSING_PLUGIN_DIR, &registry) == 0) {
		fprintf(stderr,
			"expected a plugin without an ABI table to be "
			"rejected\n");
		dp_unload_plugins(&registry);
		return 1;
	}
	if (registry.count != 0) {
		fprintf(stderr,
			"expected the registry to stay empty after "
			"rejection\n");
		return 1;
	}

	if (dp_load_plugins(ABI_TEST_UNKNOWN_PLUGIN_DIR, &registry) == 0) {
		fprintf(stderr,
			"expected a plugin referencing an entity the "
			"dataplane does not export to be rejected\n");
		dp_unload_plugins(&registry);
		return 1;
	}
	if (registry.count != 0) {
		fprintf(stderr,
			"expected the registry to stay empty after "
			"rejection\n");
		return 1;
	}

	if (dp_load_plugins(ABI_TEST_NO_COUNT_PLUGIN_DIR, &registry) == 0) {
		fprintf(stderr,
			"expected a plugin with entities but no count "
			"symbol to be rejected\n");
		dp_unload_plugins(&registry);
		return 1;
	}
	if (registry.count != 0) {
		fprintf(stderr,
			"expected the registry to stay empty after "
			"rejection\n");
		return 1;
	}

	if (dp_load_plugins(ABI_TEST_ZERO_COUNT_PLUGIN_DIR, &registry) == 0) {
		fprintf(stderr,
			"expected a plugin with an implausible entity "
			"count to be rejected\n");
		dp_unload_plugins(&registry);
		return 1;
	}
	if (registry.count != 0) {
		fprintf(stderr,
			"expected the registry to stay empty after "
			"rejection\n");
		return 1;
	}

	printf("plugin_loader_test: OK\n");
	return 0;
}
