#include "tx_pipe.h"

#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

#include <rte_mbuf.h>

struct worker_push_ctx {
	struct worker_tx_pipe *tx_pipe;
	struct rte_mbuf **mbufs;
	size_t count;
	size_t cursor;
	struct rte_mbuf **rejected;
	size_t *rejected_count;
};

// Pin every segment of one chain, recording the refcount they shared
// beforehand.
//
// Every segment must start from one baseline, the value reclaim checks each
// against: one above it never sinks back down and wedges the pipe, one below
// it already reads as reclaimable while still in flight. Walked by hand
// rather than pinning the chain blind, which offers no unwind on mismatch.
// Returns false with every pin already taken undone.
static bool
worker_pin_chain(struct rte_mbuf *mbuf, uint64_t *ref_cnt) {
	uint64_t baseline = rte_mbuf_refcnt_read(mbuf);

	struct rte_mbuf *seg = mbuf;
	do {
		if (rte_mbuf_refcnt_read(seg) != baseline) {
			for (struct rte_mbuf *pinned = mbuf; pinned != seg;
			     pinned = pinned->next) {
				rte_mbuf_refcnt_update(pinned, -1);
			}
			return false;
		}
		rte_mbuf_refcnt_update(seg, 1);
	} while ((seg = seg->next) != NULL);

	*ref_cnt = baseline;
	return true;
}

// Fill as much of the offered run of ring slots as the batch and the
// deferred-free ring allow.
//
// A chain that cannot be pinned is set aside and the walk carries on, so one
// bad chain costs itself a place in the batch and nothing more.
static size_t
worker_connection_push_cb(void **item, size_t count, void *data) {
	struct worker_push_ctx *push_ctx = (struct worker_push_ctx *)data;
	struct worker_tx_pipe *tx_pipe = push_ctx->tx_pipe;

	// Backpressure: never outrun the deferred-free ring, which has to hold
	// every pushed mbuf until its consumer-side tx completes.
	uint64_t capacity = (uint64_t)tx_pipe->pending_mask + 1;
	uint64_t in_flight = tx_pipe->pending_stop - tx_pipe->pending_start;
	uint64_t pending_free = in_flight < capacity ? capacity - in_flight : 0;

	size_t limit = count;
	if (limit > pending_free) {
		limit = pending_free;
	}

	size_t written = 0;
	while (written < limit && push_ctx->cursor < push_ctx->count) {
		struct rte_mbuf *mbuf = push_ctx->mbufs[push_ctx->cursor];

		uint64_t ref_cnt;
		if (!worker_pin_chain(mbuf, &ref_cnt)) {
			push_ctx->rejected[(*push_ctx->rejected_count)++] =
				mbuf;
			++push_ctx->cursor;
			continue;
		}

		memcpy(item + written, &mbuf, sizeof(struct rte_mbuf *));

		uint32_t ofs = tx_pipe->pending_stop & tx_pipe->pending_mask;
		tx_pipe->pending_mbufs[ofs].mbuf = mbuf;
		tx_pipe->pending_mbufs[ofs].ref_cnt = ref_cnt;
		++tx_pipe->pending_stop;

		++written;
		++push_ctx->cursor;
	}

	return written;
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
	tx_pipe->batch_count = 0;

	return 0;
}

void
worker_tx_pipe_fini(struct worker_tx_pipe *tx_pipe) {
	free(tx_pipe->pending_mbufs);
	data_pipe_fini(&tx_pipe->pipe);
}

size_t
worker_tx_pipe_push_bulk(
	struct worker_tx_pipe *tx_pipe,
	struct rte_mbuf **mbufs,
	size_t count,
	struct rte_mbuf **rejected,
	size_t *rejected_count
) {
	*rejected_count = 0;

	struct worker_push_ctx push_ctx = {
		.tx_pipe = tx_pipe,
		.mbufs = mbufs,
		.count = count,
		.cursor = 0,
		.rejected = rejected,
		.rejected_count = rejected_count,
	};

	// A batch straddling the ring's wrap needs a second round trip to
	// place its tail, since only the slots before the boundary are offered.
	//
	// Progress is how far through the batch the walk got, not how many it
	// placed, since setting a chain aside advances without placing
	// anything.
	size_t pushed = 0;
	while (push_ctx.cursor < count) {
		size_t before = push_ctx.cursor;

		pushed += data_pipe_item_push(
			&tx_pipe->pipe, worker_connection_push_cb, &push_ctx
		);

		if (push_ctx.cursor == before) {
			break;
		}
	}

	// Whatever the pipe had no room for never entered it and stays the
	// caller's, collected in the order the batch was given.
	for (size_t idx = push_ctx.cursor; idx < count; ++idx) {
		rejected[(*rejected_count)++] = mbufs[idx];
	}

	return pushed;
}

bool
worker_tx_pipe_stage(struct worker_tx_pipe *tx_pipe, struct rte_mbuf *mbuf) {
	if (tx_pipe->batch_count == WORKER_TX_BATCH_SIZE) {
		return false;
	}

	tx_pipe->batch[tx_pipe->batch_count++] = mbuf;
	return true;
}

size_t
worker_tx_pipe_flush(
	struct worker_tx_pipe *tx_pipe,
	struct rte_mbuf **rejected,
	size_t *rejected_count
) {
	size_t pushed = worker_tx_pipe_push_bulk(
		tx_pipe,
		tx_pipe->batch,
		tx_pipe->batch_count,
		rejected,
		rejected_count
	);

	tx_pipe->batch_count = 0;
	return pushed;
}

// True once every segment has sunk back to ref_cnt: the consumer freed
// the whole chain, only the producer's pin remains.
//
// Safe mid-decrement — a segment's next pointer clears only once its
// own pin drops, never while still above baseline.
static bool
worker_pending_mbuf_ready(struct rte_mbuf *mbuf, uint64_t ref_cnt) {
	do {
		// Acquire load, not relaxed + fence: a bare fence with no
		// preceding acquire load is invisible to TSan. Pairs with
		// the consumer's release decrement — DPDK has no
		// acquire-ordered refcnt accessor.
		if (rte_atomic_load_explicit(
			    &mbuf->refcnt, rte_memory_order_acquire
		    ) > ref_cnt) {
			return false;
		}
	} while ((mbuf = mbuf->next) != NULL);

	return true;
}

void
worker_tx_pipe_reclaim(struct worker_tx_pipe *tx_pipe) {
	// Reclaim consumed pipe slots (producer free phase).
	data_pipe_item_free(&tx_pipe->pipe, worker_connection_free_cb, NULL);

	// Release mbufs whose consumer-side NIC tx has completed on every
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
