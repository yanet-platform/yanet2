/*
 * Regression test for the module-parking mechanism: a construction call
 * drains every parked entry on the agent through each entry's own stored
 * teardown, and parking an already-parked module must not extend the list
 * into a cycle.
 */

#include "api/agent.h"

#include "common/container_of.h"
#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"

#include "controlplane/agent/agent.h"
#include "controlplane/config/cp_module.h"
#include "controlplane/config/zone.h"

#include "modules/decap/api/controlplane.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "logging/log.h"

#include <errno.h>
#include <signal.h>
#include <stdbool.h>
#include <stdio.h>
#include <sys/wait.h>
#include <unistd.h>

#define PARKED_TEST_MEMORY_LIMIT (1024u * 1024u)

// Frees any error a failed call above just set, before the TEST_ASSERT_*
// macro below returns on that same failure. Every call in this file that
// takes `&err` is expected to succeed, so this is a no-op on the ordinary
// path; it only fires on a genuine assertion failure, so that failure's
// real signal is not buried under an unrelated leak report.
static void
free_err_on_failure(bool failed, yanet_error **err) {
	if (failed) {
		yanet_error_free(*err);
		*err = NULL;
	}
}

// A minimal cp_module wrapper standing in for a real module's own config
// struct, used to drive distinguishable teardowns without depending on any
// real module's internal layout.
struct fake_module_config {
	struct cp_module cp_module;
	int id;
};

#define FAKE_DESTROY_LOG_CAP 8

static int fake_destroy_a_ids[FAKE_DESTROY_LOG_CAP];
static size_t fake_destroy_a_count;
static int fake_destroy_b_ids[FAKE_DESTROY_LOG_CAP];
static size_t fake_destroy_b_count;

static void
fake_module_config_destroy_common(
	struct cp_module *cp_module, int *ids, size_t *count
) {
	struct fake_module_config *fake =
		container_of(cp_module, struct fake_module_config, cp_module);
	struct agent *agent = ADDR_OF(&cp_module->agent);

	if (*count < FAKE_DESTROY_LOG_CAP) {
		ids[(*count)++] = fake->id;
	}

	cp_module_fini(cp_module);
	memory_bfree(&agent->memory_context, fake, sizeof(*fake));
}

static void
fake_module_config_destroy_a(struct cp_module *cp_module) {
	fake_module_config_destroy_common(
		cp_module, fake_destroy_a_ids, &fake_destroy_a_count
	);
}

static void
fake_module_config_destroy_b(struct cp_module *cp_module) {
	fake_module_config_destroy_common(
		cp_module, fake_destroy_b_ids, &fake_destroy_b_count
	);
}

static struct cp_module *
fake_module_config_new(
	struct agent *agent,
	const char *module_type,
	const char *module_name,
	int id,
	cp_module_free_handler destroy,
	yanet_error **err
) {
	struct fake_module_config *fake =
		(struct fake_module_config *)memory_balloc(
			&agent->memory_context,
			sizeof(struct fake_module_config)
		);
	if (fake == NULL) {
		return NULL;
	}

	if (cp_module_init(
		    &fake->cp_module,
		    agent,
		    module_type,
		    module_name,
		    destroy,
		    err
	    )) {
		memory_bfree(&agent->memory_context, fake, sizeof(*fake));
		return NULL;
	}
	fake->id = id;

	return &fake->cp_module;
}

// A drain reclaims every parked entry regardless of type, each through its
// own stored teardown rather than a teardown belonging to some other entry.
//
// Parks a decap-typed and a forward-typed fake module, both under one
// agent, then triggers the drain with a decap construction — the type one
// of the two parked entries shares, and the other does not. Recording each
// teardown's own log lets the assertions tell "both entries destroyed, each
// by its own handler" apart from "one handler destroyed everything", which
// a bare list-emptied check cannot.
//
// Parking is driven the way production drives it: a manual registry stands
// in for a live configuration generation holding both fakes, and only
// retiring that generation — not the creator's own release beforehand —
// drops each to zero references and parks it.
static int
test_drain_reclaims_every_type_via_own_teardown(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm,
		0,
		"parked-drain-every-type",
		PARKED_TEST_MEMORY_LIMIT,
		&err
	);
	free_err_on_failure(agent == NULL, &err);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	fake_destroy_a_count = 0;
	fake_destroy_b_count = 0;

	struct cp_module *decap_fake = fake_module_config_new(
		agent,
		"decap",
		"fake-decap",
		1,
		fake_module_config_destroy_a,
		&err
	);
	free_err_on_failure(decap_fake == NULL, &err);
	TEST_ASSERT_NOT_NULL(decap_fake, "fake_module_config_new failed");

	struct cp_module *forward_fake = fake_module_config_new(
		agent,
		"forward",
		"fake-forward",
		2,
		fake_module_config_destroy_b,
		&err
	);
	free_err_on_failure(forward_fake == NULL, &err);
	TEST_ASSERT_NOT_NULL(forward_fake, "fake_module_config_new failed");

	// Simulate a live generation holding both fakes: a manual registry
	// reference, exactly like an install would take.
	struct cp_module_registry reg;
	int reg_init_rc =
		cp_module_registry_init(&agent->memory_context, &reg, &err);
	free_err_on_failure(reg_init_rc != TEST_SUCCESS, &err);
	TEST_ASSERT_SUCCESS(reg_init_rc, "cp_module_registry_init failed");
	int decap_upsert_rc = cp_module_registry_upsert(
		&reg, "decap", "fake-decap", decap_fake, &err
	);
	free_err_on_failure(decap_upsert_rc != TEST_SUCCESS, &err);
	TEST_ASSERT_SUCCESS(
		decap_upsert_rc, "cp_module_registry_upsert failed"
	);
	int forward_upsert_rc = cp_module_registry_upsert(
		&reg, "forward", "fake-forward", forward_fake, &err
	);
	free_err_on_failure(forward_upsert_rc != TEST_SUCCESS, &err);
	TEST_ASSERT_SUCCESS(
		forward_upsert_rc, "cp_module_registry_upsert failed"
	);

	// Release each creator's own reference, exactly as a module's real
	// public free does.
	//
	// The generation reference above still holds both fakes alive, so
	// nothing parks yet.
	cp_module_release(decap_fake);
	cp_module_release(forward_fake);
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->parked_modules),
		"nothing must be parked while a generation still holds the "
		"configuration"
	);

	// Retire the generation: its last reference on each fake drops and
	// both park.
	cp_module_registry_fini(&reg);
	TEST_ASSERT_NOT_NULL(
		ADDR_OF(&agent->parked_modules),
		"both fakes must be parked once their generation retires"
	);

	// Trigger the drain via a decap construction, which shares only the
	// first fake's type.
	struct cp_module *trigger = fake_module_config_new(
		agent,
		"decap",
		"fake-trigger",
		3,
		fake_module_config_destroy_a,
		&err
	);
	free_err_on_failure(trigger == NULL, &err);
	TEST_ASSERT_NOT_NULL(trigger, "fake_module_config_new failed");

	TEST_ASSERT_NULL(
		ADDR_OF(&agent->parked_modules),
		"a construction of either parked type must drain the whole list"
	);
	TEST_ASSERT_EQUAL(
		(long)fake_destroy_a_count,
		1L,
		"the decap fake's own teardown must have run exactly once"
	);
	TEST_ASSERT_EQUAL(
		(long)fake_destroy_b_count,
		1L,
		"the forward fake's own teardown must have run exactly once"
	);
	TEST_ASSERT(
		fake_destroy_a_ids[0] == 1,
		"the decap fake's own teardown must have destroyed the decap "
		"fake"
	);
	TEST_ASSERT(
		fake_destroy_b_ids[0] == 2,
		"the forward fake's own teardown must have destroyed the "
		"forward "
		"fake"
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
	free_err_on_failure(agent == NULL, &err);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_module *decap_old =
		decap_module_config_new(agent, "idem-old", &err);
	free_err_on_failure(decap_old == NULL, &err);
	TEST_ASSERT_NOT_NULL(decap_old, "decap_module_config_new failed");
	struct cp_module *decap_new =
		decap_module_config_new(agent, "idem-new", &err);
	free_err_on_failure(decap_new == NULL, &err);
	TEST_ASSERT_NOT_NULL(decap_new, "decap_module_config_new failed");

	size_t checkpoint = block_allocator_free_size(&agent->block_allocator);

	// Park the older module first, then the newer one on top of it: head
	// decap_new, tail decap_old self-referential. Each is freed through
	// the real public free a decap control plane calls, dropping the
	// creator's only reference straight to zero — the module's real
	// public free parks rather than destroys.
	decap_module_config_free(decap_old);
	decap_module_config_free(decap_new);
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

	// A later decap construction reclaims every parked entry, decap_old
	// and decap_new alike.
	struct cp_module *decap_next =
		decap_module_config_new(agent, "idem-next", &err);
	free_err_on_failure(decap_next == NULL, &err);
	TEST_ASSERT_NOT_NULL(decap_next, "decap_module_config_new failed");
	TEST_ASSERT_NULL(
		ADDR_OF(&agent->parked_modules),
		"construction must reclaim every parked module"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Whether the process identified by pid is confirmed to no longer exist.
static int
process_is_dead(pid_t pid) {
	return pid > 0 && kill(pid, 0) == -1 && errno == ESRCH;
}

// The reclaim guard ignores a parked-teardown pin once its owning process
// is confirmed dead, but still honours one owned by a running process.
//
// Neither agent here ever actually parks a real module: the guard's
// decision depends only on the pin count and the recorded pid, so setting
// those fields directly is enough to drive it. Reuses agent_attach's own
// internal reclaim pass — every attach under a name already in use retires
// its predecessor the same way a real update would.
static int
test_pin_staleness_governs_reclaim(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	// A pid guaranteed to no longer exist: fork a child, let it exit
	// immediately, and reap it so its pid cannot still be running.
	pid_t dead_pid = fork();
	TEST_ASSERT(dead_pid >= 0, "fork failed");
	if (dead_pid == 0) {
		_exit(0);
	}
	int wstatus = 0;
	TEST_ASSERT(
		waitpid(dead_pid, &wstatus, 0) == dead_pid, "waitpid failed"
	);
	TEST_ASSERT(
		process_is_dead(dead_pid),
		"the reaped child's pid must read as dead before the test "
		"proceeds"
	);

	struct agent *dead_owner_v1 = agent_attach(
		shm, 0, "parked-pin-dead-owner", PARKED_TEST_MEMORY_LIMIT, &err
	);
	free_err_on_failure(dead_owner_v1 == NULL, &err);
	TEST_ASSERT_NOT_NULL(dead_owner_v1, "agent_attach failed");
	dead_owner_v1->pid = dead_pid;
	dead_owner_v1->parked_teardown_count = 1;

	struct agent *dead_owner_v2 = agent_attach(
		shm, 0, "parked-pin-dead-owner", PARKED_TEST_MEMORY_LIMIT, &err
	);
	free_err_on_failure(dead_owner_v2 == NULL, &err);
	TEST_ASSERT_NOT_NULL(dead_owner_v2, "agent_attach failed");
	TEST_ASSERT_NULL(
		ADDR_OF(&dead_owner_v2->prev),
		"a pin whose owning process is confirmed dead must not block "
		"reclaim"
	);

	struct agent *live_owner_v1 = agent_attach(
		shm, 0, "parked-pin-live-owner", PARKED_TEST_MEMORY_LIMIT, &err
	);
	free_err_on_failure(live_owner_v1 == NULL, &err);
	TEST_ASSERT_NOT_NULL(live_owner_v1, "agent_attach failed");
	live_owner_v1->pid = getpid();
	live_owner_v1->parked_teardown_count = 1;

	struct agent *live_owner_v2 = agent_attach(
		shm, 0, "parked-pin-live-owner", PARKED_TEST_MEMORY_LIMIT, &err
	);
	free_err_on_failure(live_owner_v2 == NULL, &err);
	TEST_ASSERT_NOT_NULL(live_owner_v2, "agent_attach failed");
	TEST_ASSERT(
		ADDR_OF(&live_owner_v2->prev) == live_owner_v1,
		"a pin whose owning process is still running must block reclaim"
	);

	// Clear the fabricated pin so nothing downstream mistakes this agent
	// for one with a real teardown still in flight.
	live_owner_v1->parked_teardown_count = 0;

	agent_detach(dead_owner_v2);
	agent_detach(live_owner_v2);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *mods_to_load[] = {"decap", "forward"};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 26,
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

	int res = test_drain_reclaims_every_type_via_own_teardown(shm);
	if (res == TEST_SUCCESS) {
		res = test_park_is_idempotent(shm);
	}
	if (res == TEST_SUCCESS) {
		res = test_pin_staleness_governs_reclaim(shm);
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
