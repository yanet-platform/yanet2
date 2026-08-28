/*
 * Unit tests for the generic shared object entity: lifecycle symmetry,
 * stable name index across copy/upsert/delete, counter continuity across
 * replacement, and the agent reclamation gate driven by loaded_object_count.
 */

#include "api/agent.h"

#include "common/memory_block.h"
#include "common/test_assert.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_object.h"

#include "lib/counters/counters.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "lib/logging/log.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CP_OBJECT_TEST_MEMORY_LIMIT (4u * 1024u * 1024u)

// The attach-gate test spins up three same-name agents to exercise the
// reclamation gate but holds only one tiny object, so it uses a smaller
// arena than the registry-heavy subtests.
#define CP_OBJECT_GATE_MEMORY_LIMIT (1u * 1024u * 1024u)

// Lifecycle symmetry: allocate+init then fini+reclaim, plus the zero-init
// fini no-op. The agent arena must return to its pre-test size.
//
// loaded_object_count tracks only generation references, so the creator's
// own reference never touches it and init/fini leave the count at zero.
static int
test_cp_object_lifecycle(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "cp-object-life", CP_OBJECT_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		0,
		"fresh agent must start with zero loaded_object_count"
	);

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_object *object = (struct cp_object *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_object)
	);
	TEST_ASSERT_NOT_NULL(object, "object allocation failed");
	TEST_ASSERT_SUCCESS(
		cp_object_init(object, agent, "test", "lifecycle", &err),
		"cp_object_init failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		0,
		"loaded_object_count must stay 0 after cp_object_init without "
		"a registry upsert"
	);
	cp_object_fini(object);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		0,
		"loaded_object_count must stay 0 after cp_object_fini"
	);
	memory_bfree(&agent->memory_context, object, sizeof(*object));

	// fini must be a safe no-op on a zero-initialized struct.
	struct cp_object zero;
	memset(&zero, 0, sizeof(zero));
	cp_object_fini(&zero);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		0,
		"zero-init fini must not perturb loaded_object_count"
	);

	size_t after = block_allocator_free_size(&agent->block_allocator);
	TEST_ASSERT_EQUAL(
		(long)after,
		(long)baseline,
		"arena did not return to baseline after lifecycle: "
		"baseline=%zu after=%zu",
		baseline,
		after
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Stable name index across registry_copy, slot-preserving upsert-replace,
// and delete+reinsert; counter continuity via counter_registry_link.
//
// loaded_object_count tracks generation references: each copy, upsert, and
// delete mirrors the reference it adds or drops into the owning agent, and
// finalizing every registry that referenced the objects returns the count
// to zero.
static int
test_cp_object_index_stability(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "cp-object-idx", CP_OBJECT_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_object_registry registry;
	TEST_ASSERT_SUCCESS(
		cp_object_registry_init(
			&agent->memory_context, NULL, &registry, &err
		),
		"object registry init failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	// "foo" carries a named counter so a later replace can verify the
	// counter definition is linked forward to its replacement.
	struct cp_object *foo = (struct cp_object *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_object)
	);
	TEST_ASSERT_NOT_NULL(foo, "object allocation (foo) failed");
	TEST_ASSERT_SUCCESS(
		cp_object_init(foo, agent, "test", "foo", &err),
		"cp_object_init(foo) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT(
		counter_registry_register(
			&foo->counter_registry, "bytes", 2, &err
		) != COUNTER_INVALID,
		"failed to register counter on foo"
	);

	struct cp_object *bar = (struct cp_object *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_object)
	);
	TEST_ASSERT_NOT_NULL(bar, "object allocation (bar) failed");
	TEST_ASSERT_SUCCESS(
		cp_object_init(bar, agent, "test", "bar", &err),
		"cp_object_init(bar) failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	TEST_ASSERT_SUCCESS(
		cp_object_registry_upsert(&registry, "test", "foo", foo, &err),
		"upsert(foo) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		1,
		"loaded_object_count must be 1 after upsert(foo)"
	);
	TEST_ASSERT_SUCCESS(
		cp_object_registry_upsert(&registry, "test", "bar", bar, &err),
		"upsert(bar) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		2,
		"loaded_object_count must be 2 after upsert(bar)"
	);

	uint64_t foo_index;
	uint64_t bar_index;
	TEST_ASSERT_SUCCESS(
		cp_object_registry_lookup_index(
			&registry, "test", "foo", &foo_index
		),
		"lookup(foo) failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_object_registry_lookup_index(
			&registry, "test", "bar", &bar_index
		),
		"lookup(bar) failed"
	);

	// registry_copy is slot-by-slot, so indices survive into the copy.
	// Each copied item gains a generation reference, so the count rises
	// by one per referenced object.
	struct cp_object_registry copied;
	TEST_ASSERT_SUCCESS(
		cp_object_registry_copy(
			&agent->memory_context, &copied, &registry, &err
		),
		"cp_object_registry_copy failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		4,
		"registry_copy must mirror a generation reference per object"
	);

	uint64_t index;
	TEST_ASSERT_SUCCESS(
		cp_object_registry_lookup_index(&copied, "test", "foo", &index),
		"lookup(foo) in copy failed"
	);
	TEST_ASSERT_EQUAL(
		(long)index,
		(long)foo_index,
		"foo index must be stable across copy"
	);
	TEST_ASSERT_SUCCESS(
		cp_object_registry_lookup_index(&copied, "test", "bar", &index),
		"lookup(bar) in copy failed"
	);
	TEST_ASSERT_EQUAL(
		(long)index,
		(long)bar_index,
		"bar index must be stable across copy"
	);

	// Replace foo in the copy: slot-preserving, and the replacement
	// inherits the old counter definition via counter_registry_link.
	//
	// foo2 gains the copy's generation reference while foo loses it, so
	// the two changes cancel and the count stays at four.
	struct cp_object *foo2 = (struct cp_object *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_object)
	);
	TEST_ASSERT_NOT_NULL(foo2, "object allocation (foo2) failed");
	TEST_ASSERT_SUCCESS(
		cp_object_init(foo2, agent, "test", "foo", &err),
		"cp_object_init(foo2) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_SUCCESS(
		cp_object_registry_upsert(&copied, "test", "foo", foo2, &err),
		"upsert-replace(foo) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		4,
		"loaded_object_count must stay 4 after upsert(foo2) replaces "
		"foo in one of two registries"
	);
	TEST_ASSERT_SUCCESS(
		cp_object_registry_lookup_index(&copied, "test", "foo", &index),
		"lookup(foo) after replace failed"
	);
	TEST_ASSERT_EQUAL(
		(long)index,
		(long)foo_index,
		"foo index must be stable across upsert-replace"
	);
	TEST_ASSERT_EQUAL(
		(long)foo2->counter_registry.count,
		1L,
		"replacement must inherit the linked counter"
	);

	// Delete and reinsert bar in the copy: bar is still referenced by
	// the original registry, so the delete drops only the copy's
	// generation reference. The reinserted bar2 gains one back.
	TEST_ASSERT_SUCCESS(
		cp_object_registry_delete(&copied, "test", "bar"),
		"delete(bar) failed"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		3,
		"loaded_object_count must stay 3 after delete(bar) in one "
		"of two registries"
	);
	struct cp_object *bar2 = (struct cp_object *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_object)
	);
	TEST_ASSERT_NOT_NULL(bar2, "object allocation (bar2) failed");
	TEST_ASSERT_SUCCESS(
		cp_object_init(bar2, agent, "test", "bar", &err),
		"cp_object_init(bar2) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_SUCCESS(
		cp_object_registry_upsert(&copied, "test", "bar", bar2, &err),
		"upsert(bar2) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		4,
		"loaded_object_count must be 4 after upsert(bar2)"
	);
	TEST_ASSERT_SUCCESS(
		cp_object_registry_lookup_index(&copied, "test", "bar", &index),
		"lookup(bar) after reinsert failed"
	);
	TEST_ASSERT_EQUAL(
		(long)index,
		(long)bar_index,
		"bar index must be stable across delete+reinsert"
	);

	TEST_ASSERT(
		cp_object_registry_get(&copied, foo_index) == foo2,
		"registry_get(foo) must return the replacement"
	);
	TEST_ASSERT(
		cp_object_registry_get(&copied, bar_index) == bar2,
		"registry_get(bar) must return the reinserted object"
	);
	TEST_ASSERT(
		cp_object_registry_lookup(&copied, "test", "foo") == foo2,
		"registry_lookup(foo) must return the replacement"
	);

	// Finalizing both registries drops every generation reference: foo2
	// and bar2 retire from the copy, foo and bar retire from the original,
	// returning loaded_object_count to zero. Every object is now dangling
	// at a zero reference count, known only to this test.
	cp_object_registry_fini(&copied);
	cp_object_registry_fini(&registry);
	TEST_ASSERT_EQUAL(
		agent->loaded_object_count,
		0,
		"loaded_object_count must balance to 0 after both registries "
		"are finalized"
	);

	// The registries released their references but did not free the
	// objects' storage: each object lives in the agent's arena until its
	// owner destroys it or the agent itself is reclaimed. Take each
	// dangling object out of circulation and tear it down explicitly so
	// the arena returns to its pre-test size.
	yanet_error *free_err = NULL;
	TEST_ASSERT_SUCCESS(
		cp_object_try_destroy(foo, &free_err),
		"dangling object must be destroyable"
	);
	TEST_ASSERT_SUCCESS(
		cp_object_try_destroy(bar, &free_err),
		"dangling object must be destroyable"
	);
	TEST_ASSERT_SUCCESS(
		cp_object_try_destroy(foo2, &free_err),
		"dangling object must be destroyable"
	);
	TEST_ASSERT_SUCCESS(
		cp_object_try_destroy(bar2, &free_err),
		"dangling object must be destroyable"
	);
	cp_object_fini(foo);
	memory_bfree(&agent->memory_context, foo, sizeof(*foo));
	cp_object_fini(bar);
	memory_bfree(&agent->memory_context, bar, sizeof(*bar));
	cp_object_fini(foo2);
	memory_bfree(&agent->memory_context, foo2, sizeof(*foo2));
	cp_object_fini(bar2);
	memory_bfree(&agent->memory_context, bar2, sizeof(*bar2));

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

// The agent_attach reclamation gate must spare a previous agent that still
// owns live cp_objects: agent_cleanup frees the arena wholesale and cannot
// run per-object teardown, so an agent is only reclaimed once every loaded
// count reaches zero.
//
// Attach an agent, upsert one object into a registry on it (bumping its
// loaded_object_count), then re-attach under the same name: the previous
// agent survives in the new agent's prev chain because its count is
// non-zero. Once the registry is finalized (dropping the count to zero),
// a further re-attach reclaims every prior agent and leaves the new agent
// at the head of an empty prev chain.
static int
test_cp_object_attach_gate(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent1 = agent_attach(
		shm, 0, "cp-object-gate", CP_OBJECT_GATE_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent1, "agent_attach(agent1) failed");
	TEST_ASSERT_EQUAL(
		agent1->loaded_object_count,
		0,
		"fresh agent must start with zero loaded_object_count"
	);

	struct cp_object *object = (struct cp_object *)memory_balloc(
		&agent1->memory_context, sizeof(struct cp_object)
	);
	TEST_ASSERT_NOT_NULL(object, "object allocation failed");
	TEST_ASSERT_SUCCESS(
		cp_object_init(object, agent1, "test", "gate", &err),
		"cp_object_init failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_object_registry registry;
	TEST_ASSERT_SUCCESS(
		cp_object_registry_init(
			&agent1->memory_context, NULL, &registry, &err
		),
		"object registry init failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_SUCCESS(
		cp_object_registry_upsert(
			&registry, "test", "gate", object, &err
		),
		"upsert(gate) failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		agent1->loaded_object_count,
		1,
		"loaded_object_count must be 1 after upsert(gate)"
	);

	// Re-attach under the same name: agent1 becomes the previous agent
	// and must NOT be reclaimed while it still owns a referenced object.
	struct agent *agent2 = agent_attach(
		shm, 0, "cp-object-gate", CP_OBJECT_GATE_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent2, "agent_attach(agent2) failed");
	TEST_ASSERT(
		ADDR_OF(&agent2->prev) == agent1,
		"previous agent was reclaimed despite owning a referenced "
		"object"
	);

	// Finalizing the registry drops agent1's loaded_object_count back to
	// zero. agent1 is still alive (the gate held), so its memory context
	// stays valid for the object teardown that follows.
	cp_object_registry_fini(&registry);
	TEST_ASSERT_EQUAL(
		agent1->loaded_object_count,
		0,
		"loaded_object_count must be 0 after the registry is finalized"
	);
	cp_object_fini(object);
	memory_bfree(&agent1->memory_context, object, sizeof(*object));

	// A further re-attach now reclaims every prior agent (all three
	// counts zero) and leaves the new agent at the head of an empty prev
	// chain.
	struct agent *agent3 = agent_attach(
		shm, 0, "cp-object-gate", CP_OBJECT_GATE_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent3, "agent_attach(agent3) failed");
	TEST_ASSERT_NULL(
		ADDR_OF(&agent3->prev),
		"prior agents were not reclaimed once their object counts "
		"returned to zero"
	);

	agent_detach(agent3);
	return TEST_SUCCESS;
}

// cp_object_init must reject an object type that is not loaded into the
// dataplane's object-type registry, mirroring cp_module_init's "module type
// not found" path. A lookup miss must surface an error and leave the struct
// safe to free without further teardown.
static int
test_cp_object_init_unknown_type_fails(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "cp-object-unknown", CP_OBJECT_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_object *object = (struct cp_object *)memory_balloc(
		&agent->memory_context, sizeof(struct cp_object)
	);
	TEST_ASSERT_NOT_NULL(object, "object allocation failed");

	int ret = cp_object_init(
		object, agent, "no-such-object-type", "whatever", &err
	);
	TEST_ASSERT(
		ret != 0,
		"cp_object_init must fail for an unregistered object type"
	);
	TEST_ASSERT(
		err != NULL,
		"cp_object_init must surface an error on a lookup miss"
	);
	yanet_error_free(err);
	err = NULL;

	// init rolls back via cp_object_fini on the zeroed struct, so the
	// storage can be freed directly.
	memory_bfree(&agent->memory_context, object, sizeof(*object));

	size_t after = block_allocator_free_size(&agent->block_allocator);
	TEST_ASSERT_EQUAL(
		(long)after,
		(long)baseline,
		"arena did not return to baseline after failed init: "
		"baseline=%zu after=%zu",
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

	int res = test_cp_object_lifecycle(shm);
	if (res == TEST_SUCCESS) {
		res = test_cp_object_index_stability(shm);
	}
	if (res == TEST_SUCCESS) {
		res = test_cp_object_attach_gate(shm);
	}
	if (res == TEST_SUCCESS) {
		res = test_cp_object_init_unknown_type_fails(shm);
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
