#pragma once

#include <stddef.h>
#include <stdint.h>

struct dataplane_ut_tx_pipe;
struct rte_mbuf;

// Construct a fixture owning one worker_tx_pipe and a mock mempool.
//
// Returns NULL on allocation failure. The caller releases it with
// dataplane_ut_tx_pipe_free.
struct dataplane_ut_tx_pipe *
dataplane_ut_tx_pipe_new(void);

// Tear down a fixture previously returned by dataplane_ut_tx_pipe_new.
// NULL-safe.
void
dataplane_ut_tx_pipe_free(struct dataplane_ut_tx_pipe *fixture);

// Allocate a chained mbuf of seg_count segments from the fixture's mock
// pool. seg_count must be at least 1. Returns NULL on exhaustion.
struct rte_mbuf *
dataplane_ut_tx_pipe_alloc_mbuf(
	struct dataplane_ut_tx_pipe *fixture, size_t seg_count
);

// Push one mbuf onto the fixture's pipe as a single-element batch.
//
// Returns 0 on success, -1 if the pipe or its deferred-free ring is full.
int
dataplane_ut_tx_pipe_push(
	struct dataplane_ut_tx_pipe *fixture, struct rte_mbuf *mbuf
);

// Stage one mbuf on the fixture's pipe via worker_tx_pipe_stage.
//
// Returns 0 when staged, -1 when the batch is already full.
int
dataplane_ut_tx_pipe_stage(
	struct dataplane_ut_tx_pipe *fixture, struct rte_mbuf *mbuf
);

// Flush the fixture's staged batch via worker_tx_pipe_flush.
//
// Returns how many the pipe accepted and fills rejected, which must have
// room for a whole batch.
size_t
dataplane_ut_tx_pipe_flush(
	struct dataplane_ut_tx_pipe *fixture,
	struct rte_mbuf **rejected,
	size_t *rejected_count
);

// Push a batch onto the fixture's pipe via worker_tx_pipe_push_bulk.
//
// Returns how many were accepted; the rest stay the caller's.
size_t
dataplane_ut_tx_pipe_push_bulk(
	struct dataplane_ut_tx_pipe *fixture,
	struct rte_mbuf **mbufs,
	size_t count,
	struct rte_mbuf **rejected,
	size_t *rejected_count
);

// Drain the fixture's pipe via worker_tx_pipe_drain, transmitting only the
// first `accept` mbufs of each popped burst.
//
// The remainder is dropped by the production drain logic, exercising the
// same rejected-tail path a short real NIC tx_burst would take.
void
dataplane_ut_tx_pipe_drain(struct dataplane_ut_tx_pipe *fixture, size_t accept);

// Simulate the NIC completing transmission of an mbuf a prior drain
// accepted, releasing it exactly as a real driver's tx-descriptor reclaim
// would once DMA finishes.
void
dataplane_ut_tx_pipe_complete(struct rte_mbuf *mbuf);

// Return the mbuf idx segments below mbuf (0 is mbuf itself), or NULL
// past the end of the chain.
struct rte_mbuf *
dataplane_ut_tx_pipe_segment(struct rte_mbuf *mbuf, size_t idx);

// Read one segment's current DPDK refcount, e.g. to check a rejected
// push restored it to its pre-push value.
uint16_t
dataplane_ut_tx_pipe_segment_refcnt(struct rte_mbuf *segment);

// Add delta to one segment's refcount directly, bypassing the pipe.
//
// A positive delta manufactures the mismatched-refcount chain that
// dataplane_ut_tx_pipe_push rejects. A matching negative delta restores
// uniformity so the chain can drain through the normal path afterward.
void
dataplane_ut_tx_pipe_segment_refcnt_add(
	struct rte_mbuf *segment, int16_t delta
);

// Release exactly one segment's consumer-side reference — the
// per-segment counterpart to dataplane_ut_tx_pipe_complete.
//
// Every segment must be released exactly once, via this call or
// dataplane_ut_tx_pipe_complete but never both: mixing them, or
// calling either twice on one segment, double-frees it.
void
dataplane_ut_tx_pipe_complete_segment(struct rte_mbuf *segment);

// Reclaim the fixture's pending ring via worker_tx_pipe_reclaim.
void
dataplane_ut_tx_pipe_reclaim(struct dataplane_ut_tx_pipe *fixture);

// Number of mock-pool objects currently allocated (dequeued but not yet
// returned).
size_t
dataplane_ut_tx_pipe_outstanding(struct dataplane_ut_tx_pipe *fixture);
