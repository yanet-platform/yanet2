#include <stddef.h>
#include <stdint.h>

#include "agent.h"
#include "modules/balancer/controlplane/api/balancer.h"

////////////////////////////////////////////////////////////////////////////////

/**
 * Weighted Least Connection (WLC) algorithm configuration.
 *
 * Configures the WLC scheduling algorithm that dynamically adjusts real
 * server weights based on active session counts to achieve better load
 * distribution. The WLC algorithm is particularly useful when session
 * durations vary significantly, preventing overloading of individual reals.
 *
 * ALGORITHM OVERVIEW:
 * The WLC algorithm calculates effective weights using this formula:
 *
 *   effective_weight = min(
 *       config_weight * max(1.0, power * (1.0 - connections_ratio)),
 *       max_real_weight
 *   )
 *
 * where:
 *   connections_ratio = (real_sessions * total_weight) /
 *                       (total_sessions * real_weight)
 *
 * BEHAVIOR:
 * - If a real has fewer sessions than expected (ratio < 1.0):
 *   Weight increases to attract more traffic
 * - If a real has more sessions than expected (ratio > 1.0):
 *   Weight stays at baseline (no decrease below config_weight)
 * - The 'power' parameter controls adjustment aggressiveness
 * - The 'max_real_weight' parameter caps maximum weight
 *
 * EXECUTION:
 * - Runs every refresh_period (configured in BalancerManagerConfig)
 * - Only affects virtual services listed in the 'vs' array
 * - Uses update_reals_wlc() to preserve config weights
 *
 * CONFIGURATION REQUIREMENTS:
 * - Must be fully specified if any VS has WLC flag enabled
 * - Can be empty (power=0, max_real_weight=0, vs=[]) if no VS uses WLC
 */
struct balancer_manager_wlc_config {
	/**
	 * Power factor for weight adjustment aggressiveness.
	 *
	 * Controls how aggressively weights are adjusted based on session
	 * distribution imbalance. Higher values cause more dramatic weight
	 * changes in response to load imbalance.
	 *
	 * RECOMMENDED VALUES:
	 * - Conservative (stable): 1-2
	 * - Moderate (balanced): 2-4
	 * - Aggressive (responsive): 4-8
	 * - Very aggressive: 8-16
	 *
	 * EXAMPLE IMPACT:
	 * If a real has 50% fewer sessions than expected (ratio=0.5):
	 * - power=2: weight increases by 1.0x (doubles)
	 * - power=4: weight increases by 2.0x (triples)
	 * - power=1: weight increases by 0.5x (1.5x original)
	 */
	size_t power;

	/**
	 * Maximum effective weight limit.
	 *
	 * Caps the maximum weight a real server can have after WLC
	 * adjustment. Prevents any single real from dominating traffic
	 * distribution even when severely underloaded.
	 *
	 * RECOMMENDED VALUES:
	 * - Conservative: 2-3x maximum configured weight
	 * - Moderate: 5-10x maximum configured weight
	 * - Aggressive: 10-20x maximum configured weight
	 *
	 * EXAMPLE:
	 * If configured weights range from 1-100 and max_real_weight=500:
	 * - A real with weight=50 can reach effective_weight=500 (10x)
	 * - A real with weight=100 can reach effective_weight=500 (5x)
	 */
	size_t max_real_weight;

	/**
	 * Number of virtual service indices in the 'vs' array.
	 *
	 * Specifies how many virtual services have WLC enabled.
	 * If 0, no virtual services use WLC (algorithm is disabled).
	 */
	size_t vs_count;

	/**
	 * Array of virtual service indices with WLC enabled.
	 *
	 * Contains indices into the PacketHandlerConfig.vs array,
	 * identifying which virtual services should have WLC applied.
	 * Only these VSs will have their weights dynamically adjusted.
	 *
	 * OWNERSHIP:
	 * - Caller allocates and manages this array
	 * - Must remain valid for the lifetime of the config
	 * - Array length must match vs_count
	 *
	 * EXAMPLE:
	 * If vs = [0, 2, 5], then virtual services at indices 0, 2, and 5
	 * in the configuration will have WLC enabled, while others won't.
	 */
	uint32_t *vs;
};

/**
 * Complete configuration for a balancer manager.
 *
 * Combines balancer instance configuration with WLC algorithm parameters
 * and operational settings for periodic refresh operations. The manager
 * coordinates the balancer instance and applies scheduling algorithms
 * like WLC.
 *
 * CONFIGURATION DEPENDENCIES:
 * The fields have interdependencies that must be satisfied:
 *
 * 1. If refresh_period > 0, then:
 *    - max_load_factor must be set (typically 0.7-0.9)
 *    - wlc must be configured (even if no VS uses WLC)
 *
 * 2. If any VS has wlc flag enabled, then:
 *    - refresh_period must be > 0
 *    - max_load_factor must be set
 *    - wlc.power and wlc.max_real_weight must be set
 *    - wlc.vs must include the VS index
 *
 * 3. If refresh_period == 0:
 *    - No periodic operations (no auto-resize, no WLC, no stats updates)
 *    - WLC flag cannot be enabled on any VS
 *    - max_load_factor is ignored
 *
 * REFRESH CYCLE OPERATIONS:
 * When refresh_period > 0, the manager performs these operations every
 * refresh_period milliseconds:
 *
 * 1. Session Statistics Collection:
 *    - Scans session table to count active sessions
 *    - Updates per-VS and per-real session counts
 *    - Updates last_packet_timestamp fields
 *    - Makes data available via balancer_manager_info()
 *
 * 2. Automatic Session Table Resizing:
 *    - Calculates: load_factor = active_sessions / table_capacity
 *    - If load_factor > max_load_factor:
 *      * Doubles table capacity
 *      * Migrates existing sessions
 *      * Prevents session table overflow
 *
 * 3. WLC Weight Adjustment:
 *    - For each VS in wlc.vs array:
 *      * Calculates new effective weights based on session distribution
 *      * Calls balancer_manager_update_reals_wlc() to update weights
 *      * Preserves original config weights for future calculations
 *
 * TYPICAL CONFIGURATIONS:
 *
 * Static configuration (no WLC, no auto-resize):
 * ```c
 * config.refresh_period = 0;
 * config.max_load_factor = 0.0;  // ignored
 * config.wlc = {0, 0, 0, NULL};  // ignored
 * ```
 *
 * Auto-resize only (no WLC):
 * ```c
 * config.refresh_period = 30000;  // 30 seconds
 * config.max_load_factor = 0.8;
 * config.wlc = {0, 0, 0, NULL};  // no VSs use WLC
 * ```
 *
 * Full dynamic configuration (auto-resize + WLC):
 * ```c
 * config.refresh_period = 10000;  // 10 seconds
 * config.max_load_factor = 0.75;
 * config.wlc = {
 *     .power = 4,
 *     .max_real_weight = 1000,
 *     .vs_count = 2,
 *     .vs = (uint32_t[]){0, 1}  // VSs 0 and 1 use WLC
 * };
 * ```
 */
struct balancer_manager_config {
	/**
	 * Core balancer configuration.
	 *
	 * Contains packet handler config (virtual services, reals, timeouts)
	 * and state config (session table capacity).
	 */
	struct balancer_config balancer;

	/**
	 * WLC algorithm configuration.
	 *
	 * Specifies WLC parameters (power, max_weight) and which virtual
	 * services have WLC enabled (vs array).
	 *
	 * REQUIREMENTS:
	 * - Must be fully configured if any VS has wlc flag enabled
	 * - Can be empty (all zeros) if no VS uses WLC
	 * - Requires refresh_period > 0 to function
	 */
	struct balancer_manager_wlc_config wlc;

	/**
	 * Periodic refresh interval in milliseconds.
	 *
	 * Controls how often the manager performs background operations:
	 * - Session statistics collection
	 * - Automatic session table resizing
	 * - WLC weight adjustment
	 *
	 * SPECIAL VALUES:
	 * - 0: Disables all periodic operations
	 * - > 0: Enables periodic operations at specified interval
	 *
	 * RECOMMENDED VALUES:
	 * - High-traffic dynamic: 5,000-10,000 ms (5-10 seconds)
	 * - Moderate traffic: 15,000-30,000 ms (15-30 seconds)
	 * - Stable traffic: 30,000-60,000 ms (30-60 seconds)
	 * - Static config: 0 (disabled)
	 *
	 * PERFORMANCE IMPACT:
	 * - Shorter periods: More responsive, higher CPU overhead
	 * - Longer periods: Less overhead, slower response
	 * - Cost scales with active_sessions and vs_count
	 */
	uint32_t refresh_period;

	/**
	 * Maximum session table load factor (0.0 to 1.0).
	 *
	 * Threshold for automatic session table resizing. When the load
	 * factor (active_sessions / table_capacity) exceeds this value
	 * during a refresh cycle, the table capacity is doubled.
	 *
	 * RECOMMENDED VALUES:
	 * - Conservative (frequent resize): 0.6-0.7
	 * - Balanced: 0.7-0.8
	 * - Aggressive (rare resize): 0.8-0.9
	 * - Very aggressive: 0.9-0.95
	 *
	 * TRADE-OFFS:
	 * - Lower values: More frequent resizing, lower collision rate
	 * - Higher values: Less frequent resizing, higher collision rate
	 * - Typical hash table performance degrades above 0.9
	 *
	 * REQUIREMENTS:
	 * - Must be set if refresh_period > 0
	 * - Ignored if refresh_period == 0
	 * - Valid range: 0.0 < max_load_factor < 1.0
	 */
	float max_load_factor;
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
 * This function is specifically designed for WLC (Weighted Least Connection)
 * algorithm to update effective weights without modifying the baseline
 * configuration weights. It provides a way to dynamically adjust weights
 * based on load while preserving the original configured values.
 *
 * KEY DIFFERENCES FROM balancer_manager_update_reals():
 *
 * 1. WEIGHT UPDATE SCOPE:
 *    - update_reals(): Updates BOTH config weight AND state/graph weight
 *    - update_reals_wlc(): Updates ONLY state/graph weight, preserves config
 *
 * 2. CONFIG PRESERVATION:
 *    - update_reals(): New weight becomes the baseline for future WLC
 *    calculations
 *    - update_reals_wlc(): Original config weight remains the WLC baseline
 *
 * 3. USE CASES:
 *    - update_reals(): Manual weight changes by administrator
 *    - update_reals_wlc(): Automatic weight adjustments by WLC algorithm
 *
 * 4. ENABLE STATE:
 *    - update_reals(): Can change both weight and enabled state
 *    - update_reals_wlc(): MUST NOT change enabled state (validation error)
 *
 * EXAMPLE SCENARIO:
 * ```
 * Initial config: real1.weight = 100
 *
 * // WLC adjusts weight based on load
 * update_reals_wlc(real1, weight=150)
 * Result: config.weight=100, state.weight=150
 * Next WLC cycle uses config.weight=100 as baseline
 *
 * // Admin changes weight
 * update_reals(real1, weight=200)
 * Result: config.weight=200, state.weight=200
 * Next WLC cycle uses config.weight=200 as baseline
 * ```
 *
 * VALIDATION:
 * - All updates must have weight != DONT_UPDATE_REAL_WEIGHT
 * - All updates must have enabled == DONT_UPDATE_REAL_ENABLED
 * - Returns error if any update attempts to change enabled state
 *
 * TYPICAL USAGE:
 * This function is called automatically by the background refresh task
 * when WLC is enabled. Manual calls are rare and should be done with
 * caution to avoid interfering with the WLC algorithm.
 *
 * Diagnostics: On error, a message is recorded and retrievable via
 * balancer_manager_take_error().
 *
 * @param manager Manager handle.
 * @param count   Number of updates in the array.
 * @param updates Array of real server weight updates. Each update MUST:
 *                - Have weight != DONT_UPDATE_REAL_WEIGHT
 *                - Have enabled == DONT_UPDATE_REAL_ENABLED
 * @return 0 on success, -1 on error (including validation failures).
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
