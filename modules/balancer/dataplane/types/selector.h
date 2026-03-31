#pragma once

#include <stddef.h>
#include <stdint.h>

#include "common/big_array.h"
#include "common/rcu.h"

/**
 * Ring containing backend ("real") indices.
 *
 * Each backend appears multiple times according to its weight.
 * The ring is shuffled to distribute selections evenly.
 */
struct balancer_ring {
	/*
	 * Indices of backend servers.
	 *
	 * Stored in a big array because the weighted list can exceed
	 * the allocator's maximum block size.
	 */
	struct big_array real_ids;
};

/**
 * Round-robin counter.
 *
 * Used to track the position of the current real in the ring.
 */
struct balancer_rr_counter {
	uint64_t value;
} __attribute__((aligned(64)));

/**
 * Real backend selector.
 *
 * Maintains two rings for RCU-swapped updates and per-worker RR counters.
 * Uses either round-robin or hash-based selection,
 * depending on the virtual server scheduler.
 */
struct balancer_real_selector {
	/* RCU guard for ring swaps. */
	rcu_t rcu;

	/* Double-buffered rings. */
	struct balancer_ring rings[2];

	/* Active ring index. */
	_Atomic size_t ring_id;

	/* Non-zero for RR scheduler, zero for hash scheduler. */
	int use_rr;

	/* Relative pointer to the array of per-worker round-robin counters. */
	struct balancer_rr_counter *worker_rr_counter;
};
