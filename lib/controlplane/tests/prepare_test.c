// Per-worker execution-context commit handler: the pass that derives a
// context's
// absolute addresses also fills each module's private buffer through
// its registered hook.
//
// Pins the invocation contract through the real build and delivery
// paths: a hook runs once per module execution context per assignment
// pass, sees its own context, module config and allocated buffer, and
// repeats on a later pass without error; a module declaring no buffer
// size gets none allocated; a module declaring a size without a hook
// gets its buffer allocated and absolutized but no call.

#include <errno.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "api/agent.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/test_assert.h"

#include "devices/plain/api/controlplane.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"
#include "modules/decap/api/controlplane.h"
#include "modules/forward/api/controlplane.h"

#define PREPARE_TEST_MEMORY_LIMIT (8u * 1024u * 1024u)
#define PREPARE_TEST_BUFFER_SIZE 64

// Recorded invocations of the counting hook; the registry is large
// enough for every call the scenarios can produce.
#define PREPARE_TEST_MAX_CALLS 16
struct prepare_test_call {
	struct module_ectx *module_ectx;
	struct cp_module *cp_module;
	void *buffer;
};
static struct prepare_test_call prepare_test_calls[PREPARE_TEST_MAX_CALLS];
static uint64_t prepare_test_call_count;

// Counts hook invocations, capturing the context, module config and
// derived buffer each call observed.
static void
prepare_test_count(
	struct module_ectx *module_ectx, struct cp_module *cp_module
) {
	if (prepare_test_call_count < PREPARE_TEST_MAX_CALLS) {
		prepare_test_calls[prepare_test_call_count] =
			(struct prepare_test_call){
				.module_ectx = module_ectx,
				.cp_module = cp_module,
				.buffer = module_ectx->abs_module_prepared,
			};
	}
	++prepare_test_call_count;
}

// Overrides a loaded module's hook and buffer size, standing in for
// what a module constructor declares at load time.
static int
prepare_test_patch_module(
	struct dp_config *dp_config,
	const char *name,
	module_commit_ectx_handler handler,
	uint64_t prepared_size
) {
	uint64_t idx;
	TEST_ASSERT_SUCCESS(
		dp_config_lookup_module(dp_config, name, &idx),
		"module '%s' must be loaded",
		name
	);
	struct dp_module *dp_modules = ADDR_OF(&dp_config->dp_modules);
	dp_modules[idx].commit_ectx_handler = handler;
	dp_modules[idx].prepared_size = prepared_size;

	return TEST_SUCCESS;
}

// Walks the context tree along the control-plane-owned relative
// arrays and returns the module context registered under the given
// type and name, or NULL when no stage carries it.
static struct module_ectx *
prepare_test_find_module_ectx(
	struct config_gen_ectx *ectx, const char *type, const char *name
) {
	struct device_ectx **device_ptrs = ADDR_OF(&ectx->device_ptrs);
	for (uint64_t dev_idx = 0; dev_idx < ectx->device_count; ++dev_idx) {
		struct device_ectx *device_ectx =
			ADDR_OF(device_ptrs + dev_idx);
		if (device_ectx == NULL) {
			continue;
		}
		for (uint64_t dir = 0; dir < 2; ++dir) {
			struct device_entry_ectx *entry =
				ADDR_OF(dir ? &device_ectx->output_pipelines
					    : &device_ectx->input_pipelines);
			if (entry == NULL) {
				continue;
			}
			struct pipeline_ectx **pipeline_ptrs =
				ADDR_OF(&entry->pipeline_ptrs);
			for (uint64_t pipe_idx = 0;
			     pipe_idx < entry->pipeline_count;
			     ++pipe_idx) {
				struct pipeline_ectx *pipeline_ectx =
					ADDR_OF(pipeline_ptrs + pipe_idx);
				if (pipeline_ectx == NULL) {
					continue;
				}
				struct function_ectx **function_ptrs =
					ADDR_OF(&pipeline_ectx->function_ptrs);
				for (uint64_t func_idx = 0;
				     func_idx < pipeline_ectx->length;
				     ++func_idx) {
					struct function_ectx *function_ectx =
						ADDR_OF(function_ptrs + func_idx
						);
					if (function_ectx == NULL) {
						continue;
					}
					struct chain_ectx **chain_ptrs =
						ADDR_OF(&function_ectx
								 ->chain_ptrs);
					for (uint64_t chain_idx = 0;
					     chain_idx <
					     function_ectx->chain_count;
					     ++chain_idx) {
						struct chain_ectx *chain_ectx =
							ADDR_OF(chain_ptrs +
								chain_idx);
						if (chain_ectx == NULL) {
							continue;
						}
						struct module_ectx *
							*module_ptrs = ADDR_OF(
								&chain_ectx
									 ->module_ptrs
							);
						for (uint64_t module_idx = 0;
						     module_idx <
						     chain_ectx->length;
						     ++module_idx) {
							struct module_ectx *
								module_ectx = ADDR_OF(
									module_ptrs +
									module_idx
								);
							if (module_ectx ==
							    NULL) {
								continue;
							}
							struct cp_module *
								cp_module = ADDR_OF(
									&module_ectx
										 ->cp_module
								);
							if (strncmp(cp_module
									    ->type,
								    type,
								    sizeof(cp_module
										   ->type
								    )) == 0 &&
							    strncmp(cp_module
									    ->name,
								    name,
								    sizeof(cp_module
										   ->name
								    )) == 0) {
								return module_ectx;
							}
						}
					}
				}
			}
		}
	}
	return NULL;
}

// Installs a plain device whose input entry runs the named pipeline,
// driving the same generation swap as production device updates.
static int
prepare_test_install_device(
	struct agent *agent,
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	const char *device_name,
	const char *pipeline_name,
	yanet_error **err
) {
	struct cp_device_plain_config *dev_cfg =
		cp_device_plain_config_new(device_name, 1, 0, err);
	if (dev_cfg == NULL) {
		return -1;
	}
	cp_device_plain_config_set_input_pipeline(dev_cfg, 0, pipeline_name, 1);
	struct cp_device *dev = cp_device_plain_new(agent, dev_cfg, err);
	cp_device_plain_config_free(dev_cfg);
	if (dev == NULL) {
		return -1;
	}
	struct cp_device *devs[] = {dev};
	int rc = cp_config_update_devices(dp_config, cp_config, 1, devs, err);
	yanet_error *free_err = NULL;
	int free_rc = cp_device_plain_free(dev, &free_err);
	yanet_error_free(free_err);
	if (rc == 0) {
		if (!(free_rc == -1 && errno == EAGAIN)) {
			return -1;
		}
	} else if (free_rc != 0) {
		return -1;
	}
	return rc;
}

// Shared fixture state: one harness, one agent, one generation built
// by the setup below.
struct prepare_test_env {
	struct yanet_shm *shm;
	struct agent *agent;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
	struct cp_config_gen *gen;
};

// Builds the chain [forward:hook-a, forward:hook-b, decap:no-buffer]
// and installs it behind device dev0. The forward dataplane slot is
// patched to the counting hook with a declared buffer size before the
// install, so the ectx build and the delivery pass run the real
// allocation and hook paths; decap keeps its zero declared size.
static int
prepare_test_setup(struct prepare_test_env *env, struct dataplane_ut *ut) {
	memset(env, 0, sizeof(*env));
	env->shm = dataplane_ut_shm(ut);
	TEST_ASSERT_NOT_NULL(env->shm, "harness shared memory must exist");

	yanet_error *err = NULL;
	env->agent = agent_attach(
		env->shm, 0, "prepare-test", PREPARE_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(
		env->agent,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	env->dp_config = agent_dp_config(env->agent);
	env->cp_config = ADDR_OF(&env->agent->cp_config);

	TEST_ASSERT_SUCCESS(
		prepare_test_patch_module(
			env->dp_config,
			"forward",
			prepare_test_count,
			PREPARE_TEST_BUFFER_SIZE
		),
		"patching the forward slot failed"
	);

	struct cp_module *hook_a =
		forward_module_config_init(env->agent, "hook-a", &err);
	TEST_ASSERT_NOT_NULL(
		hook_a,
		"forward module 'hook-a' failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_module *hook_b =
		forward_module_config_init(env->agent, "hook-b", &err);
	TEST_ASSERT_NOT_NULL(
		hook_b,
		"forward module 'hook-b' failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_module *no_buffer =
		decap_module_config_new(env->agent, "no-buffer", &err);
	TEST_ASSERT_NOT_NULL(
		no_buffer,
		"decap module 'no-buffer' failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_module *modules[] = {hook_a, hook_b, no_buffer};
	TEST_ASSERT_SUCCESS(
		cp_config_update_modules(
			env->dp_config, env->cp_config, 3, modules, &err
		),
		"update_modules failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	const char *const chain_types[] = {"forward", "forward", "decap"};
	const char *const chain_names[] = {"hook-a", "hook-b", "no-buffer"};
	struct cp_chain_config *chain_cfg =
		cp_chain_config_create("chain0", 3, chain_types, chain_names);
	TEST_ASSERT_NOT_NULL(chain_cfg, "chain_config_create failed");
	struct cp_function_config *func_cfg =
		cp_function_config_create("func0", 1);
	TEST_ASSERT_NOT_NULL(func_cfg, "function_config_create failed");
	TEST_ASSERT_SUCCESS(
		cp_function_config_set_chain(func_cfg, 0, chain_cfg, 1),
		"set_chain failed"
	);
	struct cp_function_config *func_cfgs[] = {func_cfg};
	TEST_ASSERT_SUCCESS(
		cp_config_update_functions(
			env->dp_config, env->cp_config, 1, func_cfgs, &err
		),
		"update_functions failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	cp_function_config_free(func_cfg);

	struct cp_pipeline_config *pipe_cfg =
		cp_pipeline_config_create("pipe0", 1);
	TEST_ASSERT_NOT_NULL(pipe_cfg, "pipeline_config_create failed");
	TEST_ASSERT_SUCCESS(
		cp_pipeline_config_set_function(pipe_cfg, 0, "func0"),
		"set_function failed"
	);
	struct cp_pipeline_config *pipe_cfgs[] = {pipe_cfg};
	TEST_ASSERT_SUCCESS(
		cp_config_update_pipelines(
			env->dp_config, env->cp_config, 1, pipe_cfgs, &err
		),
		"update_pipelines failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	cp_pipeline_config_free(pipe_cfg);

	TEST_ASSERT_SUCCESS(
		prepare_test_install_device(
			env->agent,
			env->dp_config,
			env->cp_config,
			"dev0",
			"pipe0",
			&err
		),
		"update_devices failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	env->gen = ADDR_OF(&env->cp_config->cp_config_gen);

	return TEST_SUCCESS;
}

// Verifies that the hook runs once per module execution context on
// the delivery pass, each call seeing its own context, its module
// config and the buffer allocated for that context, and that sibling
// module instances receive distinct buffers.
static int
run_prepare_hook_invoked_per_ectx_test(struct prepare_test_env *env) {
	struct config_gen_ectx *ectx = cp_config_gen_worker_ectx(env->gen, 0);
	TEST_ASSERT_NOT_NULL(ectx, "worker 0 execution context must exist");

	struct module_ectx *hook_a_ectx =
		prepare_test_find_module_ectx(ectx, "forward", "hook-a");
	struct module_ectx *hook_b_ectx =
		prepare_test_find_module_ectx(ectx, "forward", "hook-b");
	TEST_ASSERT_NOT_NULL(
		hook_a_ectx, "module context of 'hook-a' must exist"
	);
	TEST_ASSERT_NOT_NULL(
		hook_b_ectx, "module context of 'hook-b' must exist"
	);

	TEST_ASSERT_EQUAL(
		prepare_test_call_count,
		2,
		"the hook must run once per module instance in the delivery "
		"pass"
	);
	TEST_ASSERT(
		prepare_test_calls[0].module_ectx !=
			prepare_test_calls[1].module_ectx,
		"each module instance's call must carry its own context"
	);

	for (uint64_t idx = 0; idx < prepare_test_call_count; ++idx) {
		struct prepare_test_call *call = prepare_test_calls + idx;
		struct cp_module *cp_module =
			ADDR_OF(&call->module_ectx->cp_module);
		TEST_ASSERT(
			call->cp_module == cp_module,
			"the hook must receive the context's own module config"
		);
		TEST_ASSERT(
			call->module_ectx == hook_a_ectx ||
				call->module_ectx == hook_b_ectx,
			"every call must belong to a module of the patched slot"
		);
		TEST_ASSERT_NOT_NULL(
			call->buffer,
			"the hook must observe the allocated buffer"
		);
		TEST_ASSERT(
			call->buffer ==
				ADDR_OF(&call->module_ectx->module_prepared),
			"the derived buffer address must be the allocated one"
		);
	}

	TEST_ASSERT(
		prepare_test_calls[0].buffer != prepare_test_calls[1].buffer,
		"sibling module instances must not share a buffer"
	);

	return TEST_SUCCESS;
}

// Verifies that a repeated assignment pass re-invokes the hook per
// context without error and re-derives the same buffer address.
static int
run_prepare_repeat_pass_test(struct prepare_test_env *env) {
	uint64_t before = prepare_test_call_count;
	struct prepare_test_call first[PREPARE_TEST_MAX_CALLS];
	memcpy(first, prepare_test_calls, sizeof(first));

	dp_config_assign_worker_ectxs(env->dp_config, env->gen);

	TEST_ASSERT_EQUAL(
		prepare_test_call_count,
		before + 2,
		"a repeated pass must re-invoke the hook per module context"
	);
	for (uint64_t idx = 0; idx < 2; ++idx) {
		TEST_ASSERT(
			prepare_test_calls[before + idx].module_ectx ==
				first[idx].module_ectx,
			"a repeated pass must revisit the same contexts"
		);
		TEST_ASSERT(
			prepare_test_calls[before + idx].buffer ==
				first[idx].buffer,
			"a repeated pass must re-derive the same buffer address"
		);
	}

	return TEST_SUCCESS;
}

// Verifies that a module declaring no buffer size gets none
// allocated: the relative field stays empty and the pass leaves the
// absolute one empty too.
static int
run_zero_size_module_no_buffer_test(struct prepare_test_env *env) {
	struct config_gen_ectx *ectx = cp_config_gen_worker_ectx(env->gen, 0);
	struct module_ectx *no_buffer_ectx =
		prepare_test_find_module_ectx(ectx, "decap", "no-buffer");
	TEST_ASSERT_NOT_NULL(
		no_buffer_ectx, "module context of 'no-buffer' must exist"
	);

	TEST_ASSERT_NULL(
		ADDR_OF(&no_buffer_ectx->module_prepared),
		"a module with no declared size must get no buffer"
	);
	TEST_ASSERT_NULL(
		no_buffer_ectx->abs_module_prepared,
		"a module with no buffer must keep an empty absolute address"
	);

	return TEST_SUCCESS;
}

// Verifies that a module declaring a size without a hook still gets
// its buffer allocated and absolutized on the next generation, and
// that no call is recorded for it.
static int
run_null_handler_buffer_without_call_test(struct prepare_test_env *env) {
	TEST_ASSERT_SUCCESS(
		prepare_test_patch_module(
			env->dp_config, "decap", NULL, PREPARE_TEST_BUFFER_SIZE
		),
		"patching the decap slot failed"
	);

	yanet_error *err = NULL;
	TEST_ASSERT_SUCCESS(
		prepare_test_install_device(
			env->agent,
			env->dp_config,
			env->cp_config,
			"dev1",
			"pipe0",
			&err
		),
		"second device install failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_config_gen *gen = ADDR_OF(&env->cp_config->cp_config_gen);
	struct config_gen_ectx *ectx = cp_config_gen_worker_ectx(gen, 0);
	struct module_ectx *no_buffer_ectx =
		prepare_test_find_module_ectx(ectx, "decap", "no-buffer");
	TEST_ASSERT_NOT_NULL(
		no_buffer_ectx,
		"module context of 'no-buffer' must exist in the new generation"
	);

	TEST_ASSERT_NOT_NULL(
		ADDR_OF(&no_buffer_ectx->module_prepared),
		"a module with a declared size must get a buffer"
	);
	TEST_ASSERT(
		no_buffer_ectx->abs_module_prepared ==
			ADDR_OF(&no_buffer_ectx->module_prepared),
		"the pass must absolutize the buffer without a hook"
	);

	struct cp_module *decap_cp_module = ADDR_OF(&no_buffer_ectx->cp_module);
	for (uint64_t idx = 0; idx < prepare_test_call_count; ++idx) {
		TEST_ASSERT(
			prepare_test_calls[idx].cp_module != decap_cp_module,
			"no hook call may be recorded for a module without one"
		);
	}

	env->gen = gen;
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	const char *port_names[] = {"01:00.0"};
	const char *mods_to_load[] = {"forward", "decap"};
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

	LOG(INFO, "=== Starting Prepare Test Suite ===");

	struct prepare_test_env env;
	if (prepare_test_setup(&env, ut) != TEST_SUCCESS) {
		LOG(ERROR, "prepare test setup failed");
		agent_detach(env.agent);
		dataplane_ut_free(ut);
		return 1;
	}

	struct {
		const char *name;
		int (*fn)(struct prepare_test_env *);
	} tests[] = {
		{"prepare_hook_invoked_per_ectx",
		 run_prepare_hook_invoked_per_ectx_test},
		{"prepare_repeat_pass", run_prepare_repeat_pass_test},
		{"zero_size_module_no_buffer",
		 run_zero_size_module_no_buffer_test},
		{"null_handler_buffer_without_call",
		 run_null_handler_buffer_without_call_test},
	};

	size_t total = sizeof(tests) / sizeof(tests[0]);
	size_t failed = 0;

	for (size_t idx = 0; idx < total; ++idx) {
		LOG(INFO,
		    "[%zu/%zu] running %s...",
		    idx + 1,
		    total,
		    tests[idx].name);
		if (tests[idx].fn(&env) != TEST_SUCCESS) {
			LOG(ERROR, "%s FAILED", tests[idx].name);
			++failed;
		} else {
			LOG(INFO, "%s passed", tests[idx].name);
		}
	}

	agent_detach(env.agent);
	dataplane_ut_free(ut);

	if (failed == 0) {
		LOG(INFO, "=== All %zu prepare tests passed! ===", total);
	} else {
		LOG(ERROR, "=== %zu/%zu prepare tests failed ===", failed, total
		);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
