#include <stdint.h>
#include <string.h>

#include <rte_mbuf.h>
#include <rte_memcpy.h>

#include "common/test_assert.h"

#include "lib/controlplane/config/econtext.h"
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
		clone->recirc_initialized,
		src->recirc_initialized,
		"recirculation initialization state preserved"
	);
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
	p->recirc_remaining = 5;
	p->recirc_initialized = 1;
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

	struct packet *clone = worker_clone_packet(&dp_worker, &src, 64);

	int rc = assert_clone_correct(&src, clone, src.mbuf->pkt_len);
	if (rc == TEST_SUCCESS) {
		TEST_ASSERT_EQUAL(
			src.recirc_remaining,
			3,
			"source must keep the odd recirculation credit"
		);
		TEST_ASSERT_EQUAL(
			clone->recirc_remaining,
			2,
			"clone must receive half the recirculation credits"
		);
		TEST_ASSERT_EQUAL(
			src.recirc_remaining + clone->recirc_remaining,
			5,
			"cloning must preserve aggregate recirculation credits"
		);
		TEST_ASSERT(
			packet_recirc_try_redirect(
				clone, PACKET_RECIRC_LIMIT_DEFAULT
			),
			"clone must spend its assigned budget"
		);
		TEST_ASSERT_EQUAL(
			src.recirc_remaining,
			3,
			"clone redirect must not change the source's share"
		);
		TEST_ASSERT_EQUAL(
			clone->recirc_remaining,
			1,
			"clone redirect must consume one assigned credit"
		);
	}

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

static int
test_packet_alloc_resets_metadata(struct rte_mempool *pool) {
	struct dp_worker dp_worker = {0};
	dp_worker.rx_mempool = pool;

	test_mempool_poison(pool, 1);
	struct packet *packet = worker_packet_alloc(&dp_worker);
	test_mempool_poison(pool, 0);
	TEST_ASSERT_NOT_NULL(packet, "packet allocation must succeed");
	TEST_ASSERT_NULL(packet->next, "new packet chain pointer must be NULL");
	TEST_ASSERT_EQUAL(packet->hash, 0, "new packet hash must be zero");
	TEST_ASSERT_EQUAL(
		packet->rx_device_id, 0, "new packet rx device must be zero"
	);
	TEST_ASSERT_EQUAL(
		packet->tx_device_id, 0, "new packet tx device must be zero"
	);
	TEST_ASSERT_EQUAL(
		packet->module_device_id,
		0,
		"new packet module device must be zero"
	);
	TEST_ASSERT_EQUAL(packet->flags, 0, "new packet flags must be zero");
	TEST_ASSERT_EQUAL(packet->vlan, 0, "new packet vlan must be zero");
	TEST_ASSERT_EQUAL(
		packet->recirc_remaining,
		0,
		"new packet recirculation remaining budget must be zero"
	);
	TEST_ASSERT_EQUAL(
		packet->recirc_initialized,
		0,
		"new packet recirculation state must be uninitialized"
	);
	TEST_ASSERT_EQUAL(
		packet->fragment_offset,
		0,
		"new packet fragment offset must be zero"
	);
	TEST_ASSERT_EQUAL(
		packet->data_len, 0, "new packet data length must be zero"
	);
	worker_packet_free(packet);
	return TEST_SUCCESS;
}

static int
test_recirc_drop_counts_full_packet(struct rte_mempool *pool) {
	struct rte_mbuf *first = rte_pktmbuf_alloc(pool);
	struct rte_mbuf *second = rte_pktmbuf_alloc(pool);
	if (first == NULL || second == NULL) {
		if (first != NULL) {
			rte_pktmbuf_free(first);
		}
		if (second != NULL) {
			rte_pktmbuf_free(second);
		}
		return TEST_FAILED;
	}

	const uint16_t first_len = 16;
	const uint16_t second_len = 24;
	if (rte_pktmbuf_append(first, first_len) == NULL ||
	    rte_pktmbuf_append(second, second_len) == NULL) {
		rte_pktmbuf_free(first);
		rte_pktmbuf_free(second);
		return TEST_FAILED;
	}
	first->next = second;
	first->nb_segs = 2;
	first->pkt_len = first_len + second_len;

	struct packet packet = {
		.mbuf = first,
		.data_len = first_len,
	};
	struct device_entry_ectx entry = {0};
	uint64_t counters[2] = {0, 0};
	SET_OFFSET_OF(
		&entry.counter_packet_recirc_drop,
		(struct counter_value_handle *)counters
	);

	device_entry_ectx_count_recirc_drop(&entry, &packet);

	TEST_ASSERT_EQUAL(counters[0], 1, "recirc drop packet count");
	TEST_ASSERT_EQUAL(
		counters[1], first_len + second_len, "recirc drop byte count"
	);
	rte_pktmbuf_free(first);
	return TEST_SUCCESS;
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
	src.recirc_remaining = 0;
	src.recirc_initialized = 0;

	struct packet *clone = worker_clone_packet(&dp_worker, &src, 5);
	int rc = assert_clone_correct(&src, clone, src.mbuf->pkt_len);
	if (rc == TEST_SUCCESS) {
		TEST_ASSERT_EQUAL(
			src.recirc_remaining,
			3,
			"uninitialized source must keep the odd credit"
		);
		TEST_ASSERT_EQUAL(
			clone->recirc_remaining,
			2,
			"uninitialized clone must receive half the configured "
			"credits"
		);
	}

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
		{"packet_alloc_resets_metadata",
		 test_packet_alloc_resets_metadata},
		{"recirc_drop_counts_full_packet",
		 test_recirc_drop_counts_full_packet},
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

	test_mempool_free(pool);

	if (failed == 0) {
		LOG(INFO, "all %zu worker_clone tests passed", total);
	} else {
		LOG(ERROR, "%zu/%zu worker_clone tests failed", failed, total);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
