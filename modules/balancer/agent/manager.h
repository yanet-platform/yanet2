#include <stddef.h>
#include <stdint.h>

#include "agent.h"
#include "modules/balancer/controlplane/api/balancer.h"

////////////////////////////////////////////////////////////////////////////////

/**
 * Weighted Least Connection (WLC) algorithm configuration.
 *
 * Configures the WLC scheduling algorithm parameters used by the manager
 * to distribute load across real servers.
 */
struct balancer_manager_wlc_config {
	size_t power; // Power factor for weight calculations

	size_t max_real_weight; // Maximum weight value for any real server

	size_t vs_count; // Number of virtual services in the array
	uint32_t *vs;	 // Array of virtual service IDs
};

/**
 * Complete configuration for a balancer manager.
 *
 * Combines balancer instance configuration with WLC algorithm parameters
 * and operational settings like refresh period and load thresholds.
 */
struct balancer_manager_config {
	struct balancer_config balancer; // Core balancer configuration

	struct balancer_manager_wlc_config wlc; // WLC algorithm settings

	uint32_t refresh_period; // Refresh interval in milliseconds

	float max_load_factor; // Maximum load factor (0.0 to 1.0)
};

struct balancer_handle;

/**
 * Opaque handle to a balancer manager instance.
 *
 * A manager coordinates one balancer instance, applying scheduling
 * algorithms (like WLC) and managing configuration updates. It provides
 * a higher-level interface for balancer lifecycle management.
 *
 * Thread-Safety: Not thread-safe. External synchronization required for
 * concurrent access.
 */
struct balancer_manager;

////////////////////////////////////////////////////////////////////////////////
// Query Operations
////////////////////////////////////////////////////////////////////////////////

/**
 * Get the name of the balancer manager.
 *
 * @param manager Manager handle.
 * @return Pointer to the manager name string (owned by the manager, do not
 * free).
 */
const char *
balancer_manager_name(struct balancer_manager *manager);

/**
 * Retrieve the current configuration of the manager.
 *
 * Fills the provided config structure with the manager's current settings.
 * The config structure should be allocated by the caller.
 *
 * @param manager Manager handle.
 * @param config  Output structure to be filled with current configuration.
 */
void
balancer_manager_config(
	struct balancer_manager *manager, struct balancer_manager_config *config
);

////////////////////////////////////////////////////////////////////////////////
// Update Operations
////////////////////////////////////////////////////////////////////////////////

/**
 * Update the manager's configuration.
 *
 * Applies a new configuration to the manager, updating balancer settings,
 * WLC parameters, refresh period, and load factor. This may trigger
 * reconfiguration of underlying balancer instances.
 *
 * Diagnostics: On error, a message is recorded and retrievable via
 * balancer_manager_take_error().
 *
 * @param manager Manager handle.
 * @param config  New configuration to apply.
 * @param now     Current monotonic timestamp for bookkeeping.
 * @return 0 on success, -1 on error.
 */
int
balancer_manager_update(
	struct balancer_manager *manager,
	struct balancer_manager_config *config,
	uint32_t now
);

/**
 * Apply a batch of real server updates.
 *
 * Updates the state (weight, enabled status) of one or more real servers
 * managed by this manager. Each update may change weight and/or enabled state.
 *
 * Diagnostics: On error, a message is recorded and retrievable via
 * balancer_manager_take_error().
 *
 * @param manager Manager handle.
 * @param count   Number of updates in the array.
 * @param updates Array of real server updates to apply.
 * @return 0 on success, -1 on error.
 */
int
balancer_manager_update_reals(
	struct balancer_manager *manager,
	size_t count,
	struct real_update *updates
);

/**
 * Apply a batch of real server weight updates for WLC algorithm.
 *
 * Similar to balancer_manager_update_reals(), but specifically for WLC
 * algorithm updates. This function:
 * - Only updates the state/graph weights, NOT the config weights
 * - Validates that updates only change weights (not enable state)
 * - Preserves the original static config weights for WLC calculations
 *
 * The config weight remains the baseline for WLC calculations, while the
 * state weight is dynamically adjusted based on load.
 *
 * Diagnostics: On error, a message is recorded and retrievable via
 * balancer_manager_take_error().
 *
 * @param manager Manager handle.
 * @param count   Number of updates in the array.
 * @param updates Array of real server weight updates (must not change enable
 * state).
 * @return 0 on success, -1 on error.
 */
int
balancer_manager_update_reals_wlc(
	struct balancer_manager *manager,
	size_t count,
	struct real_update *updates
);

/**
 * Resize the session table used by the manager's balancer.
 *
 * Changes the capacity of the session table to accommodate more or fewer
 * concurrent sessions. This operation may involve memory reallocation and
 * session migration.
 *
 * Diagnostics: On error, a message is recorded and retrievable via
 * balancer_manager_take_error().
 *
 * @param manager  Manager handle.
 * @param new_size New number of session table entries to allocate.
 * @param now      Current monotonic timestamp for migration bookkeeping.
 * @return 0 on success, -1 on error.
 */
int
balancer_manager_resize_session_table(
	struct balancer_manager *manager, size_t new_size, uint32_t now
);

////////////////////////////////////////////////////////////////////////////////
// Statistics and Information Retrieval
////////////////////////////////////////////////////////////////////////////////

/**
 * Query aggregated balancer information from the manager.
 *
 * Retrieves comprehensive information including active sessions, virtual
 * services, and real server states. On success, allocates arrays inside
 * the info structure that must be freed with balancer_manager_info_free().
 *
 * Diagnostics: On error, a message is recorded and retrievable via
 * balancer_manager_take_error().
 *
 * @param manager Manager handle.
 * @param info    Output structure to be filled with balancer information.
 * @param now     Current monotonic timestamp for time-based calculations.
 * @return 0 on success, -1 on error.
 */
int
balancer_manager_info(
	struct balancer_manager *manager,
	struct balancer_info *info,
	uint32_t now
);

/**
 * Enumerate active sessions tracked by the manager's balancer.
 *
 * Returns a snapshot of all active sessions. The sessions structure will
 * contain heap-allocated data that must be freed with
 * balancer_manager_sessions_free().
 *
 * @param manager  Manager handle.
 * @param sessions Output structure to be filled with session information.
 * @param now      Current monotonic timestamp for session state evaluation.
 */
void
balancer_manager_sessions(
	struct balancer_manager *manager,
	struct sessions *sessions,
	uint32_t now
);

/**
 * Read balancer statistics from the manager.
 *
 * Retrieves statistics for the manager's balancers, optionally filtered
 * by packet handler reference. On success, allocates data inside the stats
 * structure that must be freed with balancer_manager_stats_free().
 *
 * Diagnostics: On error, a message is recorded and retrievable via
 * balancer_manager_take_error().
 *
 * @param manager Manager handle.
 * @param stats   Output structure to be filled with statistics.
 * @param ref     Optional filter for specific packet handler; pass NULL for
 * aggregate.
 * @return 0 on success, -1 on error.
 */
int
balancer_manager_stats(
	struct balancer_manager *manager,
	struct balancer_stats *stats,
	struct packet_handler_ref *ref
);

/**
 * Retrieve graph representation of the manager's balancer topology.
 *
 * Generates a graph structure representing the relationships between
 * virtual services, real servers, and their connections. The graph structure
 * must be freed with balancer_manager_graph_free().
 *
 * @param manager Manager handle.
 * @param graph   Output structure to be filled with graph data.
 */
void
balancer_manager_graph(
	struct balancer_manager *manager, struct balancer_graph *graph
);

////////////////////////////////////////////////////////////////////////////////
// Memory Management
////////////////////////////////////////////////////////////////////////////////

/**
 * Free all allocations inside a balancer_info structure.
 *
 * Releases memory allocated by balancer_manager_info(). Safe to call with
 * partially-initialized structures; ignores NULL pointers.
 *
 * @param info Structure to release. The struct itself is not freed.
 */
void
balancer_manager_info_free(struct balancer_info *info);

/**
 * Free all allocations inside a sessions structure.
 *
 * Releases memory allocated by balancer_manager_sessions(). Safe to call
 * with partially-initialized structures; ignores NULL pointers.
 *
 * @param sessions Structure to release. The struct itself is not freed.
 */
void
balancer_manager_sessions_free(struct sessions *sessions);

/**
 * Free all allocations inside a balancer_stats structure.
 *
 * Releases memory allocated by balancer_manager_stats(). Safe to call with
 * partially-initialized structures; ignores NULL pointers.
 *
 * @param stats Structure to release. The struct itself is not freed.
 */
void
balancer_manager_stats_free(struct balancer_stats *stats);

/**
 * Free all allocations inside a balancer_graph structure.
 *
 * Releases memory allocated by balancer_manager_graph().
 *
 * @param graph Structure to release. The struct itself is not freed.
 */
void
balancer_manager_graph_free(struct balancer_graph *graph);

////////////////////////////////////////////////////////////////////////////////
// Error Handling
////////////////////////////////////////////////////////////////////////////////

/**
 * Retrieve the last diagnostic error message for this manager.
 *
 * Returns the most recent error message recorded by manager operations.
 * After calling this function, the error state is cleared.
 *
 * Ownership: The returned string is heap-allocated for the caller; you must
 * free() it when no longer needed. Returns NULL if no error is available.
 *
 * @param manager Manager handle.
 * @return Null-terminated error message string to be freed by caller, or NULL.
 */
const char *
balancer_manager_take_error(struct balancer_manager *manager);
