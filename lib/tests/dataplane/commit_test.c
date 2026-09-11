// Generation commit pass: module and device commit handlers run
// exactly once per published generation as a preparation hook.
//
// Pins the once-per-generation semantics (a repeated pass on an
// already committed generation must not re-run handlers), the silent
// skip of handlers never registered and of out-of-range slot indices,
// that the harness deliverance path runs the handlers during its
// in-place delivery, and that the delivery wait completes once a real
// worker holds the delivered context.

#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>

#include "common/exp_array.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/test_assert.h"

#include "lib/controlplane/config/cp_device.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/registry.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/logging/log.h"
#include "lib/tests/dataplane/fixture.h"

// Registry capacity for the ownerless test registries; two entries per
// generation is the most any scenario registers.
#define COMMIT_TEST_REGISTRY_CAPACITY 4

// Invocation counters shared by the counting handlers.
static uint64_t commit_test_module_runs;
static uint64_t commit_test_device_runs;

static void
commit_test_module_count(
	struct dp_config *dp_config, struct cp_module *cp_module
) {
	(void)dp_config;
	(void)cp_module;
	commit_test_module_runs += 1;
}

static void
commit_test_device_count(
	struct dp_config *dp_config, struct cp_device *cp_device
) {
	(void)dp_config;
	(void)cp_device;
	commit_test_device_runs += 1;
}

// Builds an empty generation with ownerless module and device
// registries on the fixture's memory context.
static int
commit_test_gen_init(
	struct dp_config *cfg, struct cp_config_gen *gen, uint64_t gen_number
) {
	memset(gen, 0, sizeof(*gen));
	gen->gen = gen_number;

	if (registry_init(
		    &cfg->memory_context,
		    NULL,
		    &gen->module_registry.registry,
		    COMMIT_TEST_REGISTRY_CAPACITY
	    )) {
		return -1;
	}
	SET_OFFSET_OF(
		&gen->module_registry.memory_context, &cfg->memory_context
	);

	if (registry_init(
		    &cfg->memory_context,
		    NULL,
		    &gen->device_registry.registry,
		    COMMIT_TEST_REGISTRY_CAPACITY
	    )) {
		return -1;
	}
	SET_OFFSET_OF(
		&gen->device_registry.memory_context, &cfg->memory_context
	);

	return 0;
}

// Appends a dataplane module slot to the config and registers a
// module config in the generation pointing at it, mirroring what
// module loading and a generation upsert do together.
static int
commit_test_add_module(
	struct dp_config *cfg,
	struct cp_config_gen *gen,
	struct cp_module *cp_module,
	module_commit_handler commit_handler
) {
	struct dp_module *dp_modules = ADDR_OF(&cfg->dp_modules);
	if (mem_array_expand_exp(
		    &cfg->memory_context,
		    (void **)&dp_modules,
		    sizeof(*dp_modules),
		    &cfg->module_count
	    )) {
		return -1;
	}
	SET_OFFSET_OF(&cfg->dp_modules, dp_modules);

	memset(cp_module, 0, sizeof(*cp_module));
	registry_item_init(&cp_module->config_item);
	snprintf(cp_module->type, sizeof(cp_module->type), "%s", "test");
	snprintf(cp_module->name, sizeof(cp_module->name), "%s", "m0");
	cp_module->dp_module_idx = cfg->module_count - 1;

	struct dp_module *dp_module =
		ADDR_OF(&cfg->dp_modules) + cp_module->dp_module_idx;
	dp_module->commit_handler = commit_handler;

	return registry_insert(
		&gen->module_registry.registry, &cp_module->config_item
	);
}

// Appends a dataplane device slot to the config and registers a
// device config in the generation pointing at it.
static int
commit_test_add_device(
	struct dp_config *cfg,
	struct cp_config_gen *gen,
	struct cp_device *cp_device,
	device_commit_handler commit_handler
) {
	struct dp_device *dp_devices = ADDR_OF(&cfg->dp_devices);
	if (mem_array_expand_exp(
		    &cfg->memory_context,
		    (void **)&dp_devices,
		    sizeof(*dp_devices),
		    &cfg->device_count
	    )) {
		return -1;
	}
	SET_OFFSET_OF(&cfg->dp_devices, dp_devices);

	memset(cp_device, 0, sizeof(*cp_device));
	registry_item_init(&cp_device->config_item);
	snprintf(cp_device->type, sizeof(cp_device->type), "%s", "test");
	snprintf(cp_device->name, sizeof(cp_device->name), "%s", "d0");
	cp_device->dp_device_idx = cfg->device_count - 1;

	struct dp_device *dp_device =
		ADDR_OF(&cfg->dp_devices) + cp_device->dp_device_idx;
	dp_device->commit_handler = commit_handler;

	return registry_insert(
		&gen->device_registry.registry, &cp_device->config_item
	);
}

// Wires a single worker into the config, standing in for the
// per-instance worker array the assigner walks.
static int
commit_test_wire_worker(struct dp_config *cfg, struct dp_worker *worker) {
	memset(worker, 0, sizeof(*worker));

	struct dp_worker **workers = (struct dp_worker **)memory_balloc(
		&cfg->memory_context, sizeof(struct dp_worker *)
	);
	TEST_ASSERT_NOT_NULL(workers, "worker array alloc failed");
	SET_OFFSET_OF(workers, worker);
	SET_OFFSET_OF(&cfg->workers, workers);
	cfg->worker_count = 1;

	return TEST_SUCCESS;
}

// Verifies that the commit pass runs each module and device handler
// exactly once per generation: a second pass on the same generation
// does not re-invoke, a bumped generation does.
static int
test_commit_runs_once_per_gen(void) {
	struct dataplane_test_fixture fixture;
	TEST_ASSERT_SUCCESS(
		dataplane_test_fixture_init(&fixture, "commit_test", 1),
		"fixture init failed"
	);
	struct dp_config *cfg = &fixture.dp_config;

	struct cp_config_gen gen;
	struct cp_module cp_module;
	struct cp_device cp_device;
	TEST_ASSERT_SUCCESS(
		commit_test_gen_init(cfg, &gen, 7), "gen init failed"
	);
	TEST_ASSERT_SUCCESS(
		commit_test_add_module(
			cfg, &gen, &cp_module, commit_test_module_count
		),
		"add module failed"
	);
	TEST_ASSERT_SUCCESS(
		commit_test_add_device(
			cfg, &gen, &cp_device, commit_test_device_count
		),
		"add device failed"
	);

	commit_test_module_runs = 0;
	commit_test_device_runs = 0;

	dp_config_commit_gen(cfg, &gen);
	TEST_ASSERT_EQUAL(
		commit_test_module_runs,
		1,
		"module handler must run once on first commit"
	);
	TEST_ASSERT_EQUAL(
		commit_test_device_runs,
		1,
		"device handler must run once on first commit"
	);

	dp_config_commit_gen(cfg, &gen);
	TEST_ASSERT_EQUAL(
		commit_test_module_runs,
		1,
		"a committed generation must not re-run the module handler"
	);
	TEST_ASSERT_EQUAL(
		commit_test_device_runs,
		1,
		"a committed generation must not re-run the device handler"
	);

	gen.gen = 8;
	dp_config_commit_gen(cfg, &gen);
	TEST_ASSERT_EQUAL(
		commit_test_module_runs,
		2,
		"a bumped generation must re-run the module handler"
	);
	TEST_ASSERT_EQUAL(
		commit_test_device_runs,
		2,
		"a bumped generation must re-run the device handler"
	);

	dataplane_test_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that modules and devices which registered no commit handler
// are skipped without disturbing the pass.
static int
test_null_handlers_skipped(void) {
	struct dataplane_test_fixture fixture;
	TEST_ASSERT_SUCCESS(
		dataplane_test_fixture_init(&fixture, "commit_test", 1),
		"fixture init failed"
	);
	struct dp_config *cfg = &fixture.dp_config;

	struct cp_config_gen gen;
	struct cp_module cp_module;
	struct cp_device cp_device;
	TEST_ASSERT_SUCCESS(
		commit_test_gen_init(cfg, &gen, 1), "gen init failed"
	);
	TEST_ASSERT_SUCCESS(
		commit_test_add_module(cfg, &gen, &cp_module, NULL),
		"add module failed"
	);
	TEST_ASSERT_SUCCESS(
		commit_test_add_device(cfg, &gen, &cp_device, NULL),
		"add device failed"
	);

	dp_config_commit_gen(cfg, &gen);

	dataplane_test_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that an item whose slot index points past the loaded array
// is skipped defensively while the valid items' handlers still run,
// and that the skipped generation commit does not re-run them.
static int
test_out_of_range_indices_skip(void) {
	struct dataplane_test_fixture fixture;
	TEST_ASSERT_SUCCESS(
		dataplane_test_fixture_init(&fixture, "commit_test", 1),
		"fixture init failed"
	);
	struct dp_config *cfg = &fixture.dp_config;

	struct cp_config_gen gen;
	struct cp_module cp_module;
	struct cp_device cp_device;
	TEST_ASSERT_SUCCESS(
		commit_test_gen_init(cfg, &gen, 2), "gen init failed"
	);
	TEST_ASSERT_SUCCESS(
		commit_test_add_module(
			cfg, &gen, &cp_module, commit_test_module_count
		),
		"add module failed"
	);
	TEST_ASSERT_SUCCESS(
		commit_test_add_device(
			cfg, &gen, &cp_device, commit_test_device_count
		),
		"add device failed"
	);
	cp_module.dp_module_idx = cfg->module_count;
	cp_device.dp_device_idx = cfg->device_count;

	commit_test_module_runs = 0;
	commit_test_device_runs = 0;

	dp_config_commit_gen(cfg, &gen);
	TEST_ASSERT_EQUAL(
		commit_test_module_runs,
		0,
		"no handler may run for an out-of-range index"
	);
	TEST_ASSERT_EQUAL(
		commit_test_device_runs,
		0,
		"no handler may run for an out-of-range index"
	);

	// The skip is defensive only: a corrected successor generation
	// commits and runs the handlers.
	cp_module.dp_module_idx = 0;
	cp_device.dp_device_idx = 0;
	gen.gen = 3;
	dp_config_commit_gen(cfg, &gen);
	TEST_ASSERT_EQUAL(
		commit_test_module_runs,
		1,
		"the counting handler must run once for the successor"
	);
	TEST_ASSERT_EQUAL(
		commit_test_device_runs,
		1,
		"the counting device handler must run once for the successor"
	);

	dataplane_test_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that an absent generation commits nothing.
static int
test_null_gen_commits_nothing(void) {
	struct dataplane_test_fixture fixture;
	TEST_ASSERT_SUCCESS(
		dataplane_test_fixture_init(&fixture, "commit_test", 1),
		"fixture init failed"
	);
	struct dp_config *cfg = &fixture.dp_config;

	dp_config_commit_gen(cfg, NULL);

	dataplane_test_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that the harness deliverance path runs the commit handlers
// during its in-place delivery under the round lock, exactly once per
// generation.
static int
test_external_round_delivery_runs_handlers(void) {
	struct dataplane_test_fixture fixture;
	TEST_ASSERT_SUCCESS(
		dataplane_test_fixture_init(&fixture, "commit_test", 1),
		"fixture init failed"
	);
	struct dp_config *cfg = &fixture.dp_config;

	pthread_mutex_init(dp_config_external_round_lock(cfg), NULL);
	cfg->external_worker_rounds = true;

	struct cp_config_gen gen;
	struct cp_module cp_module;
	TEST_ASSERT_SUCCESS(
		commit_test_gen_init(cfg, &gen, 11), "gen init failed"
	);
	TEST_ASSERT_SUCCESS(
		commit_test_add_module(
			cfg, &gen, &cp_module, commit_test_module_count
		),
		"add module failed"
	);

	commit_test_module_runs = 0;

	dp_config_wait_for_gen(cfg, &gen);
	TEST_ASSERT_EQUAL(
		commit_test_module_runs,
		1,
		"the commit pass must run inside the harness delivery"
	);
	dp_config_wait_for_gen(cfg, &gen);
	TEST_ASSERT_EQUAL(
		commit_test_module_runs,
		1,
		"the committed generation must not re-run the handler"
	);

	pthread_mutex_destroy(dp_config_external_round_lock(cfg));
	dataplane_test_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

// Verifies that the delivery wait completes once a real worker holds
// the generation's context and has acknowledged it — the observation
// walks the worker array and the generation's own context array
// rather than an empty loop.
static int
test_wait_completes_on_real_worker(void) {
	struct dataplane_test_fixture fixture;
	TEST_ASSERT_SUCCESS(
		dataplane_test_fixture_init(&fixture, "commit_test", 1),
		"fixture init failed"
	);
	struct dp_config *cfg = &fixture.dp_config;

	struct cp_config_gen gen;
	struct cp_module cp_module;
	TEST_ASSERT_SUCCESS(
		commit_test_gen_init(cfg, &gen, 9), "gen init failed"
	);
	TEST_ASSERT_SUCCESS(
		commit_test_add_module(
			cfg, &gen, &cp_module, commit_test_module_count
		),
		"add module failed"
	);
	dp_config_commit_gen(cfg, &gen);

	// Leave the worker in the state the assigner and the worker
	// itself produce after a delivery: it holds the generation's
	// context (released store, as the assigner writes it) and has
	// acknowledged the generation.
	struct dp_worker worker;
	TEST_ASSERT_SUCCESS(
		commit_test_wire_worker(cfg, &worker), "worker wiring failed"
	);
	struct config_gen_ectx *config_gen_ectx =
		(struct config_gen_ectx *)memory_balloc(
			&cfg->memory_context, sizeof(struct config_gen_ectx)
		);
	TEST_ASSERT_NOT_NULL(config_gen_ectx, "ectx alloc failed");
	memset(config_gen_ectx, 0, sizeof(*config_gen_ectx));
	struct config_gen_ectx **config_gen_ectxs =
		(struct config_gen_ectx **)memory_balloc(
			&cfg->memory_context, sizeof(struct config_gen_ectx *)
		);
	TEST_ASSERT_NOT_NULL(config_gen_ectxs, "ectx array alloc failed");
	SET_OFFSET_OF(config_gen_ectxs, config_gen_ectx);
	SET_OFFSET_OF(&gen.config_gen_ectxs, config_gen_ectxs);
	gen.config_gen_ectx_count = 1;
	ATOMIC_SET_OFFSET_OF(&worker.config_gen_ectx, config_gen_ectx);
	worker.gen = gen.gen;

	// Watchdog: a broken observation must fail the suite, not hang it.
	alarm(10);
	dp_config_wait_for_gen(cfg, &gen);
	alarm(0);

	dataplane_test_fixture_fini(&fixture);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	LOG(INFO, "=== Starting Commit Test Suite ===");

	struct {
		const char *name;
		int (*fn)(void);
	} tests[] = {
		{"commit_runs_once_per_gen", test_commit_runs_once_per_gen},
		{"null_handlers_skipped", test_null_handlers_skipped},
		{"out_of_range_indices_skip", test_out_of_range_indices_skip},
		{"null_gen_commits_nothing", test_null_gen_commits_nothing},
		{"external_round_delivery_runs_handlers",
		 test_external_round_delivery_runs_handlers},
		{"wait_completes_on_real_worker",
		 test_wait_completes_on_real_worker},
	};

	size_t total = sizeof(tests) / sizeof(tests[0]);
	size_t failed = 0;

	for (size_t idx = 0; idx < total; ++idx) {
		LOG(INFO,
		    "[%zu/%zu] running %s...",
		    idx + 1,
		    total,
		    tests[idx].name);
		if (tests[idx].fn() != TEST_SUCCESS) {
			LOG(ERROR, "%s FAILED", tests[idx].name);
			++failed;
		} else {
			LOG(INFO, "%s passed", tests[idx].name);
		}
	}

	if (failed == 0) {
		LOG(INFO, "=== All %zu commit tests passed! ===", total);
	} else {
		LOG(ERROR, "=== %zu/%zu commit tests failed ===", failed, total
		);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
