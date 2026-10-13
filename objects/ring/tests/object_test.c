/*
 * Lifecycle tests for the ring shared object.
 *
 * They cover the checked allocation, parameter checks, refusal to free a
 * referenced object, and rollback on failure. Bad parameters are refused
 * and the arena stays unchanged. The object has one ring per dataplane
 * worker. While a generation references the object, a free is refused, the
 * same as for every other shared object.
 */

#include "api/agent.h"

#include "common/memory.h"
#include "common/memory_block.h"
#include "common/numutils.h"
#include "common/test_assert.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_object.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"

#include "objects/ring/api/ring_object.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "lib/logging/log.h"

#include <errno.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#define RING_OBJECT_TEST_MEMORY_LIMIT (2u * 1024u * 1024u)

// The aligned allocation rounds the array start and each entry size up.
//
// Both go up to the requested alignment, and the matching free restores the
// arena. A buddy block is always aligned to at least its own size. So only
// the ASan red zone can shift the raw block off the alignment. The checks
// hold in both cases.
static int
run_ring_object_align_alloc_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "ring-align-alloc", RING_OBJECT_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	uint64_t alignments[] = {64, 128};
	for (size_t i = 0; i < sizeof(alignments) / sizeof(alignments[0]);
	     ++i) {
		uint64_t alignment = alignments[i];
		size_t baseline =
			block_allocator_free_size(&agent->block_allocator);

		uint64_t stride = 100;
		uint64_t count = 3;
		void *raw;
		uint64_t raw_size;
		void *result = ring_object_align_alloc(
			&agent->memory_context,
			stride,
			count,
			alignment,
			&raw,
			&raw_size
		);
		TEST_ASSERT_NOT_NULL(
			result,
			"align_alloc must succeed at alignment %lu",
			alignment
		);
		TEST_ASSERT_EQUAL(
			(uintptr_t)result % alignment,
			0,
			"the array base must be aligned to %lu",
			alignment
		);

		uint64_t expected_stride =
			(stride + alignment - 1) & ~(alignment - 1);
		uint64_t expected_body = expected_stride * count;
		TEST_ASSERT(
			raw_size >= expected_body &&
				raw_size < expected_body + alignment,
			"raw_size must cover exactly the aligned-stride body "
			"plus "
			"at most one alignment unit of slack"
		);
		TEST_ASSERT(
			(uintptr_t)result - (uintptr_t)raw < alignment,
			"the base must round up by less than one alignment unit"
		);

		memory_bfree(&agent->memory_context, raw, raw_size);
		TEST_ASSERT_EQUAL(
			(long)block_allocator_free_size(&agent->block_allocator
			),
			(long)baseline,
			"freeing the raw allocation must restore the arena"
		);
	}

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Smallest power of two above the allocator's largest block.
//
// It is a too-big capacity that is still a power of two. This holds with
// or without ASan, whose red zones lower the largest block.
static uint32_t
oversize_capacity(void) {
	uint64_t capacity = next_power_of_two(
		(uint64_t)MEMORY_BLOCK_ALLOCATOR_MAX_SIZE + 1
	);
	return (uint32_t)capacity;
}

// Create refuses bad parameters and leaves the arena unchanged.
//
// A bad capacity is zero, below the frame size, not a power of two, or above
// the allocator's largest block. A bad publish batch is zero or above the
// maximum. The errno tells which check failed. The error is an invalid
// argument, which the service reports as InvalidArgument.
static int
run_ring_object_bad_parameters_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "ring-bad-capacity", RING_OBJECT_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	const uint32_t batch = RING_PUBLISH_BATCH_DEFAULT;
	struct {
		uint32_t capacity;
		uint32_t publish_batch;
		int expected_errno;
	} bad_params[] = {
		{0, batch, EINVAL},
		{4, batch, EINVAL},
		{24, batch, EINVAL},
		{oversize_capacity(), batch, E2BIG},
		{64, 0, EINVAL},
		{64, RING_PUBLISH_BATCH_MAX + 1, EINVAL},
	};
	for (size_t i = 0; i < sizeof(bad_params) / sizeof(bad_params[0]);
	     ++i) {
		uint32_t capacity = bad_params[i].capacity;
		uint32_t publish_batch = bad_params[i].publish_batch;
		size_t baseline =
			block_allocator_free_size(&agent->block_allocator);

		yanet_error *create_err = NULL;
		struct cp_object *object = ring_object_config_new(
			agent,
			"bad-capacity",
			capacity,
			publish_batch,
			&create_err
		);
		TEST_ASSERT_NULL(
			object,
			"capacity %u, publish batch %u must be refused",
			capacity,
			publish_batch
		);
		TEST_ASSERT_EQUAL(
			errno,
			bad_params[i].expected_errno,
			"capacity %u, publish batch %u must set the expected "
			"errno",
			capacity,
			publish_batch
		);
		TEST_ASSERT_EQUAL(
			yanet_error_kind(create_err),
			YANET_ERROR_INVALID_ARGUMENT,
			"capacity %u, publish batch %u must be an invalid "
			"argument",
			capacity,
			publish_batch
		);
		yanet_error_free(create_err);

		TEST_ASSERT_EQUAL(
			(long)block_allocator_free_size(&agent->block_allocator
			),
			(long)baseline,
			"a refused create must leave the arena unchanged: "
			"capacity=%u publish_batch=%u",
			capacity,
			publish_batch
		);
	}

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Config new refuses an empty name or one the name buffer would cut.
//
// A cut name could match another ring's name. The refusal sets EINVAL and
// leaves the arena unchanged.
static int
run_ring_object_bad_name_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "ring-bad-name", RING_OBJECT_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	char overlong[CP_OBJECT_NAME_LEN + 1];
	memset(overlong, 'r', CP_OBJECT_NAME_LEN);
	overlong[CP_OBJECT_NAME_LEN] = '\0';

	const char *bad_names[] = {"", overlong};
	for (size_t i = 0; i < sizeof(bad_names) / sizeof(bad_names[0]); ++i) {
		size_t len = strlen(bad_names[i]);
		size_t baseline =
			block_allocator_free_size(&agent->block_allocator);

		yanet_error *create_err = NULL;
		struct cp_object *object = ring_object_config_new(
			agent,
			bad_names[i],
			64,
			RING_PUBLISH_BATCH_DEFAULT,
			&create_err
		);
		TEST_ASSERT_NULL(
			object, "a name of %zu bytes must be refused", len
		);
		TEST_ASSERT_EQUAL(
			errno,
			EINVAL,
			"a name of %zu bytes must set EINVAL",
			len
		);
		TEST_ASSERT(
			create_err != NULL,
			"a refused name must report an error"
		);
		yanet_error_free(create_err);

		TEST_ASSERT_EQUAL(
			(long)block_allocator_free_size(&agent->block_allocator
			),
			(long)baseline,
			"a refused name must leave the arena unchanged: "
			"len=%zu",
			len
		);
	}

	agent_detach(agent);
	return TEST_SUCCESS;
}

// Create refuses a dataplane with zero workers or with UINT16_MAX or more.
//
// It refuses before it allocates anything, so the worker count is never
// truncated to 16 bits. The harness runs two workers. For each probe the test
// replaces the dataplane's worker count, then restores it.
static int
run_ring_object_bad_worker_count_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "ring-bad-workers", RING_OBJECT_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct dp_config *dp_config = agent_dp_config(agent);
	uint64_t saved_worker_count = dp_config->worker_count;

	struct {
		uint64_t worker_count;
		int expected_errno;
	} bad_counts[] = {
		{0, EINVAL},
		{UINT16_MAX, E2BIG},
	};
	int res = TEST_SUCCESS;
	for (size_t i = 0; i < sizeof(bad_counts) / sizeof(bad_counts[0]);
	     ++i) {
		uint64_t worker_count = bad_counts[i].worker_count;
		size_t baseline =
			block_allocator_free_size(&agent->block_allocator);

		dp_config->worker_count = worker_count;
		yanet_error *create_err = NULL;
		struct cp_object *object = ring_object_config_new(
			agent,
			"bad-workers",
			64,
			RING_PUBLISH_BATCH_DEFAULT,
			&create_err
		);
		int create_errno = errno;
		dp_config->worker_count = saved_worker_count;
		yanet_error_free(create_err);

		if (object != NULL) {
			LOG(ERROR,
			    "worker count %lu must be refused",
			    (unsigned long)worker_count);
			yanet_error *free_err = NULL;
			ring_object_config_free(object, &free_err);
			yanet_error_free(free_err);
			res = TEST_FAILED;
			break;
		}
		if (create_errno != bad_counts[i].expected_errno) {
			LOG(ERROR,
			    "worker count %lu: errno %d, expected %d",
			    (unsigned long)worker_count,
			    create_errno,
			    bad_counts[i].expected_errno);
			res = TEST_FAILED;
			break;
		}
		if (block_allocator_free_size(&agent->block_allocator) !=
		    baseline) {
			LOG(ERROR,
			    "worker count %lu: a refused create must leave "
			    "the arena unchanged",
			    (unsigned long)worker_count);
			res = TEST_FAILED;
			break;
		}
	}

	agent_detach(agent);
	return res;
}

// The created object has one ring per dataplane worker.
//
// Each ring has its own metadata and data area. An index past the last
// worker gives no ring.
static int
run_ring_object_worker_rings_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "ring-worker-rings", RING_OBJECT_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	uint64_t worker_count = agent_dp_config(agent)->worker_count;
	TEST_ASSERT_EQUAL(
		(long)worker_count, 2L, "the harness must run two workers"
	);

	const uint32_t publish_batch = 32;
	struct cp_object *object = ring_object_config_new(
		agent, "worker-rings", 64, publish_batch, &err
	);
	TEST_ASSERT_NOT_NULL(
		object,
		"ring_object_config_new failed: %s",
		err ? yanet_error_message(err) : "?"
	);
	TEST_ASSERT_EQUAL(
		ring_object_capacity(object),
		64,
		"the object's capacity must stick"
	);
	TEST_ASSERT_EQUAL(
		ring_object_publish_batch(object),
		publish_batch,
		"the object's publish batch must stick"
	);

	for (uint64_t idx = 0; idx < worker_count; ++idx) {
		struct ring_worker *worker = ring_object_worker(object, idx);
		uint8_t *data = ring_object_worker_data(object, idx);
		TEST_ASSERT_NOT_NULL(
			worker, "worker %lu has no ring", (unsigned long)idx
		);
		TEST_ASSERT_NOT_NULL(
			data, "worker %lu has no data", (unsigned long)idx
		);
		TEST_ASSERT_EQUAL(
			worker->local.publish_batch,
			publish_batch,
			"worker %lu must take the object's publish batch",
			(unsigned long)idx
		);

		struct ring_worker_view view;
		TEST_ASSERT(
			ring_object_worker_view(object, idx, &view),
			"worker %lu has no reader view",
			(unsigned long)idx
		);
		uint64_t *write_idx = (uint64_t *)&worker->published.write_idx;
		uint64_t *readable_idx =
			(uint64_t *)&worker->published.readable_idx;
		TEST_ASSERT(
			view.write_idx == write_idx &&
				view.readable_idx == readable_idx,
			"worker %lu view must point at the published positions",
			(unsigned long)idx
		);
		TEST_ASSERT(
			view.data == data && view.size == 64 && view.mask == 63,
			"worker %lu view must carry its data area and size",
			(unsigned long)idx
		);

		for (uint64_t prev = 0; prev < idx; ++prev) {
			TEST_ASSERT(
				ring_object_worker(object, prev) != worker,
				"workers %lu and %lu share metadata",
				(unsigned long)prev,
				(unsigned long)idx
			);
			TEST_ASSERT(
				ring_object_worker_data(object, prev) != data,
				"workers %lu and %lu share a data area",
				(unsigned long)prev,
				(unsigned long)idx
			);
		}
	}
	TEST_ASSERT_NULL(
		ring_object_worker(object, worker_count),
		"no ring may resolve past the last worker"
	);
	TEST_ASSERT_NULL(
		ring_object_worker_data(object, worker_count),
		"no data area may resolve past the last worker"
	);
	struct ring_worker_view past_view;
	TEST_ASSERT(
		!ring_object_worker_view(object, worker_count, &past_view),
		"no reader view may resolve past the last worker"
	);

	yanet_error *free_err = NULL;
	TEST_ASSERT_SUCCESS(
		ring_object_config_free(object, &free_err),
		"freeing a dangling ring object must succeed"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// While a generation references the object, its free fails with EAGAIN.
//
// The object's memory stays in place. Once the reference is gone, the same
// free succeeds.
static int
run_ring_object_free_refused_while_referenced_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "ring-free-refused", RING_OBJECT_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct cp_object *object = ring_object_config_new(
		agent, "referenced", 64, RING_PUBLISH_BATCH_DEFAULT, &err
	);
	TEST_ASSERT_NOT_NULL(
		object,
		"ring_object_config_new failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	struct cp_object_registry reg;
	TEST_ASSERT_SUCCESS(
		cp_object_registry_init(
			&agent->memory_context, NULL, &reg, &err
		),
		"cp_object_registry_init failed"
	);
	TEST_ASSERT_SUCCESS(
		cp_object_registry_upsert(
			&reg, RING_OBJECT_TYPE, "referenced", object, &err
		),
		"cp_object_registry_upsert failed"
	);

	yanet_error *free_err = NULL;
	TEST_ASSERT(
		ring_object_config_free(object, &free_err) == -1 &&
			errno == EAGAIN,
		"freeing a generation-referenced ring object must fail with "
		"EAGAIN"
	);
	yanet_error_free(free_err);
	TEST_ASSERT(
		block_allocator_free_size(&agent->block_allocator) < baseline,
		"a refused free must leave the object's memory in place"
	);

	cp_object_registry_fini(&reg);

	free_err = NULL;
	TEST_ASSERT_SUCCESS(
		ring_object_config_free(object, &free_err),
		"freeing a dangling ring object after its last reference "
		"retired must destroy it"
	);
	TEST_ASSERT_EQUAL(
		(long)block_allocator_free_size(&agent->block_allocator),
		(long)baseline,
		"destroying the object must return the arena to its baseline"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// A second fini after a full create does nothing.
//
// The first fini clears the fields it freed, so no memory is freed twice.
static int
run_ring_object_fini_idempotent_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "ring-fini-twice", RING_OBJECT_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	struct ring_object *self = ring_object_new(agent);
	TEST_ASSERT_NOT_NULL(self, "ring_object_new failed");
	TEST_ASSERT_SUCCESS(
		ring_object_init(self, agent, "fini-twice", &err),
		"ring_object_init failed"
	);
	TEST_ASSERT_SUCCESS(
		ring_object_create(self, 64, RING_PUBLISH_BATCH_DEFAULT, &err),
		"ring_object_create failed"
	);

	ring_object_fini(self);
	ring_object_fini(self);
	TEST_ASSERT_NULL(
		ADDR_OF(&self->workers), "fini must forget the workers array"
	);
	TEST_ASSERT_EQUAL(
		(long)self->worker_count, 0L, "fini must reset worker_count"
	);
	ring_object_free(self, agent);

	TEST_ASSERT_EQUAL(
		(long)block_allocator_free_size(&agent->block_allocator),
		(long)baseline,
		"a double fini must return the arena to exactly its baseline"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

// If memory runs out in the middle of the data areas, create frees them all.
//
// Create then fails with ENOMEM and the arena is back to its start state.
// The capacity where worker 0 still fits but worker 1 does not depends on
// the arena layout. So the test tries smaller and smaller powers of two. It
// stops at the first one whose error names worker 1.
static int
run_ring_object_enomem_rollback_test(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "ring-enomem", RING_OBJECT_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");
	TEST_ASSERT_EQUAL(
		(long)agent_dp_config(agent)->worker_count,
		2L,
		"the harness must run two workers"
	);

	size_t baseline = block_allocator_free_size(&agent->block_allocator);

	bool rolled_back_mid_allocation = false;
	for (uint32_t capacity = oversize_capacity() >> 1;
	     capacity >= RING_RECORD_FRAME_SIZE;
	     capacity >>= 1) {
		yanet_error *create_err = NULL;
		struct cp_object *object = ring_object_config_new(
			agent,
			"enomem",
			capacity,
			RING_PUBLISH_BATCH_DEFAULT,
			&create_err
		);
		if (object != NULL) {
			// Both workers fit, and every smaller capacity fits
			// too. There is nothing left to try.
			yanet_error *free_err = NULL;
			TEST_ASSERT_SUCCESS(
				ring_object_config_free(object, &free_err),
				"freeing a dangling ring object must succeed"
			);
			break;
		}
		int create_errno = errno;
		TEST_ASSERT_EQUAL(
			create_errno,
			ENOMEM,
			"capacity %u must fail only for lack of memory",
			capacity
		);
		TEST_ASSERT_EQUAL(
			(long)block_allocator_free_size(&agent->block_allocator
			),
			(long)baseline,
			"a failed create must restore the arena: capacity=%u",
			capacity
		);
		for (const yanet_error *cause = create_err; cause != NULL;
		     cause = yanet_error_cause(cause)) {
			const char *message = yanet_error_message(cause);
			if (message != NULL &&
			    strstr(message, "ring data for worker 1") != NULL) {
				rolled_back_mid_allocation = true;
			}
		}
		yanet_error_free(create_err);
		if (rolled_back_mid_allocation) {
			break;
		}
	}
	TEST_ASSERT(
		rolled_back_mid_allocation,
		"some capacity must fit worker 0 but not worker 1"
	);

	agent_detach(agent);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *objs_to_load[] = {RING_OBJECT_TYPE};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 26,
		.dp_memory = 1u << 20,
		.worker_count = 2,
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

	struct test_case {
		const char *name;
		int (*func)(struct yanet_shm *shm);
	};

	struct test_case cases[] = {
		{"align_alloc", run_ring_object_align_alloc_test},
		{"bad_parameters", run_ring_object_bad_parameters_test},
		{"bad_name", run_ring_object_bad_name_test},
		{"bad_worker_count", run_ring_object_bad_worker_count_test},
		{"worker_rings", run_ring_object_worker_rings_test},
		{"free_refused_while_referenced",
		 run_ring_object_free_refused_while_referenced_test},
		{"fini_idempotent", run_ring_object_fini_idempotent_test},
		{"enomem_rollback", run_ring_object_enomem_rollback_test},
	};

	// All cases share one harness. A failure stops the run, so later
	// cases do not see the state the failed case left behind.
	int res = TEST_SUCCESS;
	for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); ++i) {
		LOG(INFO, "%s running", cases[i].name);
		res = cases[i].func(shm);
		if (res != TEST_SUCCESS) {
			LOG(ERROR, "%s failed", cases[i].name);
			break;
		}
		LOG(INFO, "%s passed", cases[i].name);
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
