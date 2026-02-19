#pragma once

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

	// TODO: docs
	struct filter *acl;

	// TODO: docs
	size_t rules_count;
	struct filter_rule *rules;

	// TODO: more docs
	size_t peers_v4_count;	    // Number of IPv4 peers in 'peers_v4'
	struct net4_addr *peers_v4; // IPv4 peer balancers

	// TODO: more docs
	size_t peers_v6_count;	    // Number of IPv6 peers in 'peers_v6'
	struct net6_addr *peers_v6; // IPv6 peer balancers

	uint64_t counter_id; // Per-VS counter id
};

// TODO: docs
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
vs_init(struct vs *vs,
	struct vs *prev_vs,
	size_t first_real_idx,
	struct real *reals,
	struct balancer_state *state,
	struct named_vs_config *config,
	struct counter_registry *registry,
	struct memory_context *mctx,
	struct balancer_update_info *update_info);

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