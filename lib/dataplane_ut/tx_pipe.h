#pragma once

#include <stddef.h>

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

// Push mbuf onto the fixture's pipe via worker_tx_pipe_push.
//
// Returns 0 on success, -1 if the pipe or its deferred-free ring is full.
int
dataplane_ut_tx_pipe_push(
	struct dataplane_ut_tx_pipe *fixture, struct rte_mbuf *mbuf
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

// Reclaim the fixture's pending ring via worker_tx_pipe_reclaim.
void
dataplane_ut_tx_pipe_reclaim(struct dataplane_ut_tx_pipe *fixture);

// Number of mock-pool objects currently allocated (dequeued but not yet
// returned).
size_t
dataplane_ut_tx_pipe_outstanding(struct dataplane_ut_tx_pipe *fixture);
