#pragma once

#include <stddef.h>
#include <stdint.h>

struct rte_mbuf;

// Test-only harness wrapping one worker_tx_pipe and its own mock mempool, so
// a Go test can drive the tx pipe producer/consumer paths directly without
// standing up a full dataplane_ut harness.
struct dataplane_ut_tx_pipe;

// Construct a harness: a mock mempool plus a worker_tx_pipe of
// (1 << pipe_size) capacity, per worker_tx_pipe_init().
//
// Returns NULL on allocation failure.
struct dataplane_ut_tx_pipe *
dataplane_ut_tx_pipe_new(size_t pipe_size);

// Tear down a harness previously returned by dataplane_ut_tx_pipe_new().
// NULL-safe.
void
dataplane_ut_tx_pipe_free(struct dataplane_ut_tx_pipe *harness);

// Allocate a chained mbuf of seg_count segments from the harness mempool,
// each segment holding seg_size bytes of payload.
//
// Returns NULL on allocation failure.
struct rte_mbuf *
dataplane_ut_tx_pipe_alloc_mbuf(
	struct dataplane_ut_tx_pipe *harness,
	uint32_t seg_count,
	uint32_t seg_size
);

// Push mbuf into the harness pipe (producer side). See worker_tx_pipe_push().
//
// Returns 0 on success, -1 when the pipe is full.
int
dataplane_ut_tx_pipe_push(
	struct dataplane_ut_tx_pipe *harness, struct rte_mbuf *mbuf
);

// Drain the harness pipe (consumer side). See worker_tx_pipe_drain().
//
// Accepts the first accept_count mbufs of each burst and frees the rest,
// mirroring a NIC tx_burst that only transmits part of a burst. Accepted
// mbufs are retained by the stub xmit, owned by the consumer, until
// dataplane_ut_tx_pipe_complete_tx() frees them the way a PMD would on tx
// completion.
//
// Returns the number of items drained.
size_t
dataplane_ut_tx_pipe_drain(
	struct dataplane_ut_tx_pipe *harness, uint16_t accept_count
);

// Free every mbuf accepted by the stub xmit across prior
// dataplane_ut_tx_pipe_drain() calls, modeling a NIC completing their tx.
void
dataplane_ut_tx_pipe_complete_tx(struct dataplane_ut_tx_pipe *harness);

// Reclaim pipe slots consumed by a prior drain (producer side). See
// worker_tx_pipe_reclaim().
void
dataplane_ut_tx_pipe_reclaim(struct dataplane_ut_tx_pipe *harness);

// Report the number of mbuf objects currently outstanding (dequeued from
// the harness mempool but not yet returned), so a test can assert exact
// balance after a push/drain/reclaim sequence.
uint64_t
dataplane_ut_tx_pipe_outstanding(struct dataplane_ut_tx_pipe *harness);
