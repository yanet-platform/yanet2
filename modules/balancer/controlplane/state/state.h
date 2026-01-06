#pragma once

#include "common/memory.h"

#include "registry.h"
#include "session_table.h"

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

	// registry of virtual services
	struct service_registry vs_registry;

	// registry of reals
	struct service_registry real_registry;
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
 * Find or insert a virtual service into the registry.
 * Returns pointer to VS; inserts if absent.
 */
struct vs_state *
balancer_state_find_or_insert_vs(
	struct balancer_state *state, struct vs_identifier *id
);

/**
 * Find virtual service by identifier; returns NULL if not found.
 */
struct vs_state *
balancer_state_find_vs(struct balancer_state *state, struct vs_identifier *id);

/**
 * Get virtual service by registry index.
 */
struct vs_state *
balancer_state_get_vs_by_idx(struct balancer_state *state, size_t idx);

////////////////////////////////////////////////////////////////////////////////

/**
 * Find or insert a real into the registry.
 * Returns pointer to real; inserts if absent.
 */
struct real_state *
balancer_state_find_or_insert_real(
	struct balancer_state *state, struct real_identifier *id
);

/**
 * Find real by identifier; returns NULL if not found.
 */
struct real_state *
balancer_state_find_real(
	struct balancer_state *state, struct real_identifier *id
);

/**
 * Get real by registry index.
 */
struct real_state *
balancer_state_get_real_by_idx(struct balancer_state *state, size_t idx);

////////////////////////////////////////////////////////////////////////////////

/**
 * Number of reals in the registry.
 */
size_t
balancer_state_reals_count(struct balancer_state *state);

/**
 * Number of virtual services in the registry.
 */
size_t
balancer_state_vs_count(struct balancer_state *state);

////////////////////////////////////////////////////////////////////////////////

/**
 * Resize session table; returns 0 on success, -1 on error.
 */
int
balancer_state_resize_session_table(
	struct balancer_state *state, size_t new_size, uint32_t now
);

int
balancer_state_iter_session_table(
	struct balancer_state *state,
	uint32_t now,
	session_table_iter_callback cb,
	void *userdata
);
