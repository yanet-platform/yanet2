#pragma once

#include <stddef.h>
#include <stdint.h>

#include "common/ttlmap/ttlmap.h"

#include "modules/balancer/dataplane/types/session.h"

struct agent;
struct balancer_packet_handler;

/*
 * Resize the session table by allocating a new ttlmap and migrating
 * existing sessions into it.
 *
 * Uses a two-phase gen-bump protocol coordinated with the dataplane:
 *
 *   gen (even)  — steady state, dataplane uses map selected by gen.
 *   gen+1 (odd) — transition: the new map is active for writes,
 *                 dataplane must look up both maps for existing sessions.
 *   gen+2 (even) — migration complete, old map freed.
 *
 * The dataplane checks current_gen atomically on every packet:
 *   - Even gen: use balancer_st_cur_map(st, gen) only.
 *   - Odd gen:  write to the new map, but fall back to both maps for
 *               lookups so in-flight sessions are not lost.
 *
 * Returns 0 on success, -1 on allocation failure.
 */
int
balancer_st_resize(
	struct balancer_session_table *st, size_t new_size, uint32_t now
);

size_t
balancer_st_capacity(struct balancer_session_table *st);

struct balancer_session_table_iter {
	struct ttlmap_bucket_iter ttlmap_iter;
	uint32_t gen;
};

void
balancer_st_iter_init(
	struct balancer_session_table_iter *iter,
	struct balancer_session_table *st
);

typedef int (*balancer_st_iter_callback)(
	struct balancer_session_id *id,
	struct balancer_session_state *state,
	void *userdata
);

// Returns 0 on error
int
balancer_st_iter_next_bucket(
	struct balancer_session_table_iter *iter,
	uint32_t now,
	balancer_st_iter_callback cb,
	void *userdata
);

struct balancer_session_entry {
	struct balancer_session_id id;
	struct balancer_session_state state;
};

// Advances iterator to next bucket, fills buf with non-expired entries.
// Sets *count to number of valid entries. Returns 0 when no more buckets.
int
balancer_st_iter_next_bucket_buf(
	struct balancer_session_table_iter *iter,
	uint32_t now,
	struct balancer_session_entry *buf,
	int *count
);