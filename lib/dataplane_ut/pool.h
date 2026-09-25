#pragma once

#include <stddef.h>
#include <stdint.h>

struct rte_mempool;

// Capacity of a pool dataplane_ut_pool_new returns.
#define DATAPLANE_UT_POOL_DEFAULT_CAPACITY 1024

// Create a mock mbuf pool for a harness built without the DPDK include
// paths, as cgo builds module test harnesses.
//
// Returns NULL on allocation failure.
struct rte_mempool *
dataplane_ut_pool_new(void);

// Number of mbufs taken from the pool and not yet returned.
size_t
dataplane_ut_pool_outstanding(const struct rte_mempool *mempool);

// Limit how many mbufs the pool hands out at once: an allocation that
// would push the outstanding count past capacity fails.
void
dataplane_ut_pool_set_capacity(struct rte_mempool *mempool, uint32_t capacity);
