#pragma once

#include "real.h"
#include "vs.h"
#include <stddef.h>

/**
 * Real server state in the balancer topology graph.
 *
 * Represents the current operational state of a real server within a
 * virtual service, including its effective weight and enabled status.
 * This is a snapshot of the runtime state, which may differ from the
 * configured state due to dynamic weight adjustments (WLC).
 *
 * WEIGHT SEMANTICS:
 * - This is the EFFECTIVE weight currently used by the scheduler
 * - For non-WLC virtual services: weight == configured weight
 * - For WLC virtual services: weight may be dynamically adjusted
 * - The original configured weight is preserved in the configuration
 *
 * USE CASES:
 * - Monitoring current load distribution
 * - Debugging WLC weight adjustments
 * - Visualizing balancer topology
 * - Detecting disabled or removed reals
 */
struct graph_real {
	/** Real server identifier (relative to its virtual service) */
	struct relative_real_identifier identifier;

	/**
	 * Current effective weight used by the scheduler.
	 *
	 * This is the weight currently active in the dataplane for traffic
	 * distribution. It may differ from the configured weight if:
	 * - WLC is enabled and has adjusted weights based on session counts
	 * - Real was recently updated via UpdateReals/UpdateRealsWlc
	 *
	 * For WLC-enabled virtual services, this weight is recalculated
	 * every refresh_period based on active session distribution.
	 */
	uint16_t weight;

	/**
	 * Whether the real server is currently enabled.
	 *
	 * When false:
	 * - Real receives no NEW sessions
	 * - Existing sessions may continue to be forwarded (until timeout)
	 * - Real is excluded from scheduling decisions
	 * - Real is excluded from WLC calculations
	 *
	 * When true:
	 * - Real participates in scheduling
	 * - Real can receive new sessions
	 * - Real is included in WLC calculations
	 */
	bool enabled;
};

/**
 * Virtual service state in the balancer topology graph.
 *
 * Represents a virtual service and all its associated real servers
 * with their current operational states. This provides a complete
 * snapshot of the VS topology at a point in time.
 *
 * MEMORY MANAGEMENT:
 * - The 'reals' array is heap-allocated
 * - Caller must free with balancer_graph_free() after use
 * - Do not modify the array contents directly
 */
struct graph_vs {
	/** Virtual service identifier */
	struct vs_identifier identifier;

	/** Number of real servers in the 'reals' array */
	size_t real_count;

	/**
	 * Array of real server states.
	 *
	 * Contains current state for all reals configured for this VS,
	 * including both enabled and disabled reals. The order matches
	 * the configuration order.
	 *
	 * Ownership: Allocated by balancer_graph(), freed by
	 * balancer_graph_free()
	 */
	struct graph_real *reals;
};

/**
 * Complete balancer topology graph.
 *
 * Provides a snapshot of the entire balancer configuration showing
 * all virtual services and their real servers with current operational
 * states (weights, enabled status).
 *
 * This structure is useful for:
 * - Visualizing the complete load balancer topology
 * - Monitoring real server states across all virtual services
 * - Debugging configuration and WLC behavior
 * - Understanding current traffic distribution
 * - Detecting configuration inconsistencies
 *
 * USAGE PATTERN:
 * ```c
 * struct balancer_graph graph;
 * balancer_graph(handle, &graph);
 *
 * // Use graph data...
 * for (size_t i = 0; i < graph.vs_count; i++) {
 *     struct graph_vs *vs = &graph.vs[i];
 *     for (size_t j = 0; j < vs->real_count; j++) {
 *         struct graph_real *real = &vs->reals[j];
 *         // Process real state...
 *     }
 * }
 *
 * balancer_graph_free(&graph);
 * ```
 *
 * MEMORY MANAGEMENT:
 * - All arrays are heap-allocated by balancer_graph()
 * - Must be freed with balancer_graph_free() when done
 * - Safe to call balancer_graph_free() on partially-initialized graphs
 */
struct balancer_graph {
	/** Number of virtual services in the 'vs' array */
	size_t vs_count;

	/**
	 * Array of virtual service states.
	 *
	 * Contains state for all configured virtual services in the
	 * balancer. The order matches the configuration order.
	 *
	 * Ownership: Allocated by balancer_graph(), freed by
	 * balancer_graph_free()
	 */
	struct graph_vs *vs;
};