/*
 * Tests for the dangling-reference free protocol on modules.
 *
 * A module's reference count is the number of registries — live
 * configuration generations — that registered it; construction takes no
 * reference of its own. Only a dangling module, at zero references and
 * registered nowhere, may be destroyed, and only by its owner: the free
 * entry point of the module's own type. While a generation still holds
 * the module, the free attempt is answered with EAGAIN and the owner
 * must retry later.
 */

#include "api/agent.h"

#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/zone.h"

#include "modules/decap/api/controlplane.h"
#include "modules/forward/api/controlplane.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "lib/logging/log.h"

#include <errno.h>
#include <stdio.h>

#define DANGLING_TEST_MEMORY_LIMIT (2u * 1024u * 1024u)

// A never-registered module is dangling from birth: the owner's free must
// destroy it on the spot and return its memory to the arena.
static int
test_unregistered_free_destroys(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"dangling-unregistered",
		DANGLING_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_module *decap0 = decap_module_config_new(agent, "d0", &err);
	TEST_ASSERT_NOT_NULL(decap0, "decap_module_config_new failed");
	TEST_ASSERT(
		block_allocator_free_size(&agent->block_allocator) < baseline,
		"constructing the module must consume arena memory"
	);

	yanet_error *free_err = NULL;
	TEST_ASSERT_SUCCESS(
		decap_module_config_free(decap0, &free_err),
		"freeing a never-registered module must destroy it"
	);
	TEST_ASSERT_EQUAL(
		(long)block_allocator_free_size(&agent->block_allocator),
		(long)baseline,
		"destroying the module must return its memory to the arena"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// A module referenced by a registry — a live generation — must refuse the
// owner's free with EAGAIN and keep its memory; once the registry is
// retired and the module turns dangling, the same free succeeds.
static int
test_referenced_free_eagain_then_destroy(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "dangling-referenced", DANGLING_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_module *decap0 = decap_module_config_new(agent, "d0", &err);
	TEST_ASSERT_NOT_NULL(decap0, "decap_module_config_new failed");

	// Simulate a live generation holding decap0: a registry reference,
	// exactly like an install would take.
	struct cp_module_registry reg;
	TEST_ASSERT_SUCCESS(
		cp_module_registry_init(&agent->memory_context, &reg, &err),
		"cp_module_registry_init failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_module_registry_upsert(&reg, "decap", "d0", decap0, &err),
		"cp_module_registry_upsert failed"
	);

	yanet_error *free_err = NULL;
	TEST_ASSERT(
		decap_module_config_free(decap0, &free_err) == -1 &&
			errno == EAGAIN,
		"freeing a generation-referenced module must fail with EAGAIN"
	);
	yanet_error_free(free_err);
	TEST_ASSERT(
		block_allocator_free_size(&agent->block_allocator) < baseline,
		"a refused free must leave the module's memory in place"
	);

	// Retire the generation: decap0 turns dangling and the same free
	// succeeds.
	cp_module_registry_fini(&reg);
	free_err = NULL;
	TEST_ASSERT_SUCCESS(
		decap_module_config_free(decap0, &free_err),
		"freeing a dangling module after its last generation retired "
		"must destroy it"
	);
	TEST_ASSERT_EQUAL(
		(long)block_allocator_free_size(&agent->block_allocator),
		(long)baseline,
		"destroying the module must return the arena to its baseline"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Two module types share one agent — as decap and forward do here, and as
// acl and fwstate do in production. After the shared generation retires,
// each type's owner destroys its own module without any knowledge of the
// other type, and each destruction returns only that module's memory.
static int
test_typed_destroy_is_per_owner(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "dangling-per-owner", DANGLING_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_module *decap0 = decap_module_config_new(agent, "d0", &err);
	TEST_ASSERT_NOT_NULL(decap0, "decap_module_config_new failed");
	struct cp_module *forward0 =
		forward_module_config_init(agent, "f0", &err);
	TEST_ASSERT_NOT_NULL(forward0, "forward_module_config_init failed");

	struct cp_module_registry reg;
	TEST_ASSERT_SUCCESS(
		cp_module_registry_init(&agent->memory_context, &reg, &err),
		"cp_module_registry_init failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_module_registry_upsert(&reg, "decap", "d0", decap0, &err),
		"cp_module_registry_upsert(decap) failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_module_registry_upsert(
			&reg, "forward", "f0", forward0, &err
		),
		"cp_module_registry_upsert(forward) failed"
	);

	// Both frees are refused while the generation holds the modules.
	yanet_error *free_err = NULL;
	TEST_ASSERT(
		decap_module_config_free(decap0, &free_err) == -1 &&
			errno == EAGAIN,
		"freeing a generation-referenced decap must fail with EAGAIN"
	);
	yanet_error_free(free_err);
	free_err = NULL;
	TEST_ASSERT(
		forward_module_config_free(forward0, &free_err) == -1 &&
			errno == EAGAIN,
		"freeing a generation-referenced forward must fail with EAGAIN"
	);
	yanet_error_free(free_err);

	// Retire the generation; the forward owner destroys its module only.
	cp_module_registry_fini(&reg);
	size_t after_forward =
		block_allocator_free_size(&agent->block_allocator);
	free_err = NULL;
	TEST_ASSERT_SUCCESS(
		forward_module_config_free(forward0, &free_err),
		"forward's owner must destroy its dangling module"
	);
	TEST_ASSERT(
		block_allocator_free_size(&agent->block_allocator) >
			after_forward,
		"destroying the forward module must return its memory"
	);

	// decap0 is still intact: its owner has not asked to destroy it, and
	// nothing the forward owner did may touch it.
	free_err = NULL;
	TEST_ASSERT_SUCCESS(
		decap_module_config_free(decap0, &free_err),
		"decap's owner must still be able to destroy its module"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *mods_to_load[] = {"decap", "forward"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 24,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.modules = mods_to_load,
		.module_count = 2,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	if (ut == NULL) {
		fprintf(stderr, "dataplane_ut_new failed\n");
		return 1;
	}

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	if (shm == NULL) {
		fprintf(stderr, "dataplane_ut_shm returned NULL\n");
		dataplane_ut_free(ut);
		return 1;
	}

	int res = test_unregistered_free_destroys(shm);
	if (res == TEST_SUCCESS) {
		res = test_referenced_free_eagain_then_destroy(shm);
	}
	if (res == TEST_SUCCESS) {
		res = test_typed_destroy_is_per_owner(shm);
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
