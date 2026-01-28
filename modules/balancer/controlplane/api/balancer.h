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
	/**  Packet handling/session parameters */
	struct packet_handler_config handler;

	/** Session table sizing/config */
	struct state_config state;
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

/**
 * Get the current session table capacity.
 *
 * Returns the current maximum number of concurrent sessions the session
 * table can hold. This is the hash table size, not the number of active
 * sessions.
 *
 * The capacity can change over time due to:
 * - Manual resizing via balancer_resize_session_table()
 * - Automatic resizing when load factor exceeds threshold
 *
 * @param handle Balancer handle.
 * @return Current session table capacity (number of entries).
 */
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

/**
 * Free all allocations inside a balancer_stats structure.
 *
 * Releases memory allocated by balancer_stats() for the VS and real
 * statistics arrays. Safe to call with partially-initialized structures;
 * ignores NULL pointers.
 *
 * NOTE: This function does NOT free the balancer_stats structure itself,
 * only the dynamically allocated arrays inside it.
 *
 * @param stats Structure to release. The struct itself is not freed.
 */
void
balancer_stats_free(struct balancer_stats *stats);

/**
 * Aggregated information about a balancer instance.
 *
 * Provides a comprehensive snapshot of the balancer's operational state,
 * including active session counts, last activity timestamp, and detailed
 * information about all virtual services and their real servers.
 *
 * DATA FRESHNESS:
 * - active_sessions: Updated during periodic refresh (if enabled) or on-demand
 * - last_packet_timestamp: Real-time from dataplane
 * - vs array: Contains per-VS and per-real runtime information
 *
 * MEMORY MANAGEMENT:
 * - balancer_info() allocates the 'vs' array and all nested structures
 * - Caller must call balancer_info_free() to release all allocations
 * - Safe to call balancer_info_free() on partially-initialized structures
 *
 * USAGE PATTERN:
 * ```c
 * struct balancer_info info;
 * if (balancer_info(handle, &info, now) == 0) {
 *     // Use info.active_sessions, info.vs, etc.
 *     balancer_info_free(&info);
 * }
 * ```
 */
struct balancer_info {
	/**
	 * Total number of active sessions across all virtual services.
	 *
	 * This is the sum of active sessions for all VSs and represents
	 * the current load on the balancer.
	 */
	size_t active_sessions;

	/**
	 * Timestamp of the most recent packet processed by any VS.
	 *
	 * Monotonic timestamp (seconds since boot) representing the last
	 * activity across the entire balancer instance. This is the maximum
	 * of all VS last_packet_timestamp values.
	 *
	 * Updated in real-time by the dataplane when packets are processed.
	 */
	uint32_t last_packet_timestamp;

	/**
	 * Number of virtual services in the 'vs' array.
	 *
	 * This matches the number of virtual services configured in the
	 * packet handler configuration.
	 */
	size_t vs_count;

	/**
	 * Array of virtual service runtime information.
	 *
	 * Contains detailed information for each VS including:
	 * - Active session counts per VS
	 * - Per-real server information (active sessions, last activity)
	 * - Last packet timestamps
	 *
	 * OWNERSHIP:
	 * - Allocated by balancer_info()
	 * - Must be freed with balancer_info_free()
	 * - Array length is vs_count
	 */
	struct named_vs_info *vs;
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

/**
 * Free all allocations inside a sessions structure.
 *
 * Releases memory allocated by balancer_sessions() for the session
 * information array. Safe to call with partially-initialized structures;
 * ignores NULL pointers.
 *
 * NOTE: This function does NOT free the sessions structure itself,
 * only the dynamically allocated array inside it.
 *
 * @param sessions Structure to release. The struct itself is not freed.
 */
void
balancer_sessions_free(struct sessions *sessions);

struct balancer_graph;

/**
 * Retrieve the balancer topology graph.
 *
 * Returns a snapshot of the complete balancer topology showing all
 * virtual services and their real servers with current operational
 * states (effective weights, enabled status).
 *
 * The graph provides visibility into:
 * - Current effective weights (may differ from config due to WLC)
 * - Real server enabled/disabled states
 * - Complete VS-to-real relationships
 *
 * MEMORY MANAGEMENT:
 * - Allocates memory for the graph structure and all nested arrays
 * - Caller must free with balancer_graph_free() when done
 * - Safe to call balancer_graph_free() on partially-initialized graphs
 *
 * USAGE PATTERN:
 * ```c
 * struct balancer_graph graph;
 * balancer_graph(handle, &graph);
 * // Use graph data...
 * balancer_graph_free(&graph);
 * ```
 *
 * @param handle Balancer handle.
 * @param graph  Output structure to be filled with graph data.
 */
void
balancer_graph(struct balancer_handle *handle, struct balancer_graph *graph);

/**
 * Free all allocations inside a balancer_graph structure.
 *
 * Releases memory allocated by balancer_graph() for the virtual service
 * and real server arrays. This includes:
 * - The top-level VS array (graph->vs)
 * - Each VS's real server array (vs->reals)
 *
 * Safe to call with partially-initialized structures; ignores NULL pointers.
 *
 * NOTE: This function does NOT free the balancer_graph structure itself,
 * only the dynamically allocated arrays inside it.
 *
 * @param graph Structure to release. The struct itself is not freed.
 */
void
balancer_graph_free(struct balancer_graph *graph);

////////////////////////////////////////////////////////////////////////////////

/**
 * Get packet handler indices for a real server.
 *
 * Translates a real server identifier (VS + real) into packet handler
 * internal indices. This is useful for low-level operations that need
 * to directly access packet handler data structures.
 *
 * The returned indices identify:
 * - vs_idx: Index of the virtual service in the packet handler's VS array
 * - real_idx: Index of the real within that virtual service's real array
 *
 * These indices can be used to:
 * - Access real server configuration in packet handler structures
 * - Perform direct updates to packet handler state
 * - Map between high-level identifiers and internal indices
 *
 * USAGE:
 * This is primarily an internal API used by the manager layer to
 * coordinate between the high-level balancer API and the low-level
 * packet handler implementation.
 *
 * Diagnostics: On error, a message is recorded and retrievable via
 * balancer_take_error_msg(handle).
 *
 * @param handle   Balancer handle.
 * @param real     Real server identifier (VS + real address/port).
 * @param real_idx Output structure to be filled with indices.
 * @return 0 on success, -1 on error (e.g., real not found).
 */
int
balancer_real_ph_idx(
	struct balancer_handle *handle,
	struct real_identifier *real,
	struct real_ph_index *real_idx
);