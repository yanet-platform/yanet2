// Verifies that pending_ring_push enforces the ring-capacity guard and that
// the guard expression is correct (stop - start, not the dead stop - stop).
//
// This test must FAIL against the buggy guard (stop - stop >= num_slots always
// evaluates to 0 >= N which is never true, so the push never returns -1 and the
// ring grows unbounded) and PASS with the fix (stop - start >= num_slots).
//
// The test exercises pending_ring_push directly — no DPDK, no worker setup.
// struct rte_mbuf is used only as an opaque pointer value (never dereferenced).

#include "pending_ring.h"

#include <assert.h>
#include <stdint.h>
#include <string.h>

// Verifies that push into an empty ring succeeds and advances stop.
static void
test_push_succeeds_when_ring_has_space(void) {
	struct worker_pending_mbuf ring[4];
	memset(ring, 0, sizeof(ring));
	uint64_t start = 0, stop = 0;

	// Use arbitrary non-NULL addresses as opaque mbuf pointers.
	struct rte_mbuf *fake = (struct rte_mbuf *)(uintptr_t)0xDEAD0001;

	int rc = pending_ring_push(ring, &start, &stop, 4, fake, 1);
	assert(rc == 0);
	assert(stop == 1);
	assert(start == 0);
	assert(ring[0].mbuf == fake);
	assert(ring[0].ref_cnt == 1);
}

// Verifies that push is rejected (returns -1) when depth == num_slots.
//
// With the dead guard (stop - stop) this would return 0 instead of -1, so this
// test catches the bug directly: if the guard is broken the push succeeds,
// writes into the aliased slot, and the assertion on the return value fails.
static void
test_push_rejected_at_capacity(void) {
	const uint32_t N = 4;
	struct worker_pending_mbuf ring[N];
	memset(ring, 0, sizeof(ring));
	uint64_t start = 0, stop = 0;

	// Fill the ring to capacity.
	for (uint32_t idx = 0; idx < N; ++idx) {
		struct rte_mbuf *fake =
			(struct rte_mbuf *)(uintptr_t)(0xBEEF0001 + idx);
		int rc = pending_ring_push(ring, &start, &stop, N, fake, 1);
		assert(rc == 0);
	}
	assert(stop - start == N);

	// The next push must be rejected — ring is full.
	struct rte_mbuf *overflow_mbuf =
		(struct rte_mbuf *)(uintptr_t)0xBAD00000;
	int rc = pending_ring_push(ring, &start, &stop, N, overflow_mbuf, 1);
	assert(rc == -1);

	// stop must not have advanced.
	assert(stop - start == N);

	// The slot that would have been aliased must still hold the original
	// mbuf, not the overflow pointer.
	uint32_t alias_slot = (uint32_t)(stop % N);
	assert(ring[alias_slot].mbuf != overflow_mbuf);
}

// Verifies that depth never exceeds num_slots when the guard is correct,
// simulating the lag scenario described in the bug report (consumer slow,
// producer fast).
static void
test_depth_invariant_under_consumer_lag(void) {
	const uint32_t N = 8;
	struct worker_pending_mbuf ring[N];
	memset(ring, 0, sizeof(ring));
	uint64_t start = 0, stop = 0;

	// Run 50 rounds: push 10 mbufs, consumer advances start by only 3.
	for (int round = 0; round < 50; ++round) {
		for (int b = 0; b < 10; ++b) {
			struct rte_mbuf *fake =
				(struct rte_mbuf *)(uintptr_t)(0x1000 +
							       round * 10 + b);
			// Push may fail once ring is full — that is the correct
			// behaviour.
			pending_ring_push(ring, &start, &stop, N, fake, 1);
		}
		// Consumer advances slowly.
		uint32_t drain =
			(stop - start > 3) ? 3 : (uint32_t)(stop - start);
		start += drain;

		// Invariant: depth must never exceed N.
		assert(stop - start <= N);
	}
}

// Verifies that the ring slots are written in order modulo num_slots and wrap
// correctly when start and stop have advanced past the ring size.
static void
test_ring_wrap_writes_correct_slot(void) {
	const uint32_t N = 4;
	struct worker_pending_mbuf ring[N];
	memset(ring, 0, sizeof(ring));

	// Start with non-zero counters to exercise the modulo wrap.
	uint64_t start = 100, stop = 100;

	struct rte_mbuf *a = (struct rte_mbuf *)(uintptr_t)0xAAAA;
	struct rte_mbuf *b = (struct rte_mbuf *)(uintptr_t)0xBBBB;
	struct rte_mbuf *c = (struct rte_mbuf *)(uintptr_t)0xCCCC;

	assert(pending_ring_push(ring, &start, &stop, N, a, 10) == 0);
	assert(ring[100 % N].mbuf == a);

	assert(pending_ring_push(ring, &start, &stop, N, b, 20) == 0);
	assert(ring[101 % N].mbuf == b);

	assert(pending_ring_push(ring, &start, &stop, N, c, 30) == 0);
	assert(ring[102 % N].mbuf == c);
}

int
main(void) {
	test_push_succeeds_when_ring_has_space();
	test_push_rejected_at_capacity();
	test_depth_invariant_under_consumer_lag();
	test_ring_wrap_writes_correct_slot();
	return 0;
}
