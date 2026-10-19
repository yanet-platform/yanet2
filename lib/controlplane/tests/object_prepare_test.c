// Per-worker execution-context commit handler for objects: the pass
// that derives a context's absolute addresses also fills each object's
// private buffer through its registered hook.
//
// Pins the invocation contract through the real build and delivery
// paths: a hook runs once per object execution context per assignment
// pass, sees its own context, object config and allocated buffer, and
// repeats on a later pass without error; an object declaring no buffer
// size gets none allocated; an object declaring a size without a hook
// gets its buffer allocated and absolutized but no call.

#include <errno.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "api/agent.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/strutils.h"
#include "common/test_assert.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_object.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/object/object.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

#define OBJECT_PREPARE_TEST_MEMORY_LIMIT (8u * 1024u * 1024u)
#define OBJECT_PREPARE_TEST_BUFFER_SIZE 64

// Recorded invocations of the counting hook; the registry is large
// enough for every call the scenarios can produce.
#define OBJECT_PREPARE_TEST_MAX_CALLS 16
struct object_prepare_test_call {
	struct object_ectx *object_ectx;
	struct cp_object *cp_object;
	void *buffer;
};
static struct object_prepare_test_call
	object_prepare_test_calls[OBJECT_PREPARE_TEST_MAX_CALLS];
static uint64_t object_prepare_test_call_count;

// Counts hook invocations, capturing the context, object config and
// derived buffer each call observed.
static void
object_prepare_test_count(
	struct object_ectx *object_ectx, struct cp_object *cp_object
) {
	if (object_prepare_test_call_count < OBJECT_PREPARE_TEST_MAX_CALLS) {
		object_prepare_test_calls[object_prepare_test_call_count] =
			(struct object_prepare_test_call){
				.object_ectx = object_ectx,
				.cp_object = cp_object,
				.buffer = object_ectx->abs_object_prepared,
			};
	}
	++object_prepare_test_call_count;
}

// Overrides a loaded object type's hook and buffer size, standing in
// for what an object constructor declares at load time.
static int
object_prepare_test_patch_object(
	struct dp_config *dp_config,
	const char *name,
	object_commit_ectx_handler handler,
	uint64_t prepared_size
) {
	uint64_t idx;
	TEST_ASSERT_SUCCESS(
		dp_config_lookup_object(dp_config, name, &idx),
		"object type '%s' must be loaded",
		name
	);
	struct dp_object *dp_objects = ADDR_OF(&dp_config->dp_objects);
	dp_objects[idx].commit_ectx_handler = handler;
	dp_objects[idx].prepared_size = prepared_size;

	return TEST_SUCCESS;
}

// Allocates and initializes a shared-memory object of the given type,
// mirroring what a typed object constructor does around the generic
// object header.
static struct cp_object *
object_prepare_test_new_object(
	struct agent *agent,
	const char *type,
	const char *name,
	yanet_error **err
) {
	struct cp_object *cp_object = (struct cp_object *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_object)
	);
	if (cp_object == NULL) {
		yanet_error_add(
			err, "failed to allocate object '%s:%s'", type, name
		);
		return NULL;
	}
	if (cp_object_init(cp_object, agent, type, name, err)) {
		memory_bfree(
			&agent->memory_context,
			cp_object,
			sizeof(struct cp_object)
		);
		return NULL;
	}
	return cp_object;
}

// Returns the per-worker context the generation built for the object
// registered under the given type and name, or NULL when either the
// object or its context is absent.
static struct object_ectx *
object_prepare_test_find_object_ectx(
	struct cp_config_gen *gen,
	struct config_gen_ectx *ectx,
	const char *type,
	const char *name
) {
	uint64_t idx;
	if (cp_config_gen_lookup_object_index(gen, type, name, &idx)) {
		return NULL;
	}
	return config_gen_ectx_get_object(ectx, idx);
}

// Shared fixture state: one harness, one agent, one generation built
// by the setup below.
struct object_prepare_test_env {
	struct yanet_shm *shm;
	struct agent *agent;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
	struct cp_config_gen *gen;
};

// Installs the objects (hook:hook-a, hook:hook-b, inert:no-buffer) into
// a fresh generation. The hook dataplane slot is patched to the
// counting hook with a declared buffer size before the install, so the
// ectx build and the delivery pass run the real allocation and hook
// paths; the inert slot keeps its zero declared size.
static int
object_prepare_test_setup(
	struct object_prepare_test_env *env, struct dataplane_ut *ut
) {
	memset(env, 0, sizeof(*env));
	env->shm = dataplane_ut_shm(ut);
	TEST_ASSERT_NOT_NULL(env->shm, "harness shared memory must exist");

	yanet_error *err = NULL;
	env->agent = agent_attach(
		env->shm,
		0,
		"object-prepare-test",
		OBJECT_PREPARE_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(
		env->agent,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	env->dp_config = agent_dp_config(env->agent);
	env->cp_config = ADDR_OF(&env->agent->cp_config);

	TEST_ASSERT_SUCCESS(
		object_prepare_test_patch_object(
			env->dp_config,
			"hook",
			object_prepare_test_count,
			OBJECT_PREPARE_TEST_BUFFER_SIZE
		),
		"patching the hook object slot failed"
	);

	struct cp_object *hook_a = object_prepare_test_new_object(
		env->agent, "hook", "hook-a", &err
	);
	TEST_ASSERT_NOT_NULL(
		hook_a,
		"object 'hook:hook-a' failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_object *hook_b = object_prepare_test_new_object(
		env->agent, "hook", "hook-b", &err
	);
	TEST_ASSERT_NOT_NULL(
		hook_b,
		"object 'hook:hook-b' failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_object *no_buffer = object_prepare_test_new_object(
		env->agent, "inert", "no-buffer", &err
	);
	TEST_ASSERT_NOT_NULL(
		no_buffer,
		"object 'inert:no-buffer' failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	struct cp_object *objects[] = {hook_a, hook_b, no_buffer};
	TEST_ASSERT_SUCCESS(
		agent_update_objects(env->agent, 3, objects, &err),
		"update_objects failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	env->gen = ADDR_OF(&env->cp_config->cp_config_gen);

	return TEST_SUCCESS;
}

// Verifies that the hook runs once per object execution context on the
// delivery pass, each call seeing its own context, its object config
// and the buffer allocated for that context, and that sibling objects
// receive distinct buffers.
static int
run_object_prepare_hook_invoked_per_ectx_test(
	struct object_prepare_test_env *env
) {
	struct config_gen_ectx *ectx = cp_config_gen_worker_ectx(env->gen, 0);
	TEST_ASSERT_NOT_NULL(ectx, "worker 0 execution context must exist");

	struct object_ectx *hook_a_ectx = object_prepare_test_find_object_ectx(
		env->gen, ectx, "hook", "hook-a"
	);
	struct object_ectx *hook_b_ectx = object_prepare_test_find_object_ectx(
		env->gen, ectx, "hook", "hook-b"
	);
	TEST_ASSERT_NOT_NULL(
		hook_a_ectx, "object context of 'hook-a' must exist"
	);
	TEST_ASSERT_NOT_NULL(
		hook_b_ectx, "object context of 'hook-b' must exist"
	);

	TEST_ASSERT_EQUAL(
		object_prepare_test_call_count,
		2,
		"the hook must run once per object instance in the delivery "
		"pass"
	);
	TEST_ASSERT(
		object_prepare_test_calls[0].object_ectx !=
			object_prepare_test_calls[1].object_ectx,
		"each object instance's call must carry its own context"
	);

	for (uint64_t idx = 0; idx < object_prepare_test_call_count; ++idx) {
		struct object_prepare_test_call *call =
			object_prepare_test_calls + idx;
		struct cp_object *cp_object =
			ADDR_OF(&call->object_ectx->cp_object);
		TEST_ASSERT(
			call->cp_object == cp_object,
			"the hook must receive the context's own object config"
		);
		TEST_ASSERT(
			call->object_ectx == hook_a_ectx ||
				call->object_ectx == hook_b_ectx,
			"every call must belong to an object of the patched "
			"slot"
		);
		TEST_ASSERT_NOT_NULL(
			call->buffer,
			"the hook must observe the allocated buffer"
		);
		TEST_ASSERT(
			call->buffer ==
				ADDR_OF(&call->object_ectx->object_prepared),
			"the derived buffer address must be the allocated one"
		);
	}

	TEST_ASSERT(
		object_prepare_test_calls[0].buffer !=
			object_prepare_test_calls[1].buffer,
		"sibling object instances must not share a buffer"
	);

	return TEST_SUCCESS;
}

// Verifies that a repeated assignment pass re-invokes the hook per
// context without error and re-derives the same buffer address.
static int
run_object_prepare_repeat_pass_test(struct object_prepare_test_env *env) {
	uint64_t before = object_prepare_test_call_count;
	struct object_prepare_test_call first[OBJECT_PREPARE_TEST_MAX_CALLS];
	memcpy(first, object_prepare_test_calls, sizeof(first));

	dp_config_assign_worker_ectxs(env->dp_config, env->gen);

	TEST_ASSERT_EQUAL(
		object_prepare_test_call_count,
		before + 2,
		"a repeated pass must re-invoke the hook per object context"
	);
	for (uint64_t idx = 0; idx < 2; ++idx) {
		TEST_ASSERT(
			object_prepare_test_calls[before + idx].object_ectx ==
				first[idx].object_ectx,
			"a repeated pass must revisit the same contexts"
		);
		TEST_ASSERT(
			object_prepare_test_calls[before + idx].buffer ==
				first[idx].buffer,
			"a repeated pass must re-derive the same buffer address"
		);
	}

	return TEST_SUCCESS;
}

// Verifies that an object declaring no buffer size gets none
// allocated: the relative field stays empty and the pass leaves the
// absolute one empty too.
static int
run_object_prepare_zero_size_no_buffer_test(struct object_prepare_test_env *env
) {
	struct config_gen_ectx *ectx = cp_config_gen_worker_ectx(env->gen, 0);
	struct object_ectx *no_buffer_ectx =
		object_prepare_test_find_object_ectx(
			env->gen, ectx, "inert", "no-buffer"
		);
	TEST_ASSERT_NOT_NULL(
		no_buffer_ectx, "object context of 'no-buffer' must exist"
	);

	TEST_ASSERT_NULL(
		ADDR_OF(&no_buffer_ectx->object_prepared),
		"an object with no declared size must get no buffer"
	);
	TEST_ASSERT_NULL(
		no_buffer_ectx->abs_object_prepared,
		"an object with no buffer must keep an empty absolute address"
	);

	return TEST_SUCCESS;
}

// Verifies that an object declaring a size without a hook still gets
// its buffer allocated and absolutized on the next generation, and
// that no call is recorded for it.
static int
run_object_prepare_null_handler_buffer_without_call_test(
	struct object_prepare_test_env *env
) {
	TEST_ASSERT_SUCCESS(
		object_prepare_test_patch_object(
			env->dp_config,
			"inert",
			NULL,
			OBJECT_PREPARE_TEST_BUFFER_SIZE
		),
		"patching the inert object slot failed"
	);

	yanet_error *err = NULL;
	struct cp_object *with_buffer = object_prepare_test_new_object(
		env->agent, "inert", "with-buffer", &err
	);
	TEST_ASSERT_NOT_NULL(
		with_buffer,
		"object 'inert:with-buffer' failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_object *objects[] = {with_buffer};
	TEST_ASSERT_SUCCESS(
		agent_update_objects(env->agent, 1, objects, &err),
		"update_objects failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_config_gen *gen = ADDR_OF(&env->cp_config->cp_config_gen);
	struct config_gen_ectx *ectx = cp_config_gen_worker_ectx(gen, 0);
	struct object_ectx *no_buffer_ectx =
		object_prepare_test_find_object_ectx(
			gen, ectx, "inert", "no-buffer"
		);
	TEST_ASSERT_NOT_NULL(
		no_buffer_ectx,
		"object context of 'no-buffer' must exist in the new generation"
	);

	TEST_ASSERT_NOT_NULL(
		ADDR_OF(&no_buffer_ectx->object_prepared),
		"an object with a declared size must get a buffer"
	);
	TEST_ASSERT(
		no_buffer_ectx->abs_object_prepared ==
			ADDR_OF(&no_buffer_ectx->object_prepared),
		"the pass must absolutize the buffer without a hook"
	);

	struct cp_object *inert_cp_object = ADDR_OF(&no_buffer_ectx->cp_object);
	for (uint64_t idx = 0; idx < object_prepare_test_call_count; ++idx) {
		TEST_ASSERT(
			object_prepare_test_calls[idx].cp_object !=
				inert_cp_object,
			"no hook call may be recorded for an object without one"
		);
	}

	env->gen = gen;
	return TEST_SUCCESS;
}

// Constructor of the "hook" object type, which the harness resolves from
// this executable: the test setup installs its hook and prepared buffer.
struct object *
new_object_hook(void) {
	struct object *object =
		(struct object *)calloc(1, sizeof(struct object));
	if (object != NULL) {
		strtcpy(object->name, "hook", sizeof(object->name));
	}
	return object;
}

// Constructor of the "inert" object type, which the harness resolves from
// this executable: it declares no hook and no prepared buffer.
struct object *
new_object_inert(void) {
	struct object *object =
		(struct object *)calloc(1, sizeof(struct object));
	if (object != NULL) {
		strtcpy(object->name, "inert", sizeof(object->name));
	}
	return object;
}

int
main(void) {
	log_enable_name("info");

	const char *port_names[] = {"01:00.0"};
	const char *devs_to_load[] = {"plain"};
	const char *objs_to_load[] = {"hook", "inert"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 26,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
		.objects_to_load = objs_to_load,
		.objects_to_load_count = 2,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	if (ut == NULL) {
		fprintf(stderr, "dataplane_ut_new failed\n");
		return 1;
	}

	LOG(INFO, "=== Starting Object Prepare Test Suite ===");

	struct object_prepare_test_env env;
	if (object_prepare_test_setup(&env, ut) != TEST_SUCCESS) {
		LOG(ERROR, "object prepare test setup failed");
		agent_detach(env.agent);
		dataplane_ut_free(ut);
		return 1;
	}

	struct {
		const char *name;
		int (*fn)(struct object_prepare_test_env *);
	} tests[] = {
		{"object_prepare_hook_invoked_per_ectx",
		 run_object_prepare_hook_invoked_per_ectx_test},
		{"object_prepare_repeat_pass",
		 run_object_prepare_repeat_pass_test},
		{"object_prepare_zero_size_no_buffer",
		 run_object_prepare_zero_size_no_buffer_test},
		{"object_prepare_null_handler_buffer_without_call",
		 run_object_prepare_null_handler_buffer_without_call_test},
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
		LOG(INFO, "=== All %zu object prepare tests passed! ===", total
		);
	} else {
		LOG(ERROR,
		    "=== %zu/%zu object prepare tests failed ===",
		    failed,
		    total);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
