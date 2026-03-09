#pragma once

#include "api/inspect.h"
#include "api/vs.h"

#include "common/memory.h"
#include "filter.h"
#include "selector.h"
#include <stddef.h>

struct vs_state;
struct real;
struct named_vs_config;
struct balancer_update_info;

/**
 * Handler-side view of a virtual service.
 *
 * Holds selection policy, backend views and source filters for fast-path
 * lookup.
 */
struct vs {
	struct vs_identifier identifier; // Address + Port + Proto

	size_t registry_idx; // Index in the registry

	uint8_t flags; // VS_* flags describing behavior/scheduling

	// Can be modified atomically via real_update method
	struct real_selector selector;

	size_t reals_count;	  // Number of elements in 'reals'
	const struct real *reals; // Array of reals belongs to Virtual Service

	// Index of the first real in the reals array
	size_t first_real_idx;

	// Access Control List (ACL) filter for source IP/port filtering
	// Compiled from the rules array below, used for fast packet matching
	// (relative pointer)
	struct filter *acl;

	// Set to 1 when ACL filter is reused from previous VS configuration
	// Prevents double-free during configuration updates
	// Set to 0 when a new ACL is built
	// Reuse occurs when filter rules are identical between old and new
	// config
	int acl_reused;

	// Number of filter rules in the rules array
	// Each rule specifies allowed source networks and port ranges
	size_t rules_count;

	// Array of filter rules defining allowed sources (relative pointer)
	// Rules are normalized (sorted, deduplicated) and stored with relative
	// pointers to their internal arrays (net4.srcs, net6.srcs,
	// transport.srcs)
	struct filter_rule *rules;

	// Indices of counters for rules
	uint64_t *rule_counters;

	// Number of IPv4 peer balancer addresses
	size_t peers_v4_count;

	// Array of IPv4 peer balancer addresses (relative pointer)
	// Used for coordinating with other balancer instances
	struct net4_addr *peers_v4;

	// Number of IPv6 peer balancer addresses
	size_t peers_v6_count;

	// Array of IPv6 peer balancer addresses (relative pointer)
	// Used for coordinating with other balancer instances
	struct net6_addr *peers_v6;

	uint64_t counter_id; // Per-VS counter id
};

/**
 * Setup VS state in the balancer registry.
 *
 * Finds or inserts the virtual service into the balancer state registry
 * and initializes the VS's registry_idx and identifier fields.
 *
 * @param vs             VS structure to initialize
 * @param balancer_state Balancer state containing the VS registry
 * @param config         VS configuration with identifier
 * @return 0 on success, -1 on error
 */
int
vs_state_setup(
	struct vs *vs,
	struct balancer_state *balancer_state,
	struct named_vs_config *config
);

/**
 * Initialize handler-side VS view.
 * Returns 0 on success, -1 on error.
 */
int
vs_with_identifier_and_registry_idx_init(
	struct vs *vs,
	struct vs *prev_vs,
	size_t first_real_idx,
	struct real *reals,
	struct balancer_state *state,
	struct named_vs_config *config,
	struct counter_registry *registry,
	struct memory_context *mctx,
	struct balancer_update_info *update_info
);

/**
 * Free resources bound to the VS view.
 */
void
vs_free(struct vs *vs, struct memory_context *mctx);

/**
 * Refresh real selector and related data from the current state.
 * Returns 0 on success, -1 on error.
 */
int
vs_update_reals(struct vs *vs);

/**
 * Resolve VS registry index from a counter handle.
 * Returns index on success, or -1 on error.
 */
ssize_t
counter_to_vs_registry_idx(struct counter_handle *counter);

////////////////////////////////////////////////////////////////////////////////

static inline bool
vs_real_enabled(struct vs *vs, uint32_t real_idx) {
	return selector_real_enabled(
		&vs->selector, real_idx - vs->first_real_idx
	);
}

////////////////////////////////////////////////////////////////////////////////

void
vs_fill_inspect(struct vs *vs, struct vs_inspect *inspect, size_t workers);

////////////////////////////////////////////////////////////////////////////////

ssize_t
parse_vs_acl_counter(struct counter_handle *counter, const char **tag);