#pragma once

#include <stddef.h>
#include <stdint.h>

#include "common/data_pipe.h"

struct rte_mbuf;

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
// The producer pins every segment of a chained mbuf pushed into `pipe` and
// records the pre-pin refcount in `pending_mbufs`; every segment of a
// fresh chain starts at the same refcount, so one reading applies to the
// whole chain. worker_tx_pipe_reclaim() performs the real free once every
// segment's consumer-side handling has dropped its refcount back to that
// baseline.
struct worker_tx_pipe {
	struct data_pipe pipe;
	struct worker_pending_mbuf *pending_mbufs;
	uint32_t pending_mask;
	uint64_t pending_start;
	uint64_t pending_stop;
};

// Transmits the leading mbufs of a drained burst.
//
// Returns the number of leading mbufs accepted for transmit.
typedef uint16_t (*worker_tx_pipe_xmit_fn)(
	void *ctx, struct rte_mbuf **mbufs, uint16_t count
);

// Initialize a tx pipe: the underlying data pipe and its deferred-free ring.
//
// Returns 0 on success, -1 on allocation failure.
int
worker_tx_pipe_init(struct worker_tx_pipe *tx_pipe, size_t pipe_size);

// Release resources allocated by worker_tx_pipe_init().
//
// The caller must have drained the pipe first: this releases the ring
// itself and does not free mbufs still queued in it.
void
worker_tx_pipe_fini(struct worker_tx_pipe *tx_pipe);

// Push mbuf into the pipe (producer side).
//
// Pins every segment against the consumer's free so worker_tx_pipe_reclaim()
// can perform the real free once the pin is the only reference left.
//
// Returns 0 on success, -1 when the pipe or its pending ring is full.
int
worker_tx_pipe_push(struct worker_tx_pipe *tx_pipe, struct rte_mbuf *mbuf);

// Reclaim consumed pipe slots, and free mbufs whose consumer-side handling
// has completed on every segment (producer side).
void
worker_tx_pipe_reclaim(struct worker_tx_pipe *tx_pipe);

// Drain a pipe's ready items, handing each burst to xmit and freeing the
// rejected tail (consumer side).
//
// Returns the number of items drained.
size_t
worker_tx_pipe_drain(
	struct data_pipe *rx_pipe, worker_tx_pipe_xmit_fn xmit, void *ctx
);
