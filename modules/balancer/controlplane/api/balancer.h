#pragma once

#include "handler.h"
#include "inspect.h"
#include "real.h"
#include "session.h"
#include "state.h"
#include "stats.h"
#include "vs.h"

#include "lib/errors/errors.h"

/**
 * Error handling
 *
 * Unless otherwise stated, each API function takes an optional yanet_error**
 * output parameter for error reporting. If an error occurs, *err is set to
 * a newly allocated error object. The caller is responsible for freeing it
 * with yanet_error_free().
 *
 * For creation-time failures the function returns NULL.
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

/**
 * Information about balancer configuration update operation.
 *
 * Provides visibility into filter reuse decisions made during
 * packet handler update. Helps understand configuration change
 * impact and optimization opportunities.
 */
struct balancer_update_info {
	/**
	 * IPv4 virtual service matcher was reused from previous handler.
	 *
	 * When true (non-zero): VS lookup filter for IPv4 was not recompiled
	 * When false (zero): VS lookup filter for IPv4 was recompiled
	 */
	int vs_ipv4_matcher_reused;

	/**
	 * IPv6 virtual service matcher was reused from previous handler.
	 *
	 * When true (non-zero): VS lookup filter for IPv6 was not recompiled
	 * When false (zero): VS lookup filter for IPv6 was recompiled
	 */
	int vs_ipv6_matcher_reused;

	/**
	 * Number of virtual services that reused ACL from previous handler.
	 *
	 * These VS did not need ACL recompilation because their
	 * allowed_src rules matched the previous configuration.
	 */
	size_t vs_acl_reused_count;

	/**
	 * Array of VS identifiers that reused ACL filters.
	 *
	 * Contains identifiers for virtual services where ACL was
	 * reused (not recompiled) from the previous handler.
	 *
	 * Array length is vs_acl_reused_count.
	 * Allocated by balancer_update_packet_handler().
	 * Caller must free with free().
	 */
	struct vs_identifier *vs_acl_reused;
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

struct yanet_error;

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
 * @param handle    Balancer handle.
 * @param new_size  New number of entries to allocate.
 * @param now       Current monotonic timestamp used for migration bookkeeping.
 * @param err       Error output parameter. Only set if function returns -1.
 * @return 0 on success, -1 on error.
 */
int
balancer_resize_session_table(
	struct balancer_handle *handle,
	size_t new_size,
	uint32_t now,
	yanet_error **err
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
 * Create a new balancer instance.
 *
 * Allocates memory and initializes internal data structures for a new load
 * balancer with the given configuration. The balancer will be owned by the
 * specified agent.
 *
 * @param agent   Agent that will own the balancer.
 * @param name    Human-readable balancer name (used for identification).
 * @param config  Initial configuration.
 * @param err     Error output parameter.
 * @return Newly created balancer handle on success, or NULL on error.
 */
struct balancer_handle *
balancer_create(
	struct agent *agent,
	const char *name,
	struct balancer_config *config,
	yanet_error **err
);

/**
 * Update the packet handler configuration for an existing balancer.
 *
 * @param handle     Balancer handle to update.
 * @param config     New packet handler configuration.
 * @param update_info Output parameter for update information (can be NULL).
 * @param err        Error output parameter.
 * @return 0 on success, -1 on error.
 */
int
balancer_update_packet_handler(
	struct balancer_handle *handle,
	struct packet_handler_config *config,
	struct balancer_update_info *update_info,
	yanet_error **err
);

/**
 * Free all allocations inside a balancer_update_info structure.
 *
 * Releases memory allocated by balancer_update_packet_handler() for the
 * vs_acl_reused array. Safe to call with partially-initialized structures;
 * ignores NULL pointers.
 *
 * NOTE: This function does NOT free the balancer_update_info structure itself,
 * only the dynamically allocated array inside it.
 *
 * @param update_info Structure to release. The struct itself is not freed.
 */
void
balancer_update_info_free(struct balancer_update_info *update_info);

/**
 * Apply a batch of real server updates.
 *
 * Each update may change weight and/or enabled state; to skip a field
 * use DONT_UPDATE_REAL_WEIGHT and DONT_UPDATE_REAL_ENABLED.
 *
 * @param balancer Balancer handle.
 * @param count    Number of updates in the array.
 * @param updates  Array of updates.
 * @param err      Error output parameter.
 * @return 0 on success, -1 on error.
 */
int
balancer_update_reals(
	struct balancer_handle *balancer,
	size_t count,
	struct real_update *updates,
	yanet_error **err
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
 * @param balancer Balancer handle.
 * @param stats    Output structure to be filled.
 * @param ref      Optional filter; pass NULL for aggregate stats.
 * @param err      Error output parameter. Only set if function returns -1.
 * @return 0 on success, -1 on error.
 */
int
balancer_stats(
	struct balancer_handle *balancer,
	struct balancer_stats *stats,
	struct packet_handler_ref *ref,
	yanet_error **err
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

void
balancer_active_sessions(
	struct balancer_handle *balancer, struct balancer_info *info
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
 * @param handle   Balancer handle.
 * @param real     Real server identifier (VS + real address/port).
 * @param real_idx Output structure to be filled with indices.
 * @param err      Error output parameter.
 * @return 0 on success, -1 on error (e.g., real not found).
 */
int
balancer_real_ph_idx(
	struct balancer_handle *handle,
	struct real_identifier *real,
	struct real_ph_index *real_idx,
	yanet_error **err
);

void
balancer_inspect(
	struct balancer_handle *handle, struct balancer_inspect *inspect
);

void
balancer_inspect_free(struct balancer_inspect *inspect);

void
balancer_active_sessions(
	struct balancer_handle *handle, struct balancer_info *info
);