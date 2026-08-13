#include "tx_pipe.h"

#include <stdlib.h>

#include <rte_mbuf.h>

#include "lib/dataplane/worker/tx_pipe.h"
#include "lib/dataplane_ut/mempool.h"

struct dataplane_ut_tx_pipe {
	struct worker_tx_pipe tx_pipe;
	struct rte_mempool *mempool;
};

struct dataplane_ut_tx_pipe *
dataplane_ut_tx_pipe_new(void) {
	struct dataplane_ut_tx_pipe *fixture =
		(struct dataplane_ut_tx_pipe *)malloc(sizeof(*fixture));
	if (fixture == NULL) {
		return NULL;
	}

	fixture->mempool = test_mempool_create();
	if (fixture->mempool == NULL) {
		free(fixture);
		return NULL;
	}

	if (worker_tx_pipe_init(&fixture->tx_pipe)) {
		test_mempool_free(fixture->mempool);
		free(fixture);
		return NULL;
	}

	return fixture;
}

void
dataplane_ut_tx_pipe_free(struct dataplane_ut_tx_pipe *fixture) {
	if (fixture == NULL) {
		return;
	}

	worker_tx_pipe_fini(&fixture->tx_pipe);
	test_mempool_free(fixture->mempool);
	free(fixture);
}

struct rte_mbuf *
dataplane_ut_tx_pipe_alloc_mbuf(
	struct dataplane_ut_tx_pipe *fixture, size_t seg_count
) {
	if (seg_count == 0) {
		return NULL;
	}

	struct rte_mbuf *head = NULL;
	struct rte_mbuf *tail = NULL;

	for (size_t idx = 0; idx < seg_count; ++idx) {
		struct rte_mbuf *seg = rte_pktmbuf_alloc(fixture->mempool);
		if (seg == NULL) {
			if (head != NULL) {
				rte_pktmbuf_free(head);
			}
			return NULL;
		}

		if (head == NULL) {
			head = seg;
		} else {
			tail->next = seg;
		}
		tail = seg;
	}

	head->nb_segs = (uint16_t)seg_count;
	head->pkt_len = 0;

	return head;
}

int
dataplane_ut_tx_pipe_push(
	struct dataplane_ut_tx_pipe *fixture, struct rte_mbuf *mbuf
) {
	return worker_tx_pipe_push(&fixture->tx_pipe, mbuf);
}

struct dataplane_ut_tx_pipe_transmit_ctx {
	size_t accept;
};

static size_t
dataplane_ut_tx_pipe_transmit_cb(
	struct rte_mbuf **mbufs, size_t count, void *data
) {
	(void)mbufs;
	struct dataplane_ut_tx_pipe_transmit_ctx *ctx =
		(struct dataplane_ut_tx_pipe_transmit_ctx *)data;

	return ctx->accept < count ? ctx->accept : count;
}

void
dataplane_ut_tx_pipe_drain(
	struct dataplane_ut_tx_pipe *fixture, size_t accept
) {
	struct dataplane_ut_tx_pipe_transmit_ctx ctx = {.accept = accept};

	worker_tx_pipe_drain(
		&fixture->tx_pipe.pipe, dataplane_ut_tx_pipe_transmit_cb, &ctx
	);
}

void
dataplane_ut_tx_pipe_complete(struct rte_mbuf *mbuf) {
	rte_pktmbuf_free(mbuf);
}

struct rte_mbuf *
dataplane_ut_tx_pipe_segment(struct rte_mbuf *mbuf, size_t idx) {
	while (mbuf != NULL && idx > 0) {
		mbuf = mbuf->next;
		--idx;
	}
	return mbuf;
}

uint16_t
dataplane_ut_tx_pipe_segment_refcnt(struct rte_mbuf *segment) {
	return rte_mbuf_refcnt_read(segment);
}

void
dataplane_ut_tx_pipe_segment_refcnt_add(
	struct rte_mbuf *segment, int16_t delta
) {
	rte_mbuf_refcnt_update(segment, delta);
}

void
dataplane_ut_tx_pipe_complete_segment(struct rte_mbuf *segment) {
	rte_pktmbuf_free_seg(segment);
}

void
dataplane_ut_tx_pipe_reclaim(struct dataplane_ut_tx_pipe *fixture) {
	worker_tx_pipe_reclaim(&fixture->tx_pipe);
}

size_t
dataplane_ut_tx_pipe_outstanding(struct dataplane_ut_tx_pipe *fixture) {
	return test_mempool_outstanding(fixture->mempool);
}
