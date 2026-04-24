#pragma once

#include "common/network.h"
#include "filter/rule.h"

#include "modules/balancer2/dataplane/types/session.h"

/* ------------------------------------------------------------------
 * Forward declarations
 * ------------------------------------------------------------------ */

struct agent;
struct vs_acl;
struct vs_matcher;
struct session_table;
struct balancer_handle;

enum ip_family {
	ip_family_ip4,
	ip_family_ip6,
};

/* ------------------------------------------------------------------
 * Concurrency and lifetime
 *
 * All routines in this header are controlplane-side and must be called
 * from a single controlplane thread for a given `agent`. Dataplane
 * workers read the currently-installed balancer config concurrently
 * and without per-call synchronization; `balancer_install` blocks
 * until all workers are captured new generation.
 *
 * Once a balancer is installed, any matcher, ACL, or session table it
 * references is read by dataplane workers. Freeing such an object
 * while workers may still observe it is a use-after-free.
 *
 * The safe sequence for replacing a balancer is:
 *   1. build and install the successor via `balancer_install`;
 *   2. call `balancer_free` on the predecessor handle;
 *   3. free any matcher, ACL, or session table that is no longer
 *      referenced by any live balancer handle.
 *
 * Steps 2 and 3 are the caller's responsibility. `balancer_free` does not
 * block on workers and does not free the matcher, ACLs, or session
 * table that were passed to `balancer_create`.
 * ------------------------------------------------------------------ */

/* ------------------------------------------------------------------
 * Real (backend) configuration
 * ------------------------------------------------------------------ */

enum tunnel_kind {
	tunnel_kind_ip,
	tunnel_kind_gre,
};

struct real_config {
	struct net_addr dst;
	uint8_t ip_proto;

	uint32_t weight;
	bool enabled;

	struct net src;

	enum tunnel_kind tunnel;
};

/* ------------------------------------------------------------------
 * VS ACL
 * ------------------------------------------------------------------ */

struct vs_acl_rule {
	struct filter_net4s net4s;
	struct filter_net6s net6s;
	struct filter_port_ranges port_ranges;
};

/*
 * Builds an ACL for a virtual service.
 *
 * The ACL may be reused across balancer configs as long as the allowed
 * sources for the VS do not change. The caller owns the returned object
 * and is responsible for freeing it.
 */
struct vs_acl *
create_vs_acl(
	struct agent *agent, const struct vs_acl_rule *rules, size_t rule_count
);

/* ------------------------------------------------------------------
 * VS (virtual service) configuration
 * ------------------------------------------------------------------ */

enum vs_scheduler {
	vs_sched_wlc,
	/* Stateless one packet scheduler: weighted round-robin without a
	   session table. */
	vs_sched_op,
	vs_sched_wrr,
	vs_sched_sh,
};

/*
 * A VS is identified by the tuple (dst, port, transport_proto).
 * The dataplane uses this tuple together with the client (src_ip, src_port)
 * to look up an existing session in the session table.
 */
struct vs_config {
	struct net_addr dst;
	uint8_t ip_proto;

	/*
	 * Destination port, host byte order. If `port` is 0, the VS is
	 * L3-only and accepts all destination ports of the given
	 * `transport_proto`. `transport_proto` is always applied; "any
	 * transport" is not expressible through a single VS — use
	 * separate VS entries.
	 */
	uint16_t port;

	uint8_t transport_proto;

	enum vs_scheduler scheduler;

	struct real_config *reals;
	size_t real_count;
};

/* ------------------------------------------------------------------
 * VS matcher
 * ------------------------------------------------------------------ */

/*
 * Builds a matcher that selects a VS for an incoming packet from an
 * array of VS configs of a single IP family.
 *
 * The order of `configs` is significant: it defines the VS indices used
 * by the resulting matcher and by `balancer_vs_update_real_*` routines.
 *
 * A matcher may be reused across balancer configs as long as the VS
 * identifiers (3-tuple) and their order are preserved; scheduler, reals and
 * ACLs may differ. The caller owns the returned matcher.
 */
struct vs_matcher *
create_vs_matcher_ip4(
	struct agent *agent, const struct vs_config *vs_configs, size_t vs_count
);

struct vs_matcher *
create_vs_matcher_ip6(
	struct agent *agent, const struct vs_config *vs_configs, size_t vs_count
);

/* ------------------------------------------------------------------
 * VS bundle
 *
 * Groups everything needed to serve one IP family. `configs` and `acls`
 * are parallel arrays of length `vs_count`: slot `i` in `acls` is the
 * ACL of the VS described by `configs[i]`.
 *
 * Matcher reuse. A `matcher` may be shared across balancers and across
 * successive bundles of the same balancer. Every bundle it is placed
 * into must satisfy all of:
 *   - the matcher's IP family matches the bundle's family — a matcher
 *     from `create_vs_matcher_ip4` goes only into an IPv4 bundle, and
 *     likewise for ip6;
 *   - `vs_count` equals the number of configs the matcher was built
 *     from;
 *   - for every `i`, `configs[i]` carries the same VS identity tuple
 *     (dst, ip_proto, port, transport_proto) as the config at index
 *     `i` at matcher build time, i.e. identifiers match in the same
 *     order.
 * Fields outside VS identity — `scheduler`, `reals`, `real_count`,
 * and `acls[i]` — may differ between bundles that share a matcher.
 *
 * ACL reuse. An individual `acls[i]` may be shared across balancers
 * and reused across bundles, but only:
 *   - while that VS's allowed sources (the `vs_acl_rule` set it was
 *     built from) are unchanged. Any change to the allowed sources
 *     requires a fresh ACL.
 * ------------------------------------------------------------------ */

struct vs_bundle {
	struct vs_config *configs;
	struct vs_acl **acls;
	struct vs_matcher *matcher;
	size_t vs_count;
};

/* -----------------------------------------------------------------
 * Session table
 * ------------------------------------------------------------------ */

/*
 * Allocates a session table with capacity `size`. May be shared across
 * balancers. The caller owns the returned table.
 */
struct session_table *
session_table_create(struct agent *agent, size_t size);

/*
 * Promotes the current (forward) table to backward and installs a fresh
 * forward table of capacity `size`. Fails if a backward table already
 * exists. 
 * These method is blocked until all workers capture new generation.
 */
int
session_table_rotate(struct session_table *table, size_t size);

/* Drops the backward table. 
* These method is blocked until all workers capture new generation.
*/
int
session_table_drop_backward(struct session_table *table);

// TODO:
// session table iter.
// How it should handle two session table layers?

/* ------------------------------------------------------------------
 * Balancer
 * ------------------------------------------------------------------ */

/*
 * Assembles a balancer from an IPv4 bundle, an IPv6 bundle, and a
 * shared session table. The matchers, ACLs, and session table passed
 * in must outlive this handle.
 *
 * TODO: document session table reuse invariants — when it is safe to
 * share a `struct session_table *` across balancers and what the
 * consequences are for session lookup when multiple balancers share one table.
 */
struct balancer_handle *
balancer_create(
	struct agent *agent,
	const char *name,
	struct session_table *table,
	struct vs_bundle *vs_ip4,
	struct vs_bundle *vs_ip6,
	struct balancer_session_timeouts *timeouts
);

/*
 * Installs `handle` in the dataplane. If a balancer with the same name
 * is already installed, it is replaced and the old handle may be freed.
 */
int
balancer_install(struct agent *agent, struct balancer_handle *handle);

/*
 * Frees a balancer handle. The caller remains responsible for freeing
 * the VS matchers, ACLs, and session table that were passed in.
 */
void
balancer_free(struct agent *agent, struct balancer_handle *handle);

/*
 * Updates the weights or enabled state of reals for the VS at `vs_idx`
 * within the bundle for `family`. The arrays have length equal to the
 * VS's `real_count` and are indexed in the same order as its `reals`.
 */
int
balancer_vs_update_real_weights(
	struct balancer_handle *handle,
	enum ip_family family,
	size_t vs_idx,
	const uint32_t *weights
);

int
balancer_vs_update_real_states(
	struct balancer_handle *handle,
	enum ip_family family,
	size_t vs_idx,
	const bool *states
);

/* Need this to parse counter names. */
const char *balancer_vs_counter_prefix;
const char *balancer_vs_acl_counter_prefix;
const char *balancer_real_counter_prefix;
const char *balancer_common_counter_name;
const char *balancer_l4_counter_name;