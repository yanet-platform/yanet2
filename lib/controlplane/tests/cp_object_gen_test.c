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
#include "controlplane/config/econtext.h"
#include "controlplane/config/zone.h"

#include "counters/counters.h"

#include "devices/plain/api/controlplane.h"
#include "modules/forward/api/controlplane.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "logging/log.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CP_OBJECT_GEN_TEST_MEMORY_LIMIT (4u * 1024u * 1024u)
// A link-object unit test allocates only a base cp_module and a small objects
// array, so a small arena is plenty and avoids cumulative cp-pool pressure
// from the other tests' no-op agent_detach cycles.
#define CP_MODULE_LINK_TEST_MEMORY_LIMIT (256u * 1024u)
// The module-object-link integration test builds a full
// device-pipeline-function-chain-module ectx tree plus a forward module
// and an object.
#define MODULE_OBJECT_LINK_TEST_MEMORY_LIMIT (2u * 1024u * 1024u)

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
		cp_config_gen_lookup_object(gen, "test", "lookup-1") == obj1,
		"lookup_object(lookup-1) must return obj1"
	);
	TEST_ASSERT(
		cp_config_gen_lookup_object(gen, "test", "lookup-2") == obj2,
		"lookup_object(lookup-2) must return obj2"
	);

	uint64_t idx1;
	uint64_t idx2;
	TEST_ASSERT_SUCCESS(
		cp_config_gen_lookup_object_index(
			gen, "test", "lookup-1", &idx1
		),
		"lookup_object_index(lookup-1) failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_config_gen_lookup_object_index(
			gen, "test", "lookup-2", &idx2
		),
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
		cp_config_gen_lookup_object(gen, "test", "no-such-object"),
		"lookup of missing object must return NULL"
	);
	uint64_t unused_index;
	TEST_ASSERT(
		cp_config_gen_lookup_object_index(
			gen, "test", "no-such-object", &unused_index
		) != 0,
		"lookup_index of missing object must fail"
	);

	// Deleting each object from the live generation drops its last
	// reference (the prior generation is freed by the install), so the
	// free callback decrements loaded_object_count for each.
	TEST_ASSERT_SUCCESS(
		agent_delete_object(agent, "test", "lookup-1", &err),
		"delete_object(lookup-1) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_SUCCESS(
		agent_delete_object(agent, "test", "lookup-2", &err),
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
		cp_config_gen_lookup_object_index(
			gen, "test", "replace-me", &idx1
		),
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
		cp_config_gen_lookup_object_index(
			gen, "test", "replace-me", &new_idx1
		),
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
		agent_delete_object(agent, "test", "delete-me", &err),
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
		cp_config_gen_lookup_object(gen, "test", "delete-me"),
		"deleted object must not be findable in the live generation"
	);

	// Delete new_obj1 so every object's last reference has dropped.
	TEST_ASSERT_SUCCESS(
		agent_delete_object(agent, "test", "replace-me", &err),
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

// After install, the object's per-worker execution context owns a spawned
// counter storage carrying the object's registered counters, and that storage
// is reachable through the config generation's tag-indexed counter storage
// registry under the object_type and object_name tags.
static int
test_cp_object_gen_ectx_counter_storage(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"cp-object-gen-ectx",
		CP_OBJECT_GEN_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_object *obj = (struct cp_object *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_object)
	);
	TEST_ASSERT_NOT_NULL(obj, "object allocation failed");
	TEST_ASSERT_SUCCESS(
		cp_object_init(obj, agent, "test", "with-counters", &err),
		"cp_object_init failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT(
		counter_registry_register(
			&obj->counter_registry, "bytes", 2, &err
		) != COUNTER_INVALID,
		"failed to register counter on object"
	);
	TEST_ASSERT(
		counter_registry_register(
			&obj->link_counter_registry, "lookups", 1, &err
		) != COUNTER_INVALID,
		"failed to register counter on object link counter registry"
	);

	struct cp_object *objects[] = {obj};
	TEST_ASSERT_SUCCESS(
		agent_update_objects(agent, 1, objects, &err),
		"agent_update_objects failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	struct cp_config_gen *gen = ADDR_OF(&cp_config->cp_config_gen);

	uint64_t idx;
	TEST_ASSERT_SUCCESS(
		cp_config_gen_lookup_object_index(
			gen, "test", "with-counters", &idx
		),
		"lookup_object_index(with-counters) failed"
	);

	struct config_gen_ectx *ectx = cp_config_gen_worker_ectx(gen, 0);
	TEST_ASSERT_NOT_NULL(ectx, "worker 0 execution context must exist");

	struct object_ectx *object_ectx = config_gen_ectx_get_object(ectx, idx);
	TEST_ASSERT_NOT_NULL(
		object_ectx,
		"object execution context must exist for the installed object"
	);

	struct counter_storage *storage =
		ADDR_OF(&object_ectx->counter_storage);
	TEST_ASSERT_NOT_NULL(
		storage,
		"object execution context must own a spawned counter storage"
	);

	struct counter_registry *storage_registry = ADDR_OF(&storage->registry);
	TEST_ASSERT_EQUAL(
		(long)storage_registry->count,
		1L,
		"spawned storage must carry the object's registered counter"
	);

	// The dedicated link counter registry is independent of the object's
	// own registry: it carries the relation counter registered above, and
	// the object's spawned storage does not include it.
	TEST_ASSERT_EQUAL(
		(long)obj->link_counter_registry.count,
		1L,
		"link counter registry must hold its registered counter"
	);
	TEST_ASSERT(
		storage_registry != &obj->link_counter_registry,
		"object storage must spawn from the object's own registry, not "
		"the link registry"
	);

	// The same storage is reachable through the tag-indexed registry, so
	// the object's counters are queryable by object_type and object_name.
	struct counter_tag tags[] = {
		{.key = "object_type", .value = "test"},
		{.key = "object_name", .value = "with-counters"}
	};
	struct cp_counter_storage **found =
		cp_config_counter_storage_registry_find(
			ADDR_OF(&ectx->counter_storage_registry), tags, 2, NULL
		);
	TEST_ASSERT_NOT_NULL(found, "tag find must not fail");
	TEST_ASSERT(
		found[0] != NULL && found[1] == NULL,
		"tag find must match exactly the one object storage"
	);
	TEST_ASSERT(
		ADDR_OF(&found[0]->storage) == storage,
		"tag find must return the object execution context's storage"
	);
	free(found);

	TEST_ASSERT_SUCCESS(
		agent_delete_object(agent, "test", "with-counters", &err),
		"delete_object(with-counters) failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	cp_object_fini(obj);
	memory_bfree(&agent->memory_context, obj, sizeof(*obj));

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

// cp_module_link_object keys links by the object's (type, name): a repeat link
// returns the existing index, a same-name different-type object is a distinct
// link, and cp_module_fini reclaims the array. Uses a base cp_module whose
// memory context is initialized directly, avoiding module-type loading.
static int
test_cp_module_link_object(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"cp-module-link-object",
		CP_MODULE_LINK_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_module *module = (struct cp_module *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_module)
	);
	TEST_ASSERT_NOT_NULL(module, "module allocation failed");
	memset(module, 0, sizeof(struct cp_module));
	memory_context_init_from(
		&module->memory_context, &agent->memory_context, "test-module"
	);

	uint64_t idx1;
	TEST_ASSERT_SUCCESS(
		cp_module_link_object(module, "test", "obj-a", &idx1, &err),
		"link_object(test:obj-a) failed"
	);
	TEST_ASSERT_EQUAL((long)idx1, 0L, "first link must be at index 0");

	uint64_t idx1_again;
	TEST_ASSERT_SUCCESS(
		cp_module_link_object(
			module, "test", "obj-a", &idx1_again, &err
		),
		"link_object(test:obj-a) repeat failed"
	);
	TEST_ASSERT_EQUAL(
		(long)idx1_again,
		0L,
		"repeat link must return the existing index"
	);

	uint64_t idx2;
	TEST_ASSERT_SUCCESS(
		cp_module_link_object(module, "other", "obj-a", &idx2, &err),
		"link_object(other:obj-a) failed"
	);
	TEST_ASSERT_EQUAL(
		(long)idx2,
		1L,
		"same name, different type must append at index 1"
	);

	uint64_t idx3;
	TEST_ASSERT_SUCCESS(
		cp_module_link_object(module, "test", "obj-b", &idx3, &err),
		"link_object(test:obj-b) failed"
	);
	TEST_ASSERT_EQUAL(
		(long)idx3, 2L, "third distinct link must append at index 2"
	);
	TEST_ASSERT_EQUAL(
		(long)module->object_count, 3L, "object_count must be 3"
	);

	struct cp_module_object *objects = ADDR_OF(&module->objects);
	TEST_ASSERT(
		!strncmp(objects[0].type, "test", CP_OBJECT_TYPE_LEN) &&
			!strncmp(objects[0].name, "obj-a", CP_OBJECT_NAME_LEN),
		"link 0 must keep the (test, obj-a) identity"
	);
	TEST_ASSERT(
		!strncmp(objects[1].type, "other", CP_OBJECT_TYPE_LEN) &&
			!strncmp(objects[1].name, "obj-a", CP_OBJECT_NAME_LEN),
		"link 1 must keep the (other, obj-a) identity"
	);
	TEST_ASSERT(
		!strncmp(objects[2].type, "test", CP_OBJECT_TYPE_LEN) &&
			!strncmp(objects[2].name, "obj-b", CP_OBJECT_NAME_LEN),
		"link 2 must keep the (test, obj-b) identity"
	);

	// cp_module_fini frees the objects array (and the base allocations),
	// verifying the new cleanup path; the struct itself is then returned to
	// the agent arena.
	cp_module_fini(module);
	memory_bfree(&agent->memory_context, module, sizeof(*module));

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

static int
install_device_with_pipeline(
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
	// Drop the construction reference: on success the live generation
	// holds the device, on failure it parks on the agent.
	cp_device_plain_free(dev);
	return rc;
}

// A forward module linked to an installed object produces a per-worker link
// execution context whose spawned counter storage carries the object's link
// counters and is reachable through the tag-indexed registry under tags
// inherited from both the module's path and the linked object.
static int
test_module_object_link_ectx(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"mod-obj-link-ectx",
		MODULE_OBJECT_LINK_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct dp_config *dp_config = agent_dp_config(agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	// Install the object first so its per-worker ectx exists before the
	// module ectx build resolves the link.
	struct cp_object *obj = (struct cp_object *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_object)
	);
	TEST_ASSERT_NOT_NULL(obj, "object allocation failed");
	TEST_ASSERT_SUCCESS(
		cp_object_init(obj, agent, "test", "linked", &err),
		"cp_object_init failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT(
		counter_registry_register(
			&obj->link_counter_registry, "lookups", 1, &err
		) != COUNTER_INVALID,
		"failed to register link counter"
	);

	struct cp_object *objects[] = {obj};
	TEST_ASSERT_SUCCESS(
		agent_update_objects(agent, 1, objects, &err),
		"agent_update_objects failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	// Create the forward module and link the object to it before install.
	struct cp_module *module =
		forward_module_config_init(agent, "fwd0", &err);
	TEST_ASSERT_NOT_NULL(module, "forward_module_config_init failed");
	uint64_t link_idx;
	TEST_ASSERT_SUCCESS(
		cp_module_link_object(
			module, "test", "linked", &link_idx, &err
		),
		"cp_module_link_object failed"
	);
	TEST_ASSERT_EQUAL(
		(long)link_idx, 0L, "first object link must be at index 0"
	);
	struct cp_module *modules[] = {module};
	TEST_ASSERT_SUCCESS(
		cp_config_update_modules(
			dp_config, cp_config, 1, modules, &err
		),
		"update_modules failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	// Install the function, pipeline, then the device. The device install
	// triggers the full ectx build where the module's object link is
	// resolved and the link counter storage is spawned and registered.
	const char *const chain_types[] = {"forward"};
	const char *const chain_names[] = {"fwd0"};
	struct cp_chain_config *chain_cfg =
		cp_chain_config_create("chain0", 1, chain_types, chain_names);
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
			dp_config, cp_config, 1, func_cfgs, &err
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
			dp_config, cp_config, 1, pipe_cfgs, &err
		),
		"update_pipelines failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	cp_pipeline_config_free(pipe_cfg);

	TEST_ASSERT_SUCCESS(
		install_device_with_pipeline(
			agent, dp_config, cp_config, "dev0", "pipe0", &err
		),
		"update_devices failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	// The link counter storage is registered under tags combining the
	// module's path and the linked object's identity.
	struct cp_config_gen *gen = ADDR_OF(&cp_config->cp_config_gen);
	struct config_gen_ectx *ectx = cp_config_gen_worker_ectx(gen, 0);
	TEST_ASSERT_NOT_NULL(ectx, "worker 0 execution context must exist");

	struct counter_tag tags[] = {
		{.key = "module_type", .value = "forward"},
		{.key = "module_name", .value = "fwd0"},
		{.key = "object_type", .value = "test"},
		{.key = "object_name", .value = "linked"}
	};
	struct cp_counter_storage **found =
		cp_config_counter_storage_registry_find(
			ADDR_OF(&ectx->counter_storage_registry), tags, 4, NULL
		);
	TEST_ASSERT_NOT_NULL(found, "tag find must not fail");
	TEST_ASSERT(
		found[0] != NULL && found[1] == NULL,
		"tag find must match exactly the one link storage"
	);

	// The spawned storage carries the link counter registered on the
	// object's link counter registry.
	struct counter_storage *link_storage = ADDR_OF(&found[0]->storage);
	struct counter_registry *storage_registry =
		ADDR_OF(&link_storage->registry);
	TEST_ASSERT_EQUAL(
		(long)storage_registry->count,
		1L,
		"link storage must carry the object's link counter"
	);
	free(found);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Generation must fail when a module links to an object that is not installed:
// the device install triggers the ectx build where the link resolution fails.
//
// Uses distinct names from the positive test so the inherited generation's
// pipeline path is not disturbed.
static int
test_module_object_link_missing_fails(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"mod-obj-link-missing",
		MODULE_OBJECT_LINK_TEST_MEMORY_LIMIT,
		&err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct dp_config *dp_config = agent_dp_config(agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	// Create the forward module linking to an object that was never
	// installed. The module install succeeds because no device references
	// a pipeline containing this module yet.
	struct cp_module *module =
		forward_module_config_init(agent, "fwd-missing", &err);
	TEST_ASSERT_NOT_NULL(module, "forward_module_config_init failed");
	uint64_t link_idx;
	TEST_ASSERT_SUCCESS(
		cp_module_link_object(
			module, "test", "nonexistent", &link_idx, &err
		),
		"cp_module_link_object failed"
	);
	struct cp_module *modules[] = {module};
	TEST_ASSERT_SUCCESS(
		cp_config_update_modules(
			dp_config, cp_config, 1, modules, &err
		),
		"update_modules must succeed before pipeline is wired"
	);

	const char *const chain_types[] = {"forward"};
	const char *const chain_names[] = {"fwd-missing"};
	struct cp_chain_config *chain_cfg = cp_chain_config_create(
		"chain-missing", 1, chain_types, chain_names
	);
	TEST_ASSERT_NOT_NULL(chain_cfg, "chain_config_create failed");
	struct cp_function_config *func_cfg =
		cp_function_config_create("func-missing", 1);
	TEST_ASSERT_NOT_NULL(func_cfg, "function_config_create failed");
	TEST_ASSERT_SUCCESS(
		cp_function_config_set_chain(func_cfg, 0, chain_cfg, 1),
		"set_chain failed"
	);
	struct cp_function_config *func_cfgs[] = {func_cfg};
	TEST_ASSERT_SUCCESS(
		cp_config_update_functions(
			dp_config, cp_config, 1, func_cfgs, &err
		),
		"update_functions failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	cp_function_config_free(func_cfg);

	struct cp_pipeline_config *pipe_cfg =
		cp_pipeline_config_create("pipe-missing", 1);
	TEST_ASSERT_NOT_NULL(pipe_cfg, "pipeline_config_create failed");
	TEST_ASSERT_SUCCESS(
		cp_pipeline_config_set_function(pipe_cfg, 0, "func-missing"),
		"set_function failed"
	);
	struct cp_pipeline_config *pipe_cfgs[] = {pipe_cfg};
	TEST_ASSERT_SUCCESS(
		cp_config_update_pipelines(
			dp_config, cp_config, 1, pipe_cfgs, &err
		),
		"update_pipelines failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	cp_pipeline_config_free(pipe_cfg);

	// The device install triggers the full ectx build. The module's link
	// to the non-existent object fails resolution, so the generation
	// install must fail.
	int rc = install_device_with_pipeline(
		agent, dp_config, cp_config, "dev-missing", "pipe-missing", &err
	);
	TEST_ASSERT(
		rc != 0,
		"device install must fail when a linked object is missing"
	);
	yanet_error_free(err);
	err = NULL;

	agent_detach(agent);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	const char *mods_to_load[] = {"forward"};
	const char *devs_to_load[] = {"plain"};
	const char *objs_to_load[] = {"test"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 26,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = mods_to_load,
		.module_count = 1,
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
	if (res == TEST_SUCCESS) {
		res = test_cp_object_gen_ectx_counter_storage(shm);
	}
	if (res == TEST_SUCCESS) {
		res = test_cp_module_link_object(shm);
	}
	if (res == TEST_SUCCESS) {
		res = test_module_object_link_ectx(shm);
	}
	if (res == TEST_SUCCESS) {
		res = test_module_object_link_missing_fails(shm);
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
