#pragma once

#include "common/network.h"
#include "filter/rule.h"

#include "modules/balancer2/dataplane/types/session.h"

struct agent;
struct session_table;
struct balancer_handle;
struct vs_handle;

enum ip_family {
	ip_family_ip4,
	ip_family_ip6,
};

enum tunnel_kind {
	tunnel_kind_ip,
	tunnel_kind_gre,
};

/*
 * Configuration of a single real (backend).
 */
struct real_config {
	struct net_addr dst;
	enum ip_family ip_family;

	uint32_t weight;
	bool enabled;

	struct net src;

	enum tunnel_kind tunnel;
};

/*
 * Source allow-list for a VS. A packet is admitted only if its source
 * address matches one of the listed IPv4/IPv6 networks AND its source
 * port matches one of the listed ranges. An empty set of networks
 * disallows all networks; an empty set of ports allows all ports.
 */
struct allowed_sources {
	struct filter_net4s net4s;
	struct filter_net6s net6s;
	struct filter_port_ranges port_ranges;
};

enum vs_scheduler {
	vs_sched_wlc,
	/* Stateless one-packet scheduler: weighted round-robin without a
	   session table. */
	vs_sched_op,
	vs_sched_wrr,
	vs_sched_sh,
};

/*
 * Configuration of a virtual service.
 *
 * A VS is identified by the tuple (dst, ip_family, port,
 * transport_proto).
 *
 * If `port` is 0 the VS is L3-only and matches all destination ports
 * of the given `transport_proto`. `transport_proto` is always applied;
 * "any transport" is not expressible through a single VS — use
 * separate VS entries.
 */
struct vs_config {
	struct net_addr dst;
	enum ip_family ip_family;

	/* Destination port, host byte order. */
	uint16_t port;

	uint8_t transport_proto;

	/* Required; must not be NULL. */
	struct allowed_sources *allowed_sources;

	enum vs_scheduler scheduler;

	struct real_config *reals;
	size_t real_count;
};

/*
 * Creates a VS handle from `config`. The handle is used to mutate
 * per-real state (weights, enabled flags) after the containing
 * balancer is installed.
 */
struct vs_handle *
create_vs(struct agent *agent, const struct vs_config *config);

/*
 * Returns 0 if the handle is still referenced by a balancer, or 1 if
 * it was actually freed.
 */
int
free_vs(struct agent *agent, struct vs_handle *vs);

/*
 * Creates a session table. `capacity` is the number of session entries.
 */
struct session_table *
create_session_table(struct agent *agent, size_t capacity);

/*
 * Returns 0 if the session table is still referenced by someone, or 1
 * if it was actually freed.
 */
int
free_session_table(struct agent *agent, struct session_table *table);

// TODO:
// session table iter.

/*
 * Creates a balancer handle from its full configuration.
 *
 * The session `table` and each `vs` handle must outlive the returned
 * balancer handle; they are not owned by it.
 */
struct balancer_handle *
create_balancer(
	struct agent *agent,
	const char *name,
	struct session_table *table,
	struct balancer_session_timeouts *timeouts,
	struct vs_handle **vs,
	size_t vs_count
);

/*
 * Pushes `table` as the new front (primary) session table.
 *
 * Workers look up sessions in the front table first and fall back to
 * the previous (back) table; a session found in the back table is
 * copied forward. New sessions are always created in the front table.
 *
 * Returns -1 if two session tables are already attached.
 */
int
balancer_session_table_push_front(
	struct balancer_handle *balancer, struct session_table *table
);

/*
 * Detaches the back session table.
 *
 * After this call, new workers ignore the detached table for lookups.
 *
 * Returns -1 if only one session table is attached.
 */
int
balancer_session_table_pop_back(struct balancer_handle *balancer);

/*
 * Installs `handle` in the dataplane.
 *
 * If a balancer with the same name is already installed, it is
 * replaced; the previous handle becomes unused and the caller is
 * responsible for freeing it with `free_balancer`.
 *
 * Returns -1 on error, 0 on success.
 */
int
install_balancer(struct agent *agent, struct balancer_handle *handle);

/*
 * Frees a balancer handle. The session tables passed to
 * `create_balancer` and `balancer_session_table_push_front`, and the
 * VS handles passed to `create_balancer`, are not freed — the caller
 * owns them.
 */
void
free_balancer(struct agent *agent, struct balancer_handle *handle);

/*
 * Updates per-real weights for `vs`. `weights` must have length equal
 * to the VS's `real_count` and be indexed in the same order as the
 * `reals` array passed to `create_vs`.
 * Returns 0 on success, -1 if the length of `weights` does not match
 * the VS's `real_count`, or -2 on allocation failure.
 */
int
vs_update_real_weights(struct vs_handle *vs, const uint32_t *weights);

/*
 * Updates per-real enabled flags for `vs`. `states` follows the same
 * length and ordering as in `vs_update_real_weights`. Same return
 * codes as `vs_update_real_weights`.
 */
int
vs_update_real_states(struct vs_handle *vs, const bool *states);

/*
 * Counters are registered by API with their names. The
 * controlplane parses emitted counter names against these to route
 * values back to their VS, real, or balancer-level source.
 *
 * VS counter format:      vs_<vip>:<port>/<proto>
 *   where proto is "tcp" or "udp".
 */
extern const char *const balancer_vs_counter_prefix;

/*
 * VS ACL counter format:  vs_acl_<vip>:<port>/<proto>_<tag>
 */
extern const char *const balancer_vs_acl_counter_prefix;

/*
 * Real counter format:    real_<vip>:<port>/<proto>_<real_dst>
 */
extern const char *const balancer_real_counter_prefix;

/*
 * Common counter name:    "cmn"
 */
extern const char *const balancer_common_counter_name;

/*
 * L4 counter name:        "l4"
 */
extern const char *const balancer_l4_counter_name;