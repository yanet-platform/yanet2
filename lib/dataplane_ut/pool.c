#include "pool.h"

#include "mempool.h"

struct rte_mempool *
dataplane_ut_pool_new(void) {
	return test_mempool_create_sized(DATAPLANE_UT_POOL_DEFAULT_CAPACITY);
}

size_t
dataplane_ut_pool_outstanding(const struct rte_mempool *mempool) {
	return test_mempool_outstanding(mempool);
}

void
dataplane_ut_pool_set_capacity(struct rte_mempool *mempool, uint32_t capacity) {
	mempool->size = capacity;
}
