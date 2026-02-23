#pragma once

#include "api/handler.h"
#include "handler/real.h"
#include "lib/controlplane/config/cp_module.h"

#include "api/real.h"
#include "api/session.h"

#include "filter/filter.h"

#include "common/lpm.h"

////////////////////////////////////////////////////////////////////////////////

struct balancer_state;
struct packet_handler_config;
struct balancer_update_info;

/**
 * Sentinel value indicating an invalid or non-existent index.
 * Used in vs_index and reals_index arrays to mark unmapped entries.
 */
#define INDEX_INVALID ((uint32_t)-1)

/**
 * Per-protocol (IPv4/IPv6) virtual service container.
 *
 * This structure holds the fast-path lookup structures for a specific IP
 * protocol version. It uses relative pointers (see common/memory_address.h)
 * for all pointer fields.
 *
 * Memory Layout - Connection to packet_handler:
 * The `vs` field points into the parent packet_handler's vs array, specifically
 * to the subset of virtual services for this protocol (IPv4 or IPv6).
 *
 * Filter Reuse Optimization:
 * When updating configuration, if all VS identifiers match between old and new
 * configs (same count, same services), the filter can be reused via
 * EQUATE_OFFSET. The filter_reused flag prevents double-free during cleanup.
 */
struct packet_handler_vs {
	// Fast-path filter for matching packets to virtual services
	// Uses destination IP, port, and protocol to find VS index
	struct filter *filter;

	uint8_t __padding[56];

	// Set to 1 when filter is reused from previous packet_handler_vs
	// Prevents double-free during configuration updates
	// Set to 0 when a new filter is built
	int filter_reused;

	// LPM tree for VS address announcement/routing
	struct lpm announce;

	// Number of virtual services for this protocol
	size_t vs_count;

	// Points to the subset of packet_handler->vs array for this protocol
	// (relative pointer to IPv4 or IPv6 virtual services)
	struct vs *vs;

	// Size of the vs_index mapping array
	// Equals the total number of VS in the balancer state registry
	size_t vs_index_size;

	// Maps VS registry index to position in this protocol's vs array
	// vs_index[registry_idx] = vs_idx, or INDEX_INVALID if not present
	// (relative pointer)
	uint32_t *vs_index;
};

/**
 * Packet handler instance.
 *
 * Owns fast-path lookup structures (filters/LPM), VS/real views and counters
 * bound to a balancer_state. Used by the control-plane to program dataplane.
 */
struct packet_handler {
	// Control-plane module interface for dataplane communication
	// Manages counter registry and module lifecycle
	struct cp_module cp_module;

	// relative pointer to the balancer state,
	// corresponding to this handler
	struct balancer_state *state;

	// timeouts of sessions with different types
	struct sessions_timeouts sessions_timeouts;

	// Total number of virtual services (IPv4 + IPv6)
	size_t vs_count;

	// Array of all virtual services (relative pointer)
	// First ipv4_count entries are IPv4, remaining are IPv6
	struct vs *vs;

	// Size of the vs_index mapping array
	// Equals the total number of VS in the balancer state registry
	size_t vs_index_size;

	// Maps VS registry index to position in vs array (relative pointer)
	// vs_index[registry_idx] = vs_idx, or INDEX_INVALID if not present
	uint32_t *vs_index;

	// Per-protocol virtual service containers
	// vs_ipv4.vs points to IPv4 subset of the vs array
	// vs_ipv6.vs points to IPv6 subset of the vs array
	struct packet_handler_vs vs_ipv4;
	struct packet_handler_vs vs_ipv6;

	// reals
	size_t reals_count;
	struct real *reals;

	// map: real_registry_idx -> ph_real_idx
	// -1 means no mapping
	size_t reals_index_size;
	uint32_t *reals_index;

	// counter indices
	struct {
		// common counter
		uint64_t common;

		// icmp v4 counter
		uint64_t icmp_v4;

		// icmp v6 counter
		uint64_t icmp_v6;

		// l4 (tcp and udp) counter
		uint64_t l4;
	} counter;

	// if packet destination id is from decap list,
	// then we make decap
	struct lpm decap_ipv4;
	struct lpm decap_ipv6;

	// source address of the balancer
	struct net4_addr source_ipv4;
	struct net6_addr source_ipv6;
};

/**
 * Setup packet handler and update control-plane modules.
 *
 * Creates/configures a handler bound to the provided balancer_state.
 * Returns pointer on success, or NULL on error.
 *
 * Diagnostics: errors are recorded and retrievable via
 * balancer_take_error_msg() on the balancer associated with this handler.
 *
 * @param agent        Agent that will own the handler.
 * @param name         Handler name.
 * @param config       Packet handler configuration.
 * @param state        Balancer state to bind to.
 * @param prev_handler Previous handler for filter reuse (may be NULL).
 * @param update_info  Output structure filled with update information.
 *                     May be NULL if caller doesn't need this information.
 * @return Pointer to new handler on success, NULL on error.
 */
struct packet_handler *
packet_handler_setup(
	struct agent *agent,
	const char *name,
	struct packet_handler_config *config,
	struct balancer_state *state,
	struct packet_handler *prev_handler,
	struct balancer_update_info *update_info
);

/**
 * Apply updates to reals visible by this handler.
 *
 * @param handler Handler instance.
 * @param count   Number of updates.
 * @param updates Array of real_update entries.
 * @return 0 on success, -1 on error.
 */
int
packet_handler_update_reals(
	struct packet_handler *handler,
	size_t count,
	struct real_update *updates
);

struct packet_handler_ref;
struct balancer_stats;

/**
 * Fill statistics for the packet handler.
 *
 * Collects counter data from the handler and populates the stats structure.
 *
 * @param handler Packet handler instance
 * @param stats   Output structure to fill with statistics
 * @param ref     Reference to packet handler for counter access
 * @return 0 on success, -1 on error
 */
int
packet_handler_fill_stats(
	struct packet_handler *handler,
	struct balancer_stats *stats,
	struct packet_handler_ref *ref
);

/**
 * Get packet handler indices for a real server.
 *
 * Resolves a real identifier to its indices within the packet handler's
 * internal arrays (VS index and real index within that VS).
 *
 * @param handler Packet handler instance
 * @param real    Real server identifier to look up
 * @param idx     Output structure filled with VS and real indices
 * @return 0 on success, -1 if real not found
 */
int
packet_handler_real_idx(
	struct packet_handler *handler,
	struct real_identifier *real,
	struct real_ph_index *idx
);
