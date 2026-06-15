#include <stdint.h>
#include <string.h>

#include <rte_mbuf.h>
#include <rte_memcpy.h>

#include "common/test_assert.h"

#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/worker/worker.h"
#include "lib/dataplane_ut/mempool.h"
#include "lib/logging/log.h"
#include "yanet_build_config.h" // MBUF_MAX_SIZE

#define SEG_SIZE 64

static int
assert_payload_equal(
	const struct rte_mbuf *src,
	const struct rte_mbuf *clone,
	uint32_t pkt_len
) {
	uint8_t src_buf[pkt_len];
	uint8_t clone_buf[pkt_len];

	uint32_t off = 0;
	for (const struct rte_mbuf *s = src; s != NULL && off < pkt_len;
	     s = s->next) {
		uint32_t n = s->data_len < pkt_len - off ? s->data_len
							 : pkt_len - off;
		rte_memcpy(
			src_buf + off, rte_pktmbuf_mtod(s, const uint8_t *), n
		);
		off += n;
	}

	off = 0;
	for (const struct rte_mbuf *s = clone; s != NULL && off < pkt_len;
	     s = s->next) {
		uint32_t n = s->data_len < pkt_len - off ? s->data_len
							 : pkt_len - off;
		rte_memcpy(
			clone_buf + off, rte_pktmbuf_mtod(s, const uint8_t *), n
		);
		off += n;
	}

	TEST_ASSERT(
		memcmp(src_buf, clone_buf, pkt_len) == 0,
		"clone payload does not match source"
	);
	return TEST_SUCCESS;
}

static int
assert_clone_correct(
	const struct packet *src, const struct packet *clone, uint32_t pkt_len
) {
	TEST_ASSERT_NOT_NULL(clone, "clone must be non-NULL");
	TEST_ASSERT_EQUAL(clone->mbuf->pkt_len, pkt_len, "pkt_len must match");
	TEST_ASSERT_SUCCESS(
		assert_payload_equal(src->mbuf, clone->mbuf, pkt_len),
		"payload must match byte-for-byte"
	);
	TEST_ASSERT_EQUAL(clone->hash, src->hash, "hash preserved");
	TEST_ASSERT_EQUAL(clone->flags, src->flags, "flags preserved");
	TEST_ASSERT_EQUAL(clone->vlan, src->vlan, "vlan preserved");
	TEST_ASSERT_EQUAL(
		clone->fragment_offset,
		src->fragment_offset,
		"fragment_offset preserved"
	);
	TEST_ASSERT_EQUAL(
		clone->rx_device_id, src->rx_device_id, "rx_device_id preserved"
	);
	TEST_ASSERT_EQUAL(
		clone->tx_device_id, src->tx_device_id, "tx_device_id preserved"
	);
	TEST_ASSERT_NULL(clone->next, "clone chain pointer must be NULL");
	TEST_ASSERT(
		clone->mbuf->buf_addr != src->mbuf->buf_addr,
		"clone must be a distinct mbuf"
	);
	return TEST_SUCCESS;
}

static void
set_metadata(struct packet *p, uint32_t hash, uint16_t flags, uint16_t vlan) {
	p->hash = hash;
	p->flags = flags;
	p->vlan = vlan;
	p->fragment_offset = 42;
	p->rx_device_id = 7;
	p->tx_device_id = 3;
}

static int
test_multi_segment(struct rte_mempool *pool) {
	struct dp_worker dp_worker;
	memset(&dp_worker, 0, sizeof(dp_worker));
	dp_worker.rx_mempool = pool;

	struct rte_mbuf *segs[3] = {NULL, NULL, NULL};
	for (int idx = 0; idx < 3; idx++) {
		segs[idx] = rte_pktmbuf_alloc(pool);
		if (segs[idx] == NULL) {
			goto err;
		}
		uint8_t *data =
			(uint8_t *)rte_pktmbuf_append(segs[idx], SEG_SIZE);
		if (data == NULL) {
			goto err;
		}
		memset(data, (uint8_t)idx, SEG_SIZE);
	}
	segs[0]->next = segs[1];
	segs[1]->next = segs[2];
	segs[0]->nb_segs = 3;
	segs[0]->pkt_len = 3 * SEG_SIZE;

	struct packet src;
	memset(&src, 0, sizeof(src));
	src.mbuf = segs[0];
	set_metadata(&src, 0xDEADBEEF, 0x0001, 200);

	struct packet *clone = worker_clone_packet(&dp_worker, &src);

	int rc = assert_clone_correct(&src, clone, src.mbuf->pkt_len);

	rte_pktmbuf_free(src.mbuf);
	if (clone != NULL) {
		rte_pktmbuf_free(clone->mbuf);
	}
	return rc;

err:
	for (int idx = 0; idx < 3; idx++) {
		if (segs[idx] != NULL) {
			rte_pktmbuf_free(segs[idx]);
		}
	}
	return TEST_FAILED;
}

/*
 * Clone a packet whose payload outgrows a single mbuf's data room.
 *
 * The source is larger than one segment can hold, so the deep copy is
 * forced to chain the destination instead of coalescing into one segment.
 */
static int
test_jumbo(struct rte_mempool *pool) {
	struct dp_worker dp_worker;
	memset(&dp_worker, 0, sizeof(dp_worker));
	dp_worker.rx_mempool = pool;

	const uint16_t cap = MBUF_MAX_SIZE - RTE_PKTMBUF_HEADROOM;
	const uint16_t seg_lens[2] = {cap / 2, cap * 3 / 4};

	struct rte_mbuf *segs[2] = {NULL, NULL};
	uint32_t total = 0;
	for (int idx = 0; idx < 2; idx++) {
		segs[idx] = rte_pktmbuf_alloc(pool);
		if (segs[idx] == NULL) {
			goto err;
		}
		uint8_t *data =
			(uint8_t *)rte_pktmbuf_append(segs[idx], seg_lens[idx]);
		if (data == NULL) {
			goto err;
		}
		memset(data, (uint8_t)(0xA0 + idx), seg_lens[idx]);
		total += seg_lens[idx];
	}
	segs[0]->next = segs[1];
	segs[0]->nb_segs = 2;
	segs[0]->pkt_len = total;

	struct packet src;
	memset(&src, 0, sizeof(src));
	src.mbuf = segs[0];
	set_metadata(&src, 0xABCDEF01, 0x0002, 300);

	struct packet *clone = worker_clone_packet(&dp_worker, &src);
	int rc = assert_clone_correct(&src, clone, src.mbuf->pkt_len);

	rte_pktmbuf_free(src.mbuf);
	if (clone != NULL) {
		rte_pktmbuf_free(clone->mbuf);
	}

	TEST_ASSERT_SUCCESS(rc, "jumbo clone correctness");
	return TEST_SUCCESS;

err:
	for (int idx = 0; idx < 2; idx++) {
		if (segs[idx] != NULL) {
			rte_pktmbuf_free(segs[idx]);
		}
	}
	return TEST_FAILED;
}

int
main(void) {
	log_enable_name("info");

	struct rte_mempool *pool = test_mempool_create();
	if (pool == NULL) {
		LOG(ERROR, "test_mempool_create failed");
		return TEST_FAILED;
	}

	struct {
		const char *name;
		int (*fn)(struct rte_mempool *);
	} tests[] = {
		{"multi_segment", test_multi_segment},
		{"jumbo", test_jumbo},
	};

	size_t total = sizeof(tests) / sizeof(tests[0]);
	size_t failed = 0;

	for (size_t idx = 0; idx < total; idx++) {
		LOG(INFO,
		    "[%zu/%zu] running %s...",
		    idx + 1,
		    total,
		    tests[idx].name);
		if (tests[idx].fn(pool) != TEST_SUCCESS) {
			LOG(ERROR, "%s FAILED", tests[idx].name);
			failed++;
		} else {
			LOG(INFO, "%s passed", tests[idx].name);
		}
	}

	free(pool);

	if (failed == 0) {
		LOG(INFO, "all %zu worker_clone tests passed", total);
	} else {
		LOG(ERROR, "%zu/%zu worker_clone tests failed", failed, total);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
