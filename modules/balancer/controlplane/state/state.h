#pragma once

#include "common/memory.h"

#include "session_table.h"

#include "api/inspect.h"

////////////////////////////////////////////////////////////////////////////////

/**
 * Persistent balancer state.
 * Holds registries (VS/reals), session table and per-worker stats.
 */
struct balancer_state {
	// number of workers
	size_t workers;

	// session table
	struct session_table session_table;
};

////////////////////////////////////////////////////////////////////////////////

/**
 * Initialize balancer_state.
 * Returns 0 on success, -1 on error.
 */
int
balancer_state_init(
	struct balancer_state *state,
	struct memory_context *mctx,
	size_t workers,
	size_t table_size
);

/**
 * Free resources held by balancer_state.
 */
void
balancer_state_free(struct balancer_state *state);

////////////////////////////////////////////////////////////////////////////////

/**
 * Resize session table; returns 0 on success, -1 on error.
 */
int
balancer_state_resize_session_table(
	struct balancer_state *state, size_t new_size, uint32_t now
);

// TODO: docs
int
balancer_state_iter_session_table(
	struct balancer_state *state,
	uint32_t now,
	session_table_iter_callback cb,
	void *userdata
);

// TODO: docs
void
balancer_state_inspect(
	struct balancer_state *state, struct state_inspect *inspect
);