#pragma once

#include <stddef.h>
#include <stdint.h>

#include "common/data_pipe.h"

// log2 of the per-connection SPSC data pipe capacity.
#define WORKER_TX_PIPE_SIZE 10
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
};

// Initialize the pipe and its deferred-free ring.
//
// Returns 0 on success, -1 on error.
int
worker_tx_pipe_init(struct worker_tx_pipe *tx_pipe);

// Release resources allocated by worker_tx_pipe_init().
void
worker_tx_pipe_fini(struct worker_tx_pipe *tx_pipe);

// Push mbuf onto the pipe for the consumer to transmit.
//
// Returns 0 on success, -1 if the pipe or its deferred-free ring is
// full, or if the chain's segments don't share one refcount to record
// as the reclaim baseline.
int
worker_tx_pipe_push(struct worker_tx_pipe *tx_pipe, struct rte_mbuf *mbuf);

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
