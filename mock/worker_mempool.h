#pragma once
#include <rte_mbuf.h>
#include <rte_mempool.h>

////////////////////////////////////////////////////////////////////////////////
// mempool ops

static int
mock_pool_alloc(struct rte_mempool *mp) {
	(void)mp;
	rte_panic("unimplemented: pool's internal data alloc");
	return 0;
}

static void
mock_pool_free(struct rte_mempool *mp) {
	(void)mp;
	rte_panic("unimplemented: pool's internal data free");
}

static int
mock_pool_enqueue(struct rte_mempool *mp, void *const *obj_table, unsigned n) {
	for (unsigned i = 0; i < n; i++) {
		free((char *)obj_table[i] - mp->header_size);
	}
	return 0;
}

static int
mock_pool_dequeue(struct rte_mempool *mp, void **obj_table, unsigned n) {
	for (unsigned i = 0; i < n; i++) {
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

		obj_table[i] = (char *)ptr + mp->header_size;

		// Set the fields of a packet mbuf to their default values.
		rte_pktmbuf_init(mp, NULL, obj_table[i], 0);
	}
	return 0;
}

static unsigned
mock_pool_get_count(const struct rte_mempool *mp) {
	(void)mp;
	return 1024; // dummy number
}

static const struct rte_mempool_ops mock_pool_ops = {
	.name = "mock_pool_ops",
	.alloc = mock_pool_alloc,
	.free = mock_pool_free,
	.enqueue = mock_pool_enqueue,
	.dequeue = mock_pool_dequeue,
	.get_count = mock_pool_get_count,
};

////////////////////////////////////////////////////////////////////////////////

////////////////////////////////////////////////////////////////////////////
// mempool initialization

struct rte_mempool *
mock_mempool_create() {

	// force reset number of ops in global registry, okay for tests
	rte_mempool_ops_table.num_ops = 0;
	// register mock pool ops at index 0
	rte_mempool_register_ops(&mock_pool_ops);

	size_t private_data_size = sizeof(struct rte_pktmbuf_pool_private);
	struct rte_mempool *mp =
		calloc(1, sizeof(struct rte_mempool) + private_data_size);
	mp->flags |= RTE_MEMPOOL_F_POOL_CREATED;
	mp->socket_id = 0;
	mp->cache_size = 0; // cache size is zero we always calloc data
	mp->elt_size = sizeof(struct rte_mbuf) + RTE_MBUF_DEFAULT_BUF_SIZE;
	mp->header_size = sizeof(struct rte_mempool_objhdr);
	if (mp->header_size % 64 != 0) {
		mp->header_size += 64 - (mp->header_size % 64);
	}
	mp->private_data_size = private_data_size;
	rte_pktmbuf_pool_init(mp, NULL);
	return mp;
}
////////////////////////////////////////////////////////////////////////////
