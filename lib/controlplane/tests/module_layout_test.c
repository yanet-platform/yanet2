// Module configuration layout: the loader mirrors the declared layout,
// and a configuration is created only for the loaded module's layout.
//
// Uses one module declaring a non-zero layout (constructor below) and
// forward, which declares none: a matching layout is accepted, any
// other is refused with the layout mismatch error, and plain
// cp_module_init keeps working for a module without a layout.

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "api/agent.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/test_assert.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#define LAYOUT_TEST_MEMORY_LIMIT (8u * 1024u * 1024u)
#define LAYOUT_TEST_LAYOUT 0x1234abcd5678ef90ull

static void
layout_test_handler(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;
	(void)module_ectx;
	(void)packet_front;
}

// Constructor of a module declaring a configuration layout, found by
// the harness loader through the exported symbol.
struct module *
new_module_layout_probe(void) {
	struct module *module = calloc(1, sizeof(*module));
	if (module == NULL) {
		return NULL;
	}
	snprintf(module->name, sizeof(module->name), "%s", "layout_probe");
	module->handler = layout_test_handler;
	module->config_layout = LAYOUT_TEST_LAYOUT;
	return module;
}

// Creates a configuration of the given type with the given expected
// layout and releases it again; returns the init result and leaves the
// error, if any, in err.
static int
layout_test_init(
	struct agent *agent,
	const char *type,
	uint64_t layout,
	yanet_error **err
) {
	struct cp_module *cp_module =
		memory_balloc(&agent->memory_context, sizeof(*cp_module));
	if (cp_module == NULL) {
		return -2;
	}
	int rc = cp_module_init_layout(
		cp_module, agent, type, "m0", layout, err
	);
	if (rc == 0) {
		cp_module_fini(cp_module);
	}
	memory_bfree(&agent->memory_context, cp_module, sizeof(*cp_module));
	return rc;
}

static int
run_loader_mirrors_layout_test(struct agent *agent) {
	struct dp_config *dp_config = agent_dp_config(agent);
	uint64_t idx;
	TEST_ASSERT_SUCCESS(
		dp_config_lookup_module(dp_config, "layout_probe", &idx),
		"module 'layout_probe' must be loaded"
	);
	struct dp_module *dp_module = ADDR_OF(&dp_config->dp_modules) + idx;
	TEST_ASSERT_EQUAL(
		dp_module->config_layout,
		LAYOUT_TEST_LAYOUT,
		"the loader must copy the declared layout"
	);
	return TEST_SUCCESS;
}

static int
run_matching_layout_accepted_test(struct agent *agent) {
	yanet_error *err = NULL;
	int rc = layout_test_init(
		agent, "layout_probe", LAYOUT_TEST_LAYOUT, &err
	);
	TEST_ASSERT_EQUAL(
		rc,
		0,
		"matching layout must be accepted: %s",
		err ? yanet_error_message(err) : "?"
	);
	yanet_error_free(err);
	return TEST_SUCCESS;
}

static int
run_other_layout_refused_test(struct agent *agent) {
	const uint64_t layouts[] = {0, LAYOUT_TEST_LAYOUT + 1};
	for (size_t i = 0; i < sizeof(layouts) / sizeof(layouts[0]); ++i) {
		yanet_error *err = NULL;
		int rc = layout_test_init(
			agent, "layout_probe", layouts[i], &err
		);
		TEST_ASSERT_EQUAL(
			rc,
			-1,
			"layout 0x%llx must be refused",
			(unsigned long long)layouts[i]
		);
		TEST_ASSERT_STR_CONTAINS(
			err ? yanet_error_message(err) : NULL,
			"config layout mismatch",
			"refusal must name the mismatch"
		);
		yanet_error_free(err);
	}
	return TEST_SUCCESS;
}

static int
run_module_without_layout_test(struct agent *agent) {
	yanet_error *err = NULL;
	struct cp_module *cp_module =
		memory_balloc(&agent->memory_context, sizeof(*cp_module));
	TEST_ASSERT_NOT_NULL(cp_module, "allocation failed");
	int rc = cp_module_init(cp_module, agent, "forward", "m0", &err);
	if (rc == 0) {
		cp_module_fini(cp_module);
	}
	memory_bfree(&agent->memory_context, cp_module, sizeof(*cp_module));
	TEST_ASSERT_EQUAL(
		rc,
		0,
		"plain init of a module without layout must succeed: %s",
		err ? yanet_error_message(err) : "?"
	);

	rc = layout_test_init(agent, "forward", LAYOUT_TEST_LAYOUT, &err);
	TEST_ASSERT_EQUAL(
		rc, -1, "a layout for a module without one must be refused"
	);
	TEST_ASSERT_STR_CONTAINS(
		err ? yanet_error_message(err) : NULL,
		"config layout mismatch",
		"refusal must name the mismatch"
	);
	yanet_error_free(err);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	const char *port_names[] = {"01:00.0"};
	const char *mods_to_load[] = {"forward", "layout_probe"};
	const char *devs_to_load[] = {"plain"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 26,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = mods_to_load,
		.module_count = 2,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	if (ut == NULL) {
		fprintf(stderr, "dataplane_ut_new failed\n");
		return 1;
	}

	yanet_error *err = NULL;
	struct agent *agent = agent_attach(
		dataplane_ut_shm(ut),
		0,
		"layout-test",
		LAYOUT_TEST_MEMORY_LIMIT,
		&err
	);
	if (agent == NULL) {
		fprintf(stderr,
			"agent_attach failed: %s\n",
			err ? yanet_error_message(err) : "?");
		yanet_error_free(err);
		dataplane_ut_free(ut);
		return 1;
	}

	struct {
		const char *name;
		int (*fn)(struct agent *);
	} tests[] = {
		{"loader_mirrors_layout", run_loader_mirrors_layout_test},
		{"matching_layout_accepted", run_matching_layout_accepted_test},
		{"other_layout_refused", run_other_layout_refused_test},
		{"module_without_layout", run_module_without_layout_test},
	};

	size_t total = sizeof(tests) / sizeof(tests[0]);
	size_t failed = 0;
	for (size_t idx = 0; idx < total; ++idx) {
		if (tests[idx].fn(agent) != TEST_SUCCESS) {
			LOG(ERROR, "%s FAILED", tests[idx].name);
			++failed;
		} else {
			LOG(INFO, "%s passed", tests[idx].name);
		}
	}

	agent_detach(agent);
	dataplane_ut_free(ut);

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
