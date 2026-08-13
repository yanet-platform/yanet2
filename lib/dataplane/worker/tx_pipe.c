#include "tx_pipe.h"

#include <stdlib.h>
#include <string.h>

#include <rte_mbuf.h>

struct worker_push_ctx {
	struct worker_tx_pipe *tx_pipe;
	struct rte_mbuf *mbuf;
};

static size_t
worker_connection_push_cb(void **item, size_t count, void *data) {
	struct worker_push_ctx *push_ctx = (struct worker_push_ctx *)data;
	struct worker_tx_pipe *tx_pipe = push_ctx->tx_pipe;

	if (count > 0) {
		uint32_t ref_cnt =
			rte_mbuf_refcnt_update(push_ctx->mbuf, 1) - 1;
		memcpy(item, &push_ctx->mbuf, sizeof(struct rte_mbuf *));

		uint32_t ofs = tx_pipe->pending_stop & tx_pipe->pending_mask;
		tx_pipe->pending_mbufs[ofs].mbuf = push_ctx->mbuf;
		tx_pipe->pending_mbufs[ofs].ref_cnt = ref_cnt;
		++tx_pipe->pending_stop;

		return 1;
	}
	return 0;
}

static size_t
worker_connection_free_cb(void **item, size_t count, void *data) {
	(void)item;
	(void)data;

	return count;
}

int
worker_tx_pipe_init(struct worker_tx_pipe *tx_pipe) {
	if (data_pipe_init(&tx_pipe->pipe, WORKER_TX_PIPE_SIZE)) {
		return -1;
	}

	uint32_t pending_capacity =
		1u << (WORKER_TX_PIPE_SIZE + WORKER_TX_PIPE_PENDING_SHIFT);
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

void
worker_tx_pipe_reclaim(struct worker_tx_pipe *tx_pipe) {
	// Reclaim consumed pipe slots (producer free phase).
	data_pipe_item_free(&tx_pipe->pipe, worker_connection_free_cb, NULL);

	// Release mbufs whose consumer-side NIC tx has completed. A single
	// pipe feeds one rx worker and one NIC tx queue, so completion is
	// FIFO: drain from the head and stop at the first mbuf still held.
	while (tx_pipe->pending_start < tx_pipe->pending_stop) {
		uint32_t ofs = tx_pipe->pending_start & tx_pipe->pending_mask;
		struct worker_pending_mbuf *pending =
			tx_pipe->pending_mbufs + ofs;

		if (rte_mbuf_refcnt_read(pending->mbuf) > pending->ref_cnt) {
			break;
		}

		rte_pktmbuf_free(pending->mbuf);
		++tx_pipe->pending_start;
	}
}

struct worker_tx_pipe_drain_ctx {
	worker_tx_pipe_transmit_func transmit_func;
	void *transmit_func_data;
};

static size_t
worker_tx_pipe_drain_cb(void **item, size_t count, void *data) {
	struct worker_tx_pipe_drain_ctx *drain_ctx =
		(struct worker_tx_pipe_drain_ctx *)data;
	struct rte_mbuf **mbufs = (struct rte_mbuf **)item;

	size_t written = drain_ctx->transmit_func(
		mbufs, count, drain_ctx->transmit_func_data
	);

	for (size_t idx = written; idx < count; ++idx) {
		rte_pktmbuf_free(mbufs[idx]);
	}

	return count;
}

void
worker_tx_pipe_drain(
	struct data_pipe *rx_pipe,
	worker_tx_pipe_transmit_func transmit_func,
	void *transmit_func_data
) {
	struct worker_tx_pipe_drain_ctx drain_ctx = {
		.transmit_func = transmit_func,
		.transmit_func_data = transmit_func_data,
	};

	data_pipe_item_pop(rx_pipe, worker_tx_pipe_drain_cb, &drain_ctx);
}
