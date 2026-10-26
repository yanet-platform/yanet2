/*
 * Verifies that ring scenarios release their data on success and assertion
 * failure.
 *
 * The original scenario entrypoints run with local allocation tracking and
 * faults that reach their return-style assertions without breaking invariants.
 */

#include "common/test_assert.h"

#include "common/ring.h"

#include "lib/logging/log.h"

#include <errno.h>
#include <stdatomic.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

enum fixture_fault {
	FIXTURE_NO_FAULT,
	FIXTURE_REJECT_SPLIT_PREPARE,
	FIXTURE_EVICT_AFTER_FOURTH_PUBLISH,
	FIXTURE_CHANGE_UNDERSIZE_ERRNO,
};

struct allocation_record {
	uintptr_t identity;
	size_t releases;
};

static struct {
	struct allocation_record records[8];
	size_t acquired;
	size_t release_calls;
	size_t unowned_releases;
	enum fixture_fault fault;
	unsigned int publications;
	bool fault_reached;
} probe;

// Record integer identities so ownership checks never read a freed pointer.
static void *
fixture_calloc(size_t count, size_t size) {
	if (probe.acquired ==
	    sizeof(probe.records) / sizeof(probe.records[0])) {
		LOG(ERROR, "too many fixture allocations to track");
		abort();
	}
	void *data = calloc(count, size);
	if (data != NULL) {
		probe.records[probe.acquired++].identity = (uintptr_t)data;
	}
	return data;
}

// Count every release attempt and pass each owned allocation to the real free.
static void
fixture_free(void *data) {
	if (data == NULL) {
		return;
	}
	++probe.release_calls;
	uintptr_t identity = (uintptr_t)data;
	for (size_t i = probe.acquired; i > 0; --i) {
		struct allocation_record *record = &probe.records[i - 1];
		if (record->identity == identity) {
			if (++record->releases == 1) {
				free(data);
			}
			return;
		}
	}
	++probe.unowned_releases;
}

// Reach the prepare or errno assertion while retaining the real ring checks.
static int
fixture_prepare(struct ring_worker *ring, uint8_t *data, uint32_t total_len) {
	if (probe.fault == FIXTURE_REJECT_SPLIT_PREPARE) {
		probe.fault_reached = true;
		errno = EIO;
		return -1;
	}
	int result = ring_worker_prepare(ring, data, total_len);
	if (probe.fault == FIXTURE_CHANGE_UNDERSIZE_ERRNO && result == -1 &&
	    total_len == RING_RECORD_FRAME_SIZE - 1) {
		probe.fault_reached = true;
		errno = EIO;
	}
	return result;
}

// Keep all cursor and record-boundary invariants valid before the assertion.
static void
fixture_publish(struct ring_worker *ring) {
	ring_worker_publish(ring);
	if (probe.fault == FIXTURE_EVICT_AFTER_FOURTH_PUBLISH &&
	    ++probe.publications == 4) {
		ring_worker_set_positions(ring, ring->local.write_idx, 16);
		probe.fault_reached = true;
	}
}

static int
run_ring_wrap_splits_header_test(void);
static int
run_ring_overwrite_evicts_whole_records_test(void);
static int
run_ring_prepare_rejects_undersize_test(void);

// Dependencies are already included so these redirects affect scenarios only.
#define calloc fixture_calloc
#define free fixture_free
#define ring_worker_prepare fixture_prepare
#define ring_worker_publish fixture_publish
#define main ring_scenarios_main
#include "ring_test.c"
#undef main
#undef ring_worker_publish
#undef ring_worker_prepare
#undef free
#undef calloc

struct fixture_case {
	const char *name;
	int (*scenario)(void);
	enum fixture_fault fault;
	int expected_result;
};

// Record ownership failures before reclaiming leftover fixture storage.
static int
run_fixture_ownership_test(const struct fixture_case *test) {
	memset(&probe, 0, sizeof(probe));
	probe.fault = test->fault;
	int result = test->scenario();
	bool passed = true;
	if (result != test->expected_result) {
		LOG(ERROR,
		    "%s: scenario returned %d, expected %d",
		    test->name,
		    result,
		    test->expected_result);
		passed = false;
	}
	if (probe.fault_reached != (test->fault != FIXTURE_NO_FAULT)) {
		LOG(ERROR, "%s: assertion fault was not reached", test->name);
		passed = false;
	}
	if (probe.acquired != 1 || probe.release_calls != 1 ||
	    probe.unowned_releases != 0) {
		LOG(ERROR,
		    "%s: acquired %zu, release calls %zu, unowned releases %zu",
		    test->name,
		    probe.acquired,
		    probe.release_calls,
		    probe.unowned_releases);
		passed = false;
	}
	for (size_t i = 0; i < probe.acquired; ++i) {
		struct allocation_record *record = &probe.records[i];
		if (record->releases != 1) {
			LOG(ERROR,
			    "%s: allocation %zu was released %zu times",
			    test->name,
			    i,
			    record->releases);
			passed = false;
		}
		if (record->releases == 0) {
			free((void *)record->identity);
		}
	}
	return passed ? TEST_SUCCESS : TEST_FAILED;
}

int
main(void) {
	log_enable_name("debug");
	const struct fixture_case cases[] = {
		{
			.name = "wrap_splits_header_success_releases_data",
			.scenario = run_ring_wrap_splits_header_test,
			.fault = FIXTURE_NO_FAULT,
			.expected_result = TEST_SUCCESS,
		},
		{
			.name = "split_prepare_rejection_releases_data",
			.scenario = run_ring_wrap_splits_header_test,
			.fault = FIXTURE_REJECT_SPLIT_PREPARE,
			.expected_result = TEST_FAILED,
		},
		{
			.name = "overwrite_evicts_whole_records_success_"
				"releases_"
				"data",
			.scenario =
				run_ring_overwrite_evicts_whole_records_test,
			.fault = FIXTURE_NO_FAULT,
			.expected_result = TEST_SUCCESS,
		},
		{
			.name = "premature_eviction_releases_data",
			.scenario =
				run_ring_overwrite_evicts_whole_records_test,
			.fault = FIXTURE_EVICT_AFTER_FOURTH_PUBLISH,
			.expected_result = TEST_FAILED,
		},
		{
			.name = "prepare_rejects_undersize_success_releases_"
				"data",
			.scenario = run_ring_prepare_rejects_undersize_test,
			.fault = FIXTURE_NO_FAULT,
			.expected_result = TEST_SUCCESS,
		},
		{
			.name = "wrong_undersize_errno_releases_data",
			.scenario = run_ring_prepare_rejects_undersize_test,
			.fault = FIXTURE_CHANGE_UNDERSIZE_ERRNO,
			.expected_result = TEST_FAILED,
		},
	};
	int failed = 0;
	for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); ++i) {
		if (run_fixture_ownership_test(&cases[i]) != TEST_SUCCESS) {
			LOG(ERROR, "%s failed", cases[i].name);
			failed = 1;
			continue;
		}
		LOG(INFO, "%s passed", cases[i].name);
	}
	return failed;
}
