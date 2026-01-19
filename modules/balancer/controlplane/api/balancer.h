#pragma once

#include "handler.h"
#include "real.h"
#include "session.h"
#include "state.h"
#include "stats.h"
#include "vs.h"

/**
 * Diagnostics
 *
 * Unless otherwise stated, on error each API function records a human-readable
 * diagnostic message associated with the balancer handle. Retrieve it via
 * balancer_take_error_msg().
 *
 * Ownership: The returned message is heap-allocated and must be freed by the
 * caller with free().
 *
 * For creation-time failures use the diag parameter of balancer_create().
 */

/**
 * Balancer module configuration.
 *
 * Combines packet handler configuration and session/state configuration
 * required to instantiate a balancer instance.
 */
struct balancer_config {
	struct packet_handler_config
		handler;	   // Packet handling/session parameters
	struct state_config state; // Session table sizing/config
};

struct agent;

/**
 * Opaque handle to a balancer instance.
 *
 * The handle is returned by balancer_create() and balancers() and is used with
 * all other API calls. Its internals are private to the implementation.
 *
 * Thread-Safety: Does not allow multithreading access.
 * Safe to work concurrently with the controlplane and dataplane.
 */
struct balancer_handle;

// TODO: docs
struct diag;

/**
 * Create a new balancer instance and register it.
 *
 * On success returns a handle to the created balancer. On failure returns
 * NULL and records diagnostic information in the provided diag object.
 *
 * Diagnostics: On error, details are written into 'diag'. After a successful
 * creation, subsequent API calls record diagnostics on the balancer and can be
 * retrieved via balancer_take_error_msg().
 *
 * @param agent   Agent that will own the balancer.
 * @param name    Human-readable balancer name (used for identification).
 * @param config  Initial configuration.
 * @param diag    Diagnostics sink for error details (must not be NULL).
 * @return Newly created balancer handle on success, or NULL on error.
 */
struct balancer_handle *
balancer_create(
	struct agent *agent, const char *name, struct balancer_config *config
);

/**
 * Retrieve the last diagnostic error message for this balancer.
 *
 * Ownership: The returned string is heap-allocated for the caller; you must
 * free() it when no longer needed. Returns NULL if no message is available.
 *
 * @param handle Balancer handle.
 * @return Null-terminated error message string to be freed by caller, or NULL.
 */
const char *
balancer_take_error_msg(struct balancer_handle *handle);

/**
 * Get the name of the balancer instance.
 *
 * @param handle Balancer handle.
 * @return Pointer to the balancer name string (owned by the balancer, do not
 * free).
 */
const char *
balancer_name(struct balancer_handle *handle);

// Update

/**
 * Resize the session table used by the balancer.
 *
 * Diagnostics: On error, a message is recorded and retrievable via
 * balancer_take_error_msg(handle).
 *
 * @param handle    Balancer handle.
 * @param new_size  New number of entries to allocate.
 * @param now       Current monotonic timestamp used for migration bookkeeping.
 * @return 0 on success, -1 on error.
 */
int
balancer_resize_session_table(
	struct balancer_handle *handle, size_t new_size, uint32_t now
);

size_t
balancer_session_table_capacity(struct balancer_handle *handle);

/**
 * Update packet handler configuration.
 *
 * This call applies changes such as timeouts, VS list or source addresses.
 *
 * Diagnostics: On error, a message is recorded and retrievable via
 * balancer_take_error_msg(balancer).
 *
 * @param balancer Balancer handle.
 * @param config   New packet handler configuration.
 * @return 0 on success, -1 on error.
 */
int
balancer_update_packet_handler(
	struct balancer_handle *balancer, struct packet_handler_config *config
);

/**
 * Apply a batch of real server updates.
 *
 * Each update may change weight and/or enabled state; to skip a field
 * use DONT_UPDATE_REAL_WEIGHT and DONT_UPDATE_REAL_ENABLED.
 *
 * Diagnostics: On error, a message is recorded and retrievable via
 * balancer_take_error_msg(balancer).
 *
 * @param balancer Balancer handle.
 * @param count    Number of updates in the array.
 * @param updates  Array of updates.
 * @return 0 on success, -1 on error.
 */
int
balancer_update_reals(
	struct balancer_handle *balancer,
	size_t count,
	struct real_update *updates
);

// Stats

/**
 * Optional reference to narrow statistics to a particular packet handler
 * attachment point.
 *
 * Any field may be NULL to indicate no filtering on that dimension.
 */
struct packet_handler_ref {
	const char *device;   // Optional device name (NULL for any)
	const char *pipeline; // Optional pipeline name (NULL for any)
	const char *function; // Optional function name (NULL for any)
	const char *chain;    // Optional chain name (NULL for any)
};

/**
 * Read balancer statistics, optionally filtered by packet handler reference.
 *
 * Diagnostics: On error, a message is recorded and retrievable via
 * balancer_take_error_msg(balancer).
 *
 * @param balancer Balancer handle.
 * @param stats    Output structure to be filled.
 * @param ref      Optional filter; pass NULL for aggregate stats.
 * @return 0 on success, -1 on error.
 */
int
balancer_stats(
	struct balancer_handle *balancer,
	struct balancer_stats *stats,
	struct packet_handler_ref *ref
);

void
balancer_stats_free(struct balancer_stats *stats);

/**
 * Aggregated information about a balancer instance.
 *
 * Includes statistics, active session count and snapshots of VS/real info.
 *
 * Memory management:
 * - On success balancer_info() allocates arrays referenced by vs and reals.
 * - Release all allocations inside this struct with balancer_info_free().
 */
struct balancer_info {
	size_t active_sessions; // Total number of active sessions
	uint32_t last_packet_timestamp;

	size_t vs_count;	  // Number of entries in 'vs'
	struct named_vs_info *vs; // Array of VS info (length: vs_count)
};

/**
 * Query aggregated balancer information.
 *
 * On success fills the provided structure and allocates arrays inside it.
 * Release all memory with balancer_info_free().
 *
 * Diagnostics: On error, a message is recorded and retrievable via
 * balancer_take_error_msg(balancer).
 *
 * @param balancer Balancer handle.
 * @param info     Output structure to be filled.
 * @return 0 on success, -1 on error.
 */
int
balancer_info(
	struct balancer_handle *balancer,
	struct balancer_info *info,
	uint32_t now
);

/**
 * Free all allocations inside a balancer_info previously filled by
 * balancer_info().
 *
 * Safe to call with partially-initialized structures; ignores NULL pointers.
 *
 * @param info Structure to release. The struct itself is not freed.
 */
void
balancer_info_free(struct balancer_info *info);

/**
 * Enumerate active sessions tracked by the balancer.
 *
 * Returns a heap-allocated array of named_session_info entries representing
 * a point-in-time snapshot. The caller owns the array and must
 * balancer_sessions_free() it.
 *
 * Diagnostics: On error, a message is recorded and retrievable via
 * balancer_take_error_msg(balancer).
 *
 * @param balancer Balancer handle.
 * @param sessions Output pointer to a heap-allocated array of session infos.
 * @return Number of entries on success
 */
void
balancer_sessions(
	struct balancer_handle *balancer,
	struct sessions *sessions,
	uint32_t now
);

void
balancer_sessions_free(struct sessions *sessions);

struct balancer_graph;

void
balancer_graph(struct balancer_handle *handle, struct balancer_graph *graph);

void
balancer_graph_free(struct balancer_graph *graph);

////////////////////////////////////////////////////////////////////////////////

int
balancer_real_ph_idx(
	struct balancer_handle *handle,
	struct real_identifier *real,
	struct real_ph_index *real_idx
);