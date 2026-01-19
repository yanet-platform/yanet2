#pragma once

#include "handler/real.h"
#include "lib/controlplane/config/cp_module.h"

#include "api/real.h"
#include "api/session.h"

#include "filter/filter.h"

#include "common/lpm.h"

////////////////////////////////////////////////////////////////////////////////

struct balancer_state;
struct packet_handler_config;

#define INDEX_INVALID ((uint32_t)-1)

/**
 * Packet handler instance.
 *
 * Owns fast-path lookup structures (filters/LPM), VS/real views and counters
 * bound to a balancer_state. Used by the control-plane to program dataplane.
 */
struct packet_handler {
	struct cp_module cp_module;

	struct memory_context mctx;

	// relative pointer to the balancer state,
	// corresponding to this handler
	struct balancer_state *state;

	// timeouts of sessions with different types
	struct sessions_timeouts sessions_timeouts;

	// mapping: (address, port, proto) -> vs_id
	struct filter vs_v4;
	struct filter vs_v6;

	// set of IP addresses announced by balancer
	// (virtual service IPs)
	struct lpm announce_ipv4;
	struct lpm announce_ipv6;

	// virtual services
	size_t vs_count;
	struct vs *vs;

	// map: vs_registry_idx -> ph_vs_idx
	size_t vs_index_count;
	uint32_t *vs_index;

	// reals
	size_t reals_count;
	struct real *reals;

	// map: real_registry_idx -> ph_real_idx
	// -1 means no mapping
	size_t reals_index_count;
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
 */
struct packet_handler *
packet_handler_setup(
	struct agent *agent,
	const char *name,
	struct packet_handler_config *config,
	struct balancer_state *state
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
 * Fill balancer statistics from this handler.
 *
 * Optionally filters by packet handler reference.
 */
void
packet_handler_fill_stats(
	struct packet_handler *handler,
	struct balancer_stats *stats,
	struct packet_handler_ref *ref
);

int
packet_handler_real_idx(
	struct packet_handler *handler,
	struct real_identifier *real,
	struct real_ph_index *idx
);