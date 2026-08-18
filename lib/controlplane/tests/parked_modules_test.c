/*
 * Regression test for the module-parking mechanism: a construction call
 * must reclaim only parked entries of its own type, and parking an
 * already-parked module must not extend the list into a cycle.
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

#include <stdio.h>

#define PARKED_TEST_MEMORY_LIMIT (2u * 1024u * 1024u)

// Reproduces a real cross-service parking scenario: releasing one
// service's module while another service shares the same agent.
//
// In production, acl and fwstate share one agent. A released fwstate
// handle parks once its last reference drops, and an unrelated acl update
// must leave it alone. Decap and forward stand in for fwstate and acl
// here, since both link into every test harness. A release no longer
// reclaims by itself, so whichever module each type releases last here
// stays parked until a later construction of that same type reclaims it.
static int
test_drain_parked_filters_by_type(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "parked-drain-filter", PARKED_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_module *decap0 = decap_module_config_new(agent, "d0", &err);
	TEST_ASSERT_NOT_NULL(decap0, "decap_module_config_new failed");

	// Simulate a live generation holding decap0: a manual registry
	// reference, exactly like an install would take.
	struct cp_module_registry reg;
	TEST_ASSERT_SUCCESS(
		cp_module_registry_init(&agent->memory_context, &reg, &err),
		"cp_module_registry_init failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_module_registry_upsert(&reg, "decap", "d0", decap0, &err),
		"cp_module_registry_upsert failed"
	);

	// Release the creator's own reference, exactly as decap's control
	// plane does on free.
	//
	// The generation reference above still holds decap0 alive, so
	// nothing parks yet.
	decap_module_config_free(decap0);
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->parked_modules),
		"nothing must be parked while a generation still holds decap0"
	);

	// Retire the generation: its last reference drops and decap0 parks.
	cp_module_registry_fini(&reg);
	TEST_ASSERT(
		ADDR_OF(&agent->parked_modules) == decap0,
		"decap0 must be parked once its last reference drops"
	);

	// An unrelated forward construction must not touch a parked decap
	// module.
	struct cp_module *forward0 =
		forward_module_config_init(agent, "f0", &err);
	TEST_ASSERT_NOT_NULL(forward0, "forward_module_config_init failed");
	TEST_ASSERT(
		ADDR_OF(&agent->parked_modules) == decap0,
		"forward's own construction must leave a parked decap module "
		"untouched"
	);

	// A decap construction reaches its own type's parked list and
	// destroys decap0.
	struct cp_module *decap1 = decap_module_config_new(agent, "d1", &err);
	TEST_ASSERT_NOT_NULL(decap1, "decap_module_config_new failed");
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->parked_modules),
		"decap's own construction must reclaim the parked decap module"
	);

	// Steady state: exactly one live decap and one live forward module.
	// Reconstructing each type below must return to this same byte count.
	size_t checkpoint = block_allocator_free_size(&agent->block_allocator);

	// Freeing the only live instance of each type parks it instead of
	// destroying it: nothing constructs that type again yet to reclaim it.
	forward_module_config_free(forward0);
	decap_module_config_free(decap1);
	TEST_ASSERT(
		ADDR_OF(&agent->parked_modules) == decap1,
		"the last release of each type must park it, not destroy it"
	);
	TEST_ASSERT(
		ADDR_OF(&decap1->parked_next) == forward0,
		"parking must chain onto the existing list head"
	);
	TEST_ASSERT(
		ADDR_OF(&forward0->parked_next) == forward0,
		"the list tail stays self-referential"
	);
	TEST_ASSERT_EQUAL(
		(long)block_allocator_free_size(&agent->block_allocator),
		(long)checkpoint,
		"parking must not itself move memory"
	);

	// A later construction of each type reclaims what its own release
	// left parked, exactly as decap1 reclaimed decap0 above.
	struct cp_module *decap2 = decap_module_config_new(agent, "d2", &err);
	TEST_ASSERT_NOT_NULL(decap2, "decap_module_config_new failed");
	TEST_ASSERT(
		ADDR_OF(&agent->parked_modules) == forward0,
		"a decap construction must reclaim only the parked decap module"
	);

	struct cp_module *forward1 =
		forward_module_config_init(agent, "f1", &err);
	TEST_ASSERT_NOT_NULL(forward1, "forward_module_config_init failed");
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->parked_modules),
		"a forward construction must reclaim the parked forward module"
	);
	TEST_ASSERT_EQUAL(
		(long)block_allocator_free_size(&agent->block_allocator),
		(long)checkpoint,
		"replacing each type's parked predecessor must return to the "
		"same steady state"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// A duplicate zero-transition on an already-parked module must not link
// it in twice.
//
// A single-node list can't tell: the head is already the node itself, so
// a duplicate push is a no-op regardless. This drives it on a two-node
// list instead: park an older module, then a newer one on top of it,
// then re-drive the callback on the older, tail entry. Without the
// guard that relinks the tail in front of the head, closing the two
// nodes into a cycle a real drain call never finishes walking.
static int
test_park_is_idempotent(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "parked-park-idempotent", PARKED_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_module *decap_old =
		decap_module_config_new(agent, "idem-old", &err);
	TEST_ASSERT_NOT_NULL(decap_old, "decap_module_config_new failed");
	struct cp_module *decap_new =
		decap_module_config_new(agent, "idem-new", &err);
	TEST_ASSERT_NOT_NULL(decap_new, "decap_module_config_new failed");

	size_t checkpoint = block_allocator_free_size(&agent->block_allocator);

	// Park the older module first, then the newer one on top of it: head
	// decap_new, tail decap_old self-referential.
	cp_module_registry_item_free_cb(&decap_old->config_item, NULL);
	cp_module_registry_item_free_cb(&decap_new->config_item, NULL);
	TEST_ASSERT(
		ADDR_OF(&agent->parked_modules) == decap_new,
		"the second park must move the head to decap_new"
	);
	TEST_ASSERT(
		ADDR_OF(&decap_new->parked_next) == decap_old,
		"decap_new must link onto decap_old"
	);
	TEST_ASSERT(
		ADDR_OF(&decap_old->parked_next) == decap_old,
		"decap_old must remain the self-referential tail"
	);

	// Drive the zero-transition callback again on the tail, the same way
	// a lost update between two racing releases once could.
	cp_module_registry_item_free_cb(&decap_old->config_item, NULL);

	// Walk from the head with a bound past the real list length: a cyclic
	// corruption fails this assertion instead of hanging the traversal.
	struct cp_module *seen[8];
	size_t seen_count = 0;
	struct cp_module *node = ADDR_OF(&agent->parked_modules);
	while (node != NULL && seen_count < 8) {
		seen[seen_count++] = node;
		struct cp_module *next = ADDR_OF(&node->parked_next);
		node = (next == node) ? NULL : next;
	}
	TEST_ASSERT_EQUAL(
		(long)seen_count,
		2L,
		"a duplicate zero-transition must not change the list length"
	);
	TEST_ASSERT(
		seen[0] == decap_new && seen[1] == decap_old,
		"a duplicate zero-transition must not reorder the list"
	);
	TEST_ASSERT(
		ADDR_OF(&decap_old->parked_next) == decap_old,
		"a duplicate push must leave decap_old's own link untouched"
	);
	TEST_ASSERT_EQUAL(
		(long)block_allocator_free_size(&agent->block_allocator),
		(long)checkpoint,
		"parking and re-parking an already-parked module must not move "
		"memory"
	);

	// A later decap construction reclaims every parked entry of its own
	// type, decap_old and decap_new alike.
	struct cp_module *decap_next =
		decap_module_config_new(agent, "idem-next", &err);
	TEST_ASSERT_NOT_NULL(decap_next, "decap_module_config_new failed");
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->parked_modules),
		"construction must reclaim every parked module of its own type"
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

	int res = test_drain_parked_filters_by_type(shm);
	if (res == TEST_SUCCESS) {
		res = test_park_is_idempotent(shm);
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
