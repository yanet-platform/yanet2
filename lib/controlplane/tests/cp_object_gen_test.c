/*
 * End-to-end test for cp_object wired through cp_config_gen: updating,
 * replacing, and deleting objects drives the same gen-swap path as
 * modules/devices/pipelines, and the refcount-based reclamation
 * releases exactly the objects retired by each installed generation.
 */

#include "api/agent.h"

#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"

#include "controlplane/agent/agent.h"
#include "controlplane/config/cp_object.h"
#include "controlplane/config/zone.h"

#include "counters/counters.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "logging/log.h"

#include <stdio.h>
#include <string.h>

#define CP_OBJECT_GEN_TEST_MEMORY_LIMIT (4u * 1024u * 1024u)

// Update installs objects into the live generation; lookup, lookup_index,
// and get_object all agree on identity and index, and a missing name
// resolves to NULL / failure. Objects are then deleted and the arena is
// returned to its pre-test size by tearing each object down explicitly.
static int
test_cp_object_gen_update_and_lookup(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"cp-object-gen-lookup",
		CP_OBJECT_GEN_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_object *obj1 = (struct cp_object *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_object)
	);
	TEST_ASSERT_NOT_NULL(obj1, "object allocation (obj1) failed");
	TEST_ASSERT_SUCCESS(
		cp_object_init(obj1, agent, "test", "lookup-1", &err),
		"cp_object_init(obj1) failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_object *obj2 = (struct cp_object *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_object)
	);
	TEST_ASSERT_NOT_NULL(obj2, "object allocation (obj2) failed");
	TEST_ASSERT_SUCCESS(
		cp_object_init(obj2, agent, "test", "lookup-2", &err),
		"cp_object_init(obj2) failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_object *objects[] = {obj1, obj2};
	TEST_ASSERT_SUCCESS(
		agent_update_objects(agent, 2, objects, &err),
		"agent_update_objects failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		2,
		"loaded_object_count must be 2 after update installs both "
		"objects"
	);

	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	struct cp_config_gen *gen = ADDR_OF(&cp_config->cp_config_gen);

	TEST_ASSERT(
		cp_config_gen_lookup_object(gen, "lookup-1") == obj1,
		"lookup_object(lookup-1) must return obj1"
	);
	TEST_ASSERT(
		cp_config_gen_lookup_object(gen, "lookup-2") == obj2,
		"lookup_object(lookup-2) must return obj2"
	);

	uint64_t idx1;
	uint64_t idx2;
	TEST_ASSERT_SUCCESS(
		cp_config_gen_lookup_object_index(gen, "lookup-1", &idx1),
		"lookup_object_index(lookup-1) failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_config_gen_lookup_object_index(gen, "lookup-2", &idx2),
		"lookup_object_index(lookup-2) failed"
	);
	TEST_ASSERT(idx1 != idx2, "indices for distinct objects collide");

	TEST_ASSERT(
		cp_config_gen_get_object(gen, idx1) == obj1,
		"get_object(idx1) must agree with lookup"
	);
	TEST_ASSERT(
		cp_config_gen_get_object(gen, idx2) == obj2,
		"get_object(idx2) must agree with lookup"
	);

	TEST_ASSERT_NULL(
		cp_config_gen_lookup_object(gen, "no-such-object"),
		"lookup of missing object must return NULL"
	);
	uint64_t unused_index;
	TEST_ASSERT(
		cp_config_gen_lookup_object_index(
			gen, "no-such-object", &unused_index
		) != 0,
		"lookup_index of missing object must fail"
	);

	// Deleting each object from the live generation drops its last
	// reference (the prior generation is freed by the install), so the
	// free callback decrements loaded_object_count for each.
	TEST_ASSERT_SUCCESS(
		agent_delete_object(agent, "lookup-1", &err),
		"delete_object(lookup-1) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_SUCCESS(
		agent_delete_object(agent, "lookup-2", &err),
		"delete_object(lookup-2) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		0,
		"loaded_object_count must balance to 0 after both objects "
		"are deleted"
	);

	// The objects' storage remains in the agent's arena until torn down
	// explicitly; the registry free callback only tracks the refcount.
	cp_object_fini(obj1);
	memory_bfree(&agent->memory_context, obj1, sizeof(*obj1));
	cp_object_fini(obj2);
	memory_bfree(&agent->memory_context, obj2, sizeof(*obj2));

	size_t after = block_allocator_free_size(&agent->block_allocator);
	TEST_ASSERT_EQUAL(
		(long)after,
		(long)baseline,
		"arena did not return to baseline: baseline=%zu after=%zu",
		baseline,
		after
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Replace is slot-preserving (the new object inherits the old index) and
// the replacement's counter registry inherits the prior object's counter
// definitions via counter_registry_link. Delete removes the object from
// the live generation. Across the update/replace/delete gen swaps each
// released object's last reference drops in the registry free callback,
// so loaded_object_count returns to zero; the arena is then returned to
// its pre-test size by tearing each object down explicitly.
static int
test_cp_object_gen_replace_delete(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"cp-object-gen-replace",
		CP_OBJECT_GEN_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	// obj1 carries a named counter so the replace step can verify the
	// counter definition is linked forward to its replacement.
	struct cp_object *obj1 = (struct cp_object *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_object)
	);
	TEST_ASSERT_NOT_NULL(obj1, "object allocation (obj1) failed");
	TEST_ASSERT_SUCCESS(
		cp_object_init(obj1, agent, "test", "replace-me", &err),
		"cp_object_init(obj1) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT(
		counter_registry_register(
			&obj1->counter_registry, "bytes", 2, &err
		) != COUNTER_INVALID,
		"failed to register counter on obj1"
	);

	struct cp_object *obj2 = (struct cp_object *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_object)
	);
	TEST_ASSERT_NOT_NULL(obj2, "object allocation (obj2) failed");
	TEST_ASSERT_SUCCESS(
		cp_object_init(obj2, agent, "test", "delete-me", &err),
		"cp_object_init(obj2) failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_object *objects[] = {obj1, obj2};
	TEST_ASSERT_SUCCESS(
		agent_update_objects(agent, 2, objects, &err),
		"agent_update_objects(initial) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		2,
		"loaded_object_count must be 2 after the initial update"
	);

	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	struct cp_config_gen *gen = ADDR_OF(&cp_config->cp_config_gen);

	uint64_t idx1;
	TEST_ASSERT_SUCCESS(
		cp_config_gen_lookup_object_index(gen, "replace-me", &idx1),
		"lookup_object_index(replace-me) failed before replace"
	);

	// Replace obj1 with a fresh object of the same name: the slot index
	// is preserved and the replacement inherits obj1's counter. obj1 is
	// still referenced by the prior generation until that generation is
	// freed by the install, at which point its last reference drops and
	// the free callback decrements loaded_object_count.
	struct cp_object *new_obj1 = (struct cp_object *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_object)
	);
	TEST_ASSERT_NOT_NULL(new_obj1, "object allocation (new_obj1) failed");
	TEST_ASSERT_SUCCESS(
		cp_object_init(new_obj1, agent, "test", "replace-me", &err),
		"cp_object_init(new_obj1) failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_object *replace_batch[] = {new_obj1};
	TEST_ASSERT_SUCCESS(
		agent_update_objects(agent, 1, replace_batch, &err),
		"agent_update_objects(replace) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		2,
		"loaded_object_count must stay 2 after replace: new_obj1 "
		"gains a reference while obj1 loses its last one"
	);

	gen = ADDR_OF(&cp_config->cp_config_gen);
	uint64_t new_idx1;
	TEST_ASSERT_SUCCESS(
		cp_config_gen_lookup_object_index(gen, "replace-me", &new_idx1),
		"lookup_object_index(replace-me) failed after replace"
	);
	TEST_ASSERT_EQUAL(
		(long)new_idx1,
		(long)idx1,
		"replace must preserve the slot index"
	);
	TEST_ASSERT(
		cp_config_gen_get_object(gen, new_idx1) == new_obj1,
		"get_object after replace must return the new object"
	);
	TEST_ASSERT_EQUAL(
		(long)new_obj1->counter_registry.count,
		1L,
		"replacement must inherit the linked counter"
	);

	// Delete obj2: it leaves the live generation, and its last reference
	// drops when the prior generation is freed by the install.
	TEST_ASSERT_SUCCESS(
		agent_delete_object(agent, "delete-me", &err),
		"delete_object(delete-me) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		1,
		"loaded_object_count must be 1 after delete-me retires"
	);

	gen = ADDR_OF(&cp_config->cp_config_gen);
	TEST_ASSERT_NULL(
		cp_config_gen_lookup_object(gen, "delete-me"),
		"deleted object must not be findable in the live generation"
	);

	// Delete new_obj1 so every object's last reference has dropped.
	TEST_ASSERT_SUCCESS(
		agent_delete_object(agent, "replace-me", &err),
		"delete_object(replace-me) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		0,
		"loaded_object_count must balance to 0 after every object is "
		"retired"
	);

	// Each object's storage remains in the agent's arena until torn
	// down explicitly.
	cp_object_fini(obj1);
	memory_bfree(&agent->memory_context, obj1, sizeof(*obj1));
	cp_object_fini(obj2);
	memory_bfree(&agent->memory_context, obj2, sizeof(*obj2));
	cp_object_fini(new_obj1);
	memory_bfree(&agent->memory_context, new_obj1, sizeof(*new_obj1));

	size_t after = block_allocator_free_size(&agent->block_allocator);
	TEST_ASSERT_EQUAL(
		(long)after,
		(long)baseline,
		"arena did not return to baseline: baseline=%zu after=%zu",
		baseline,
		after
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	const char *devs_to_load[] = {"plain"};
	const char *objs_to_load[] = {"test"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 25,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = NULL,
		.module_count = 0,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
		.objects_to_load = objs_to_load,
		.objects_to_load_count = 1,
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

	int res = test_cp_object_gen_update_and_lookup(shm);
	if (res == TEST_SUCCESS) {
		res = test_cp_object_gen_replace_delete(shm);
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
