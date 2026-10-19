// Loading of object types a plugin ships.
//
// The loaders resolve a constructor through the main-program handle, which
// also sees the symbols of a plugin loaded with global symbols. Only the
// plugin constructs the "loader_probe" object type, so the scenario tells
// from the loaded descriptor that the plugin's constructor ran: the harness
// loading the type it is asked for.

#include <dlfcn.h>
#include <stdint.h>
#include <string.h>

#include "api/agent.h"
#include "common/memory_address.h"
#include "common/test_assert.h"
#include "lib/dataplane/config/object_loader.h"
#include "lib/dataplane/config/plugin_loader.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/object/object.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/logging/log.h"

#include "loader_probe.h"

// Asserts that the loader_probe object type is registered with the plugin's
// hook and prepared size; the registry holds the probe plugin alone.
static int
assert_from_plugin(
	struct dp_config *dp_config, const struct plugin_registry *plugins
) {
	TEST_ASSERT_EQUAL(plugins->count, 1, "the probe plugin must be loaded");
	void *hook =
		dlsym(plugins->plugins[0].dl_handle,
		      "loader_probe_plugin_hook");
	TEST_ASSERT_NOT_NULL(hook, "the plugin must export its hook");

	uint64_t index;
	TEST_ASSERT_EQUAL(
		dp_config_lookup_object(dp_config, LOADER_PROBE_NAME, &index),
		0,
		"the object type must be registered"
	);
	struct dp_object *object = ADDR_OF(&dp_config->dp_objects) + index;
	TEST_ASSERT_EQUAL(
		object->prepared_size,
		LOADER_PROBE_PLUGIN_PREPARED_SIZE,
		"the object type must keep the plugin's prepared size"
	);
	TEST_ASSERT(
		(void *)object->commit_ectx_handler == hook,
		"the object type must keep the plugin's hook"
	);
	return TEST_SUCCESS;
}

// The harness loads a plugin's object type through the normal lookup, so
// the type keeps its own hook and prepared size.
static int
run_harness_loads_plugin_object_test(void) {
	const char *objects[] = {LOADER_PROBE_NAME};
	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 24,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.plugin_dir = LOADER_PLUGIN_DIR,
		.objects_to_load = objects,
		.objects_to_load_count = 1,
	};
	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	TEST_ASSERT_NOT_NULL(ut, "the harness must start with the plugin");

	struct plugin_registry plugins = {0};
	int rc = dp_load_plugins(LOADER_PLUGIN_DIR, &plugins);
	struct dp_config *dp_config =
		yanet_shm_dp_config(dataplane_ut_shm(ut), 0);
	int result =
		rc == 0 ? assert_from_plugin(dp_config, &plugins) : TEST_FAILED;
	dp_unload_plugins(&plugins);
	dataplane_ut_free(ut);
	return result;
}

int
main(void) {
	log_enable_name("info");

	struct {
		const char *name;
		int (*fn)(void);
	} tests[] = {
		{"harness_loads_plugin_object",
		 run_harness_loads_plugin_object_test},
	};

	size_t failed = 0;
	for (size_t idx = 0; idx < sizeof(tests) / sizeof(tests[0]); ++idx) {
		if (tests[idx].fn() != TEST_SUCCESS) {
			LOG(ERROR, "test %s failed", tests[idx].name);
			++failed;
		} else {
			LOG(INFO, "test %s passed", tests[idx].name);
		}
	}
	return failed == 0 ? 0 : 1;
}
