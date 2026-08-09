#pragma once

#include <stddef.h>
#include <stdint.h>

#include "common/data_pipe.h"

struct rte_mbuf;

// log2 of the per-connection SPSC data pipe capacity.
#define WORKER_TX_PIPE_SIZE 10

// A data pipe to another worker.
struct worker_tx_pipe {
	struct data_pipe pipe;
};

// Transmits the leading mbufs of a drained burst.
//
// Returns the number of leading mbufs accepted for transmit.
typedef uint16_t (*worker_tx_pipe_xmit_fn)(
	void *ctx, struct rte_mbuf **mbufs, uint16_t count
);

// Initialize a tx pipe: the underlying data pipe alone.
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
// Returns 0 on success, -1 when the pipe is full.
int
worker_tx_pipe_push(struct worker_tx_pipe *tx_pipe, struct rte_mbuf *mbuf);

// Reclaim consumed pipe slots (producer side).
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
