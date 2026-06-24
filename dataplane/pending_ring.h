#pragma once

#include <stdint.h>

struct rte_mbuf;

// One slot in the pending-mbuf ring.
//
// Stores the mbuf and the refcount baseline at push time so the sweep can
// decide when the consumer has released its reference.
struct worker_pending_mbuf {
	struct rte_mbuf *mbuf;
	uint64_t ref_cnt;
};

// Push one mbuf into the pending ring.
//
// Returns 0 on success. Returns -1 when the ring is at capacity
// (depth == num_slots) without writing; the caller is responsible for
// freeing the mbuf rather than forwarding it.
static inline int
pending_ring_push(
	struct worker_pending_mbuf *ring,
	uint64_t *start,
	uint64_t *stop,
	uint32_t num_slots,
	struct rte_mbuf *mbuf,
	uint64_t ref_cnt
) {
	if (*stop - *start >= num_slots) {
		return -1;
	}
	uint32_t ofs = (uint32_t)(*stop % num_slots);
	ring[ofs].mbuf = mbuf;
	ring[ofs].ref_cnt = ref_cnt;
	++(*stop);
	return 0;
}
