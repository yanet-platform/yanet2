#pragma once

#include "api/vs.h"
#include "common/memory.h"
#include "selector.h"

#include "common/lpm.h"

struct vs_state;
struct real;
struct named_vs_config;

/**
 * Handler-side view of a virtual service.
 *
 * Holds selection policy, backend views and source filters for fast-path
 * lookup.
 */
struct vs {
	struct vs_identifier identifier; // Address + Port + Proto
	
	size_t registry_idx; // Index in the registry
	
	uint8_t flags;	  // VS_* flags describing behavior/scheduling
	struct real_selector selector; // Real selection helper (RR/hash)

	size_t real_count;	       // Number of elements in 'reals'
	struct real *reals;       // Array of reals belongs to Virtual Service

	struct lpm src_filter; // Client source allowlist (LPM trie)

	size_t peers_v4_count;	    // Number of IPv4 peers in 'peers_v4'
	struct net4_addr *peers_v4; // IPv4 peer balancers

	size_t peers_v6_count;	    // Number of IPv6 peers in 'peers_v6'
	struct net6_addr *peers_v6; // IPv6 peer balancers

	uint64_t counter_id; // Per-VS counter id
};

/**
 * Initialize handler-side VS view.
 * Returns 0 on success, -1 on error.
 */
int
vs_view_init(
	struct vs *vs,
	struct real *reals,
	struct balancer_state *state,
	struct named_vs_config *config,
	struct counter_registry *registry,
	struct memory_context *mctx
);

/**
 * Free resources bound to the VS view.
 */
void
vs_view_free(struct vs *vs, struct memory_context *mctx);

/**
 * Refresh real selector and related data from the current state.
 * Returns 0 on success, -1 on error.
 */
int
vs_view_update_reals(struct vs *vs);

/**
 * Resolve VS registry index from a counter handle.
 * Returns index on success, or -1 on error.
 */
ssize_t
counter_to_vs_registry_idx(struct counter_handle *counter);
