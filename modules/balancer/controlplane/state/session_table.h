#pragma once

#include "common/memory.h"
#include "common/rcu.h"
#include "common/ttlmap/detail/ttlmap.h"

#include "api/session.h"

#include <assert.h>
#include <stdatomic.h>
#include <stdio.h>

////////////////////////////////////////////////////////////////////////////////

#define SESSION_FOUND TTLMAP_FOUND
#define SESSION_CREATED (TTLMAP_INSERTED | TTLMAP_REPLACED)
#define SESSION_TABLE_OVERFLOW TTLMAP_FAILED

/**
 * Lock-free session table with RCU-protected generation swapping.
 */
struct session_table {
	struct ttlmap maps[2]; // Active and previous maps

	rcu_t rcu;		      // RCU guard for map swaps
	_Atomic uint64_t current_gen; // Workers read, control-plane updates

	struct memory_context mctx; // Allocation context
};

static inline int
session_table_map_idx(uint32_t gen) {
	return ((gen + 1) & 0b11) >> 1;
}

static inline struct ttlmap *
session_table_map(struct session_table *table, uint32_t gen) {
	return &table->maps[session_table_map_idx(gen)];
}

static inline struct ttlmap *
session_table_prev_map(struct session_table *table, uint32_t gen) {
	return &table->maps[session_table_map_idx(gen) ^ 1];
}

static inline uint32_t
session_table_current_gen(struct session_table *table) {
	return atomic_load_explicit(&table->current_gen, memory_order_acquire);
}

/**
 * Initialize session table.
 * Returns 0 on success, -1 on error.
 */
int
session_table_init(
	struct session_table *table, struct memory_context *mctx, size_t size
);

/**
 * Free resources held by the session table.
 */
void
session_table_free(struct session_table *table);

/**
 * Current capacity (number of buckets/entries).
 */
size_t
session_table_capacity(struct session_table *table);

/**
 * Snapshot active sessions into a heap-allocated array.
 *
 * If only_count is true, counts sessions without allocating the list.
 *
 * Returns number of sessions on success, or (size_t)-1 on error.
 */
size_t
session_table_sessions_info(
	struct session_table *table,
	struct named_session_info **info,
	uint32_t now,
	bool only_count
);

/**
 * Try to resize session table.
 * Returns 0 on success, -1 on error (e.g., out of memory).
 */
int
session_table_resize(
	struct session_table *table, size_t new_size, uint32_t now
);