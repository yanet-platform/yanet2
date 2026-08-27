#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "common/data_pipe.h"

// log2 of the per-connection SPSC data pipe capacity.
#define WORKER_TX_PIPE_SIZE 10

// How many mbufs one flush hands the pipe at most.
//
// Sized to the worker's own tx burst so a full burst bound for one pipe
// crosses to its consumer in a single ring round trip.
#define WORKER_TX_BATCH_SIZE 32

// The per-pipe deferred-free ring holds 2^(WORKER_TX_PIPE_SIZE + this)
// mbufs, sized above the pipe capacity to absorb consumer-side NIC tx
// backlog before backpressure drops further packets.
#define WORKER_TX_PIPE_PENDING_SHIFT 2

struct worker_pending_mbuf {
	struct rte_mbuf *mbuf;
	uint64_t ref_cnt;
};

// A data pipe to another worker paired with its own deferred-free ring.
//
// The producer pins every segment, recording their shared pre-pin
// refcount as the baseline reclaim checks each segment against — so
// segments must already agree on one refcount. FIFO completion drains
// the ring head-first as each segment's NIC tx finishes.
struct worker_tx_pipe {
	struct data_pipe pipe;
	struct worker_pending_mbuf *pending_mbufs;
	uint32_t pending_mask;
	uint64_t pending_start;
	uint64_t pending_stop;

	// Mbufs accumulated since the last flush.
	//
	// Held here rather than by the producer so the batch travels with the
	// pipe it is bound for, across the reallocation that grows the
	// producer's pipe array as the topology is wired up.
	struct rte_mbuf *batch[WORKER_TX_BATCH_SIZE];
	uint16_t batch_count;
};

// Initialize the pipe and its deferred-free ring.
//
// Returns 0 on success, -1 on error.
int
worker_tx_pipe_init(struct worker_tx_pipe *tx_pipe);

// Release resources allocated by worker_tx_pipe_init().
void
worker_tx_pipe_fini(struct worker_tx_pipe *tx_pipe);

// Add mbuf to the pipe's pending batch.
//
// Returns false when the batch is already full, which means the caller owes
// the pipe a flush before it can stage anything more.
bool
worker_tx_pipe_stage(struct worker_tx_pipe *tx_pipe, struct rte_mbuf *mbuf);

// Hand the staged batch to the pipe and empty it.
//
// Returns how many the pipe accepted and collects the rest, which never
// entered the pipe and stay the caller's to dispose of. The collecting array
// needs room for a whole batch.
size_t
worker_tx_pipe_flush(
	struct worker_tx_pipe *tx_pipe,
	struct rte_mbuf **rejected,
	size_t *rejected_count
);

// Hand a batch to the consumer in far fewer ring round trips than one per
// mbuf.
//
// Returns how many were accepted, and collects the rest in the order they
// were given, so the two together always account for the whole batch; the
// collecting array needs room for all of them. A chain whose segments do not
// share one refcount is refused on its own, and the rest of the batch is
// unaffected.
size_t
worker_tx_pipe_push_bulk(
	struct worker_tx_pipe *tx_pipe,
	struct rte_mbuf **mbufs,
	size_t count,
	struct rte_mbuf **rejected,
	size_t *rejected_count
);

// Reclaim consumed pipe slots and release mbufs whose consumer-side NIC tx
// has completed.
void
worker_tx_pipe_reclaim(struct worker_tx_pipe *tx_pipe);

// Caller-supplied transmit callback for worker_tx_pipe_drain().
//
// Must attempt to transmit the given mbufs and return how many were
// actually accepted. The drain frees the rest.
typedef size_t (*worker_tx_pipe_transmit_func)(
	struct rte_mbuf **mbufs, size_t count, void *data
);

// Drain a consumer-side rx pipe, handing popped mbufs to transmit_func and
// freeing whatever it did not accept.
void
worker_tx_pipe_drain(
	struct data_pipe *rx_pipe,
	worker_tx_pipe_transmit_func transmit_func,
	void *transmit_func_data
);
