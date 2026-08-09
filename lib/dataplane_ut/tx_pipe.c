#include "lib/dataplane_ut/tx_pipe.h"

#include <stdlib.h>

#include <rte_mbuf.h>

#include "lib/dataplane/worker/tx_pipe.h"
#include "lib/dataplane_ut/mempool.h"

struct dataplane_ut_tx_pipe {
	struct worker_tx_pipe pipe;
	struct rte_mempool *mempool;
	// Mbufs accepted by the stub xmit and not yet released by
	// dataplane_ut_tx_pipe_complete_tx().
	struct rte_mbuf **accepted;
	size_t accepted_count;
	size_t accepted_capacity;
};

struct dataplane_ut_tx_pipe *
dataplane_ut_tx_pipe_new(size_t pipe_size) {
	struct dataplane_ut_tx_pipe *harness = calloc(1, sizeof(*harness));
	if (harness == NULL) {
		return NULL;
	}

	harness->mempool = test_mempool_create();
	if (harness->mempool == NULL) {
		free(harness);
		return NULL;
	}

	if (worker_tx_pipe_init(&harness->pipe, pipe_size)) {
		test_mempool_free(harness->mempool);
		free(harness);
		return NULL;
	}

	return harness;
}

// Reject every item, so worker_tx_pipe_drain() frees it via its tail loop
// instead of handing it to a caller-owned xmit.
static uint16_t
drain_reject_xmit(void *ctx, struct rte_mbuf **mbufs, uint16_t count) {
	(void)ctx;
	(void)mbufs;
	(void)count;
	return 0;
}

void
dataplane_ut_tx_pipe_free(struct dataplane_ut_tx_pipe *harness) {
	if (harness == NULL) {
		return;
	}

	dataplane_ut_tx_pipe_complete_tx(harness);
	// Free mbufs still pushed but never drained, so a test that skips
	// draining before teardown does not leak them.
	while (worker_tx_pipe_drain(
		       &harness->pipe.pipe, drain_reject_xmit, NULL
	       ) > 0) {
	}
	free(harness->accepted);
	worker_tx_pipe_fini(&harness->pipe);
	test_mempool_free(harness->mempool);
	free(harness);
}

struct rte_mbuf *
dataplane_ut_tx_pipe_alloc_mbuf(
	struct dataplane_ut_tx_pipe *harness,
	uint32_t seg_count,
	uint32_t seg_size
) {
	if (seg_count == 0) {
		return NULL;
	}

	struct rte_mbuf *head = rte_pktmbuf_alloc(harness->mempool);
	if (head == NULL) {
		return NULL;
	}
	if (rte_pktmbuf_append(head, seg_size) == NULL) {
		rte_pktmbuf_free(head);
		return NULL;
	}

	for (uint32_t idx = 1; idx < seg_count; ++idx) {
		struct rte_mbuf *seg = rte_pktmbuf_alloc(harness->mempool);
		if (seg == NULL) {
			rte_pktmbuf_free(head);
			return NULL;
		}
		if (rte_pktmbuf_append(seg, seg_size) == NULL ||
		    rte_pktmbuf_chain(head, seg)) {
			rte_pktmbuf_free(seg);
			rte_pktmbuf_free(head);
			return NULL;
		}
	}

	return head;
}

int
dataplane_ut_tx_pipe_push(
	struct dataplane_ut_tx_pipe *harness, struct rte_mbuf *mbuf
) {
	return worker_tx_pipe_push(&harness->pipe, mbuf);
}

struct drain_accept_ctx {
	struct dataplane_ut_tx_pipe *harness;
	uint16_t accept_count;
};

// Grow harness->accepted and append mbuf. Returns -1 on allocation
// failure, leaving the mbuf untracked; the caller stops accepting at that
// point so the mbuf is freed via the rejected-tail path instead.
static int
harness_retain_accepted(
	struct dataplane_ut_tx_pipe *harness, struct rte_mbuf *mbuf
) {
	if (harness->accepted_count == harness->accepted_capacity) {
		size_t capacity = harness->accepted_capacity
					  ? harness->accepted_capacity * 2
					  : 8;
		struct rte_mbuf **grown =
			realloc(harness->accepted, capacity * sizeof(*grown));
		if (grown == NULL) {
			return -1;
		}
		harness->accepted = grown;
		harness->accepted_capacity = capacity;
	}

	harness->accepted[harness->accepted_count++] = mbuf;
	return 0;
}

// Stub xmit accepting only the leading accept_count mbufs of a burst, so a
// test can force the tail-rejection path without a NIC. Accepted mbufs are
// retained on the harness until dataplane_ut_tx_pipe_complete_tx() frees
// them, modeling consumer ownership until NIC tx completion. On a retain
// failure, the loop stops early and reports fewer accepted mbufs so the
// untracked one is freed by the caller's rejected-tail path instead of
// leaking.
static uint16_t
drain_accept_xmit(void *ctx, struct rte_mbuf **mbufs, uint16_t count) {
	struct drain_accept_ctx *accept_ctx = (struct drain_accept_ctx *)ctx;
	uint16_t accepted = accept_ctx->accept_count < count
				    ? accept_ctx->accept_count
				    : count;

	uint16_t idx = 0;
	for (; idx < accepted; ++idx) {
		if (harness_retain_accepted(accept_ctx->harness, mbufs[idx])) {
			break;
		}
	}

	return idx;
}

size_t
dataplane_ut_tx_pipe_drain(
	struct dataplane_ut_tx_pipe *harness, uint16_t accept_count
) {
	struct drain_accept_ctx ctx = {
		.harness = harness,
		.accept_count = accept_count,
	};
	return worker_tx_pipe_drain(
		&harness->pipe.pipe, drain_accept_xmit, &ctx
	);
}

void
dataplane_ut_tx_pipe_complete_tx(struct dataplane_ut_tx_pipe *harness) {
	for (size_t idx = 0; idx < harness->accepted_count; ++idx) {
		rte_pktmbuf_free(harness->accepted[idx]);
	}
	harness->accepted_count = 0;
}

void
dataplane_ut_tx_pipe_reclaim(struct dataplane_ut_tx_pipe *harness) {
	worker_tx_pipe_reclaim(&harness->pipe);
}

uint64_t
dataplane_ut_tx_pipe_outstanding(struct dataplane_ut_tx_pipe *harness) {
	return test_mempool_outstanding(harness->mempool);
}
