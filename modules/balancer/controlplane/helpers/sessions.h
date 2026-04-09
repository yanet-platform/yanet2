#pragma once

#include <stddef.h>
#include <stdint.h>

#include "common/ttlmap/ttlmap.h"

#include "modules/balancer/dataplane/types/session.h"

struct agent;
struct balancer_packet_handler;

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