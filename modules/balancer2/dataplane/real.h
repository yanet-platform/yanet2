#pragma once

#include <stdatomic.h>
#include <stdbool.h>

#include "common/network.h"

#include "lib/counters/counters.h"

enum real_flags {
	real_enabled = 1u << 0,
	real_ip6 = 1u << 1,
};

/* A real (backend) server within a virtual service. */
struct real {
	/* Destination IP address of the real server (IPv4 or IPv6). */
	struct net_addr addr;

	/*
	 * Source network for the outer tunnel header.
	 *
	 * Contains both the base address (src.v4.addr / src.v6.addr)
	 * and the mask (src.v4.mask / src.v6.mask).
	 *
	 * INVARIANT: the address bytes must be pre-masked by the
	 * controlplane, i.e. (addr[i] & mask[i]) == addr[i] for every
	 * byte i. The tunnel code relies on this to embed client source
	 * IP bits into the unmasked positions without an extra AND:
	 *
	 *   outer_src[i] = addr[i] | (client_src[i] & ~mask[i])
	 *
	 * Use v4 when real_ip6 is clear, v6 when set.
	 */
	struct net src;

	uint64_t counter_id;

	_Atomic uint8_t flags;
};

static inline struct balancer_real_stats *
real_fetch_stats(
	struct real *real,
	uint32_t worker,
	struct counter_storage *counter_storage
) {
	return (struct balancer_real_stats *)counter_get_address(
		real->counter_id, worker, counter_storage
	);
}

static inline uint8_t
real_flags(struct real *real) {
	return atomic_load_explicit(&real->flags, memory_order_relaxed);
}