#pragma once

#include <stddef.h>
#include <stdint.h>

#include "common/big_array.h"

/**
 * Ring containing real indices.
 *
 * Each backend appears multiple times according to its weight.
 * The ring is shuffled to distribute selections evenly.
 */
struct ring {
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
struct rr_counter {
	uint64_t value;
} __attribute__((aligned(64)));

/**
 * Real backend selector.
 *
 * Maintains two rings for RCU-swapped updates and per-worker RR counters.
 * Uses either round-robin or hash-based selection,
 * depending on the virtual server scheduler.
 */
struct real_selector {
	/* Double-buffered rings. */
	struct ring rings[2];

	/* Active ring index. */
	_Atomic uint64_t ring_id;

	/* TODO: docs */
	uint64_t packet_hash_mask;

	/* Array of per-worker round-robin counters. */
	struct rr_counter workers_rr_counter[];
};
