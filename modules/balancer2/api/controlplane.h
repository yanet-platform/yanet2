#pragma once

#include "common/network.h"
#include "filter/rule.h"

#include "modules/balancer2/dataplane/types/session.h"

struct agent;
struct balancer_session_table;
struct balancer_handle;
struct balancer_vs_handle;

enum balancer_tunnel_kind {
	balancer_tunnel_kind_ip,
	balancer_tunnel_kind_gre,
};

/*
 * Configuration of a single real (backend).
 */
struct balancer_real_config {
	struct net_addr dst;
	enum ip_family ip_family;

	struct net src;

	enum balancer_tunnel_kind tunnel;
};

/*
 * Source allow-list for a VS. A packet is admitted only if its source
 * address matches one of the listed IPv4/IPv6 networks AND its source
 * port matches one of the listed ranges. An empty set of networks
 * disallows all networks; an empty set of ports allows all ports.
 */
struct balancer_allowed_sources {
	struct filter_net4s net4s;
	struct filter_net6s net6s;
	struct filter_port_ranges port_ranges;
};

enum balancer_vs_sched {
	/* Stateless one-packet scheduler: weighted round-robin without a
	   session table. */
	balancer_vs_sched_op,
	balancer_vs_sched_wrr,
	balancer_vs_sched_sh,
};

/*
 * Configuration of a virtual service.
 *
 * A VS is identified by the tuple (destination address, address family,
 * destination port, transport protocol).
 *
 * If the destination port is 0 the VS is L3-only and matches all
 * destination ports of the given transport protocol. The transport
 * protocol is always applied; "any transport" is not expressible
 * through a single VS — use separate VS entries.
 */
struct balancer_vs_config {
	struct net_addr dst;
	enum ip_family ip_family;

	/* Destination port, host byte order. */
	uint16_t port;

	uint8_t transport_proto;

	struct balancer_allowed_sources allowed_sources;

	enum balancer_vs_sched scheduler;

	struct balancer_real_config *reals;
	size_t real_count;
};

/*
 * Creates a VS handle from the supplied configuration. The handle is
 * used to mutate per-real state (weights, enabled flags) after the
 * containing balancer is installed.
 */
struct balancer_vs_handle *
balancer_create_vs(
	struct agent *agent, const struct balancer_vs_config *config
);

/*
 * Returns 0 if the handle is still referenced by a balancer, or 1 if
 * it was actually freed.
 */
int
balancer_free_vs(struct agent *agent, struct balancer_vs_handle *vs);

/*
 * Creates a session table with the given capacity (number of session
 * entries).
 */
struct balancer_session_table *
balancer_create_session_table(struct agent *agent, size_t capacity);

/*
 * Returns 0 if the session table is still referenced by a balancer, or 1
 * if it was actually freed.
 */
int
balancer_free_session_table(
	struct agent *agent, struct balancer_session_table *table
);

// TODO:
// session table iter.

/*
 * Bounded chain of session tables consulted by workers on each packet.
 *
 * At most two tables are attached at any time: a front table, into
 * which workers insert new sessions, and an optional back table used
 * only as a lookup fallback. When a session is found in the back
 * table its entry is copied into the front table so subsequent
 * lookups hit the front directly.
 *
 * The two-slot arrangement lets the controlplane swap the active
 * session table (for example, to resize it) without dropping
 * in-flight sessions: push the replacement to the front, leaving the
 * old table as a back fallback, then pop the back once it has drained.
 *
 * At least one table must be attached for workers to handle packets.
 */
struct balancer_session_table_chain;

/*
 * Creates a session table chain seeded with the given front table.
 * The table is not owned by the chain and must outlive it.
 * Returns NULL on allocation failure.
 */
struct balancer_session_table_chain *
balancer_create_session_table_chain(
	struct agent *agent, struct balancer_session_table *front_table
);

/*
 * Frees the session table chain. The session tables it referenced
 * are not freed — the caller owns them.
 */
void
balancer_free_session_table_chain(
	struct agent *agent, struct balancer_session_table_chain *chain
);

/*
 * Creates a balancer handle from its full configuration.
 *
 * The session table and each VS handle must outlive the returned
 * balancer handle; they are not owned by it.
 */
struct balancer_handle *
balancer_create(
	struct agent *agent,
	const char *name,
	struct balancer_session_table_chain *session_table_chain,
	struct balancer_session_timeouts *timeouts,
	struct balancer_vs_handle **vs,
	size_t vs_count
);

/*
 * Pushes the given table as the new front (primary) session table.
 *
 * Workers look up sessions in the front table first and fall back to
 * the previous (back) table; a session found in the back table is
 * copied forward. New sessions are always created in the front table.
 *
 * Returns -1 if two session tables are already attached.
 */
int
balancer_session_table_chain_push_front(
	struct balancer_session_table_chain *session_table_chain,
	struct balancer_session_table *front_table
);

/*
 * Detaches the back session table.
 *
 * After this call, new workers ignore the detached table for lookups.
 *
 * Returns -1 if only one session table is attached.
 */
int
balancer_session_table_chain_pop_back(
	struct balancer_session_table_chain *session_table_chain
);

/*
 * Installs a balancer handle in the dataplane.
 *
 * If a balancer with the same name is already installed, it is
 * replaced; the previous handle becomes unused and the caller is
 * responsible for freeing it.
 *
 * Returns -1 on error, 0 on success.
 */
int
balancer_install(struct agent *agent, struct balancer_handle *handle);

/*
 * Frees a balancer handle. The session tables and VS handles
 * attached to the balancer are not freed — the caller owns them.
 */
void
balancer_free(struct agent *agent, struct balancer_handle *handle);

/*
 * Updates per-real weights for a VS. The weights array must have
 * length equal to the number of reals configured for the VS and be
 * indexed in the same order as they were passed at VS creation.
 * Returns 0 on success, -1 if the length does not match the number
 * of reals, or -2 on allocation failure.
 */
int
balancer_vs_update_real_weights(
	struct balancer_vs_handle *vs, const uint32_t *weights
);

/*
 * Updates per-real enabled flags for a VS. The states array must have
 * length equal to the number of reals configured for the VS and be
 * indexed in the same order as they were passed at VS creation.
 * Returns 0 on success, -1 if the length does not match the number
 * of reals, or -2 on allocation failure.
 */
int
balancer_vs_update_real_states(
	struct balancer_vs_handle *vs, const bool *states
);

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

extern const char *const balancer_common_counter_name;

extern const char *const balancer_l4_counter_name;