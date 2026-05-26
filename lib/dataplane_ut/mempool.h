#pragma once

#include <rte_mbuf.h>
#include <rte_mempool.h>

#include <stdlib.h>
#include <string.h>

#include "yanet_build_config.h" // MBUF_MAX_SIZE

static int
test_pool_alloc(struct rte_mempool *mp) {
	(void)mp;
	rte_panic("unimplemented: pool's internal data alloc");
	return 0;
}

static void
test_pool_free(struct rte_mempool *mp) {
	(void)mp;
	rte_panic("unimplemented: pool's internal data free");
}

static int
test_pool_enqueue(struct rte_mempool *mp, void *const *obj_table, unsigned n) {
	for (unsigned idx = 0; idx < n; idx++) {
		free((char *)obj_table[idx] - mp->header_size);
	}

	return 0;
}

static int
test_pool_dequeue(struct rte_mempool *mp, void **obj_table, unsigned n) {
	for (unsigned idx = 0; idx < n; idx++) {
		void *ptr = aligned_alloc(64, mp->header_size + mp->elt_size);
		if (ptr == NULL) {
			rte_panic("failed to allocate object");
		}
		memset(ptr, 0, mp->header_size + mp->elt_size);

		struct rte_mempool_objhdr *hdr =
			(struct rte_mempool_objhdr
				 *)((char *)ptr + mp->header_size -
				    sizeof(struct rte_mempool_objhdr));
		hdr->mp = mp;
		hdr->iova =
			(rte_iova_t)(uintptr_t)((char *)ptr + mp->header_size);

		obj_table[idx] = (char *)ptr + mp->header_size;

		rte_pktmbuf_init(mp, NULL, obj_table[idx], 0);
	}
	return 0;
}

static unsigned
test_pool_get_count(const struct rte_mempool *mp) {
	(void)mp;
	return 1024;
}

static const struct rte_mempool_ops test_pool_ops = {
	.name = "test_pool_ops",
	.alloc = test_pool_alloc,
	.free = test_pool_free,
	.enqueue = test_pool_enqueue,
	.dequeue = test_pool_dequeue,
	.get_count = test_pool_get_count,
};

struct rte_mempool *
test_mempool_create(void) {
	rte_mempool_ops_table.num_ops = 0;
	rte_mempool_register_ops(&test_pool_ops);

	size_t private_data_size = sizeof(struct rte_pktmbuf_pool_private);
	struct rte_mempool *mp =
		calloc(1, sizeof(struct rte_mempool) + private_data_size);
	mp->flags |= RTE_MEMPOOL_F_POOL_CREATED;
	mp->socket_id = 0;
	mp->cache_size = 0;
	mp->elt_size = sizeof(struct rte_mbuf) + MBUF_MAX_SIZE;
	mp->header_size = sizeof(struct rte_mempool_objhdr);
	if (mp->header_size % 64 != 0) {
		mp->header_size += 64 - (mp->header_size % 64);
	}
	mp->private_data_size = private_data_size;
	rte_pktmbuf_pool_init(mp, NULL);
	return mp;
}
