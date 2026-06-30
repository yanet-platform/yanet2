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
	 * The control plane guarantees the host bits are already cleared,
	 * so the tunnel code can embed client source bits into the unmasked
	 * positions with a single bitwise-OR, without an extra mask step.
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
