#include "tx_pipe.h"

#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <rte_mbuf.h>

int
worker_tx_pipe_init(struct worker_tx_pipe *tx_pipe, size_t pipe_size) {
	if (data_pipe_init(&tx_pipe->pipe, pipe_size)) {
		return -1;
	}

	uint32_t pending_capacity =
		1u << (pipe_size + WORKER_TX_PIPE_PENDING_SHIFT);
	tx_pipe->pending_mbufs = (struct worker_pending_mbuf *)malloc(
		sizeof(struct worker_pending_mbuf) * pending_capacity
	);
	if (tx_pipe->pending_mbufs == NULL) {
		data_pipe_fini(&tx_pipe->pipe);
		return -1;
	}
	tx_pipe->pending_mask = pending_capacity - 1;
	tx_pipe->pending_start = 0;
	tx_pipe->pending_stop = 0;

	return 0;
}

void
worker_tx_pipe_fini(struct worker_tx_pipe *tx_pipe) {
	free(tx_pipe->pending_mbufs);
	data_pipe_fini(&tx_pipe->pipe);
}

struct worker_push_ctx {
	struct worker_tx_pipe *tx_pipe;
	struct rte_mbuf *mbuf;
};

static size_t
worker_connection_push_cb(void **item, size_t count, void *data) {
	struct worker_push_ctx *push_ctx = (struct worker_push_ctx *)data;
	struct worker_tx_pipe *tx_pipe = push_ctx->tx_pipe;

	if (count > 0) {
		// Read the pre-pin baseline before rte_pktmbuf_refcnt_update()
		// pins every segment of the chain by one.
		uint64_t ref_cnt = rte_mbuf_refcnt_read(push_ctx->mbuf);
		rte_pktmbuf_refcnt_update(push_ctx->mbuf, 1);
		memcpy(item, &push_ctx->mbuf, sizeof(struct rte_mbuf *));

		uint32_t ofs = tx_pipe->pending_stop & tx_pipe->pending_mask;
		tx_pipe->pending_mbufs[ofs].mbuf = push_ctx->mbuf;
		tx_pipe->pending_mbufs[ofs].ref_cnt = ref_cnt;
		++tx_pipe->pending_stop;

		return 1;
	}
	return 0;
}

int
worker_tx_pipe_push(struct worker_tx_pipe *tx_pipe, struct rte_mbuf *mbuf) {
	// Backpressure: drop when this pipe's pending ring is full.
	if (tx_pipe->pending_stop - tx_pipe->pending_start >
	    tx_pipe->pending_mask) {
		return -1;
	}

	struct worker_push_ctx push_ctx = {
		.tx_pipe = tx_pipe,
		.mbuf = mbuf,
	};

	if (data_pipe_item_push(
		    &tx_pipe->pipe, worker_connection_push_cb, &push_ctx
	    ) != 1) {
		return -1;
	}

	return 0;
}

static size_t
worker_connection_free_cb(void **item, size_t count, void *data) {
	(void)item;
	(void)data;

	return count;
}

// Reports whether every segment of mbuf has decremented back to ref_cnt,
// meaning the consumer's free has run on the whole chain and only the
// producer's pin is left. The consumer's decrement never clears a pinned
// segment's `next` (it only retires that once the pin itself is gone), so
// walking the chain here is safe even mid-decrement on the consumer side.
static bool
worker_pending_mbuf_ready(struct rte_mbuf *mbuf, uint64_t ref_cnt) {
	do {
		if (rte_mbuf_refcnt_read(mbuf) > ref_cnt) {
			return false;
		}
	} while ((mbuf = mbuf->next) != NULL);

	return true;
}

void
worker_tx_pipe_reclaim(struct worker_tx_pipe *tx_pipe) {
	// Reclaim consumed pipe slots (producer free phase).
	data_pipe_item_free(&tx_pipe->pipe, worker_connection_free_cb, NULL);

	// Release mbufs whose consumer-side handling has completed on every
	// segment. A single pipe feeds one rx worker and one NIC tx queue,
	// so completion is FIFO: drain from the head and stop at the first
	// mbuf still held.
	while (tx_pipe->pending_start < tx_pipe->pending_stop) {
		uint32_t ofs = tx_pipe->pending_start & tx_pipe->pending_mask;
		struct worker_pending_mbuf *pending =
			tx_pipe->pending_mbufs + ofs;

		if (!worker_pending_mbuf_ready(
			    pending->mbuf, pending->ref_cnt
		    )) {
			break;
		}

		rte_pktmbuf_free(pending->mbuf);
		++tx_pipe->pending_start;
	}
}

struct worker_tx_pipe_drain_ctx {
	worker_tx_pipe_xmit_fn xmit;
	void *ctx;
};

static size_t
worker_tx_pipe_drain_cb(void **item, size_t count, void *data) {
	struct worker_tx_pipe_drain_ctx *drain_ctx =
		(struct worker_tx_pipe_drain_ctx *)data;
	struct rte_mbuf **mbufs = (struct rte_mbuf **)item;

	// xmit's count is uint16_t - clamp the burst handed to it so a caller
	// with a wider pipe (pipe_size >= 16) can't silently truncate count
	// on the cast below. Anything past the clamp is freed by the tail
	// loop below, same as a rejected item.
	size_t burst = count < UINT16_MAX ? count : UINT16_MAX;
	uint16_t written =
		drain_ctx->xmit(drain_ctx->ctx, mbufs, (uint16_t)burst);

	for (size_t idx = written; idx < count; ++idx) {
		rte_pktmbuf_free(mbufs[idx]);
	}

	return count;
}

size_t
worker_tx_pipe_drain(
	struct data_pipe *rx_pipe, worker_tx_pipe_xmit_fn xmit, void *ctx
) {
	struct worker_tx_pipe_drain_ctx drain_ctx = {
		.xmit = xmit,
		.ctx = ctx,
	};

	return data_pipe_item_pop(rx_pipe, worker_tx_pipe_drain_cb, &drain_ctx);
}
