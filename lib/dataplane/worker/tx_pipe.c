#include "tx_pipe.h"

#include <stdint.h>
#include <string.h>

#include <rte_mbuf.h>

int
worker_tx_pipe_init(struct worker_tx_pipe *tx_pipe, size_t pipe_size) {
	return data_pipe_init(&tx_pipe->pipe, pipe_size);
}

void
worker_tx_pipe_fini(struct worker_tx_pipe *tx_pipe) {
	data_pipe_fini(&tx_pipe->pipe);
}

struct worker_push_ctx {
	struct rte_mbuf *mbuf;
};

static size_t
worker_connection_push_cb(void **item, size_t count, void *data) {
	struct worker_push_ctx *push_ctx = (struct worker_push_ctx *)data;

	if (count > 0) {
		memcpy(item, &push_ctx->mbuf, sizeof(struct rte_mbuf *));
		return 1;
	}
	return 0;
}

int
worker_tx_pipe_push(struct worker_tx_pipe *tx_pipe, struct rte_mbuf *mbuf) {
	struct worker_push_ctx push_ctx = {
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

void
worker_tx_pipe_reclaim(struct worker_tx_pipe *tx_pipe) {
	// Reclaim consumed pipe slots. data_pipe_item_push() sizes
	// availability off f_pos, which only advances here, so this call
	// must run even though it is now a no-op on the mbufs themselves.
	data_pipe_item_free(&tx_pipe->pipe, worker_connection_free_cb, NULL);
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
