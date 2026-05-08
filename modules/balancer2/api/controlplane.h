#pragma once

#include "common/network.h"
#include "filter/rule.h"
#include "lib/errors/errors.h"
#include "modules/balancer2/dataplane/types/session.h"

struct agent;
struct balancer_handle;

enum balancer_tunnel_kind {
	balancer_tunnel_kind_ip,
	balancer_tunnel_kind_gre,
};

/*
 * Configuration of a single real (backend).
 */
struct balancer_real_config {
	struct net_addr dst;
	struct net src;
	enum ip_family ip_family;
	const char *counter_name;
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
	const char *counter_name;
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

	enum transport_proto transport;

	struct balancer_allowed_sources *allowed_sources;
	size_t allowed_sources_count;

	enum balancer_vs_sched scheduler;
	enum balancer_tunnel_kind tunnel;

	struct balancer_real_config *reals;
	size_t real_count;

	bool fix_mss;

	const char *counter_name;
};

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
 * Creates a balancer handle from its full configuration.
 *
 * The session table chain and session timeouts must be non-NULL. The
 * session table chain must outlive the returned balancer handle; it
 * is referenced, not owned. The session timeouts are copied into the
 * handle.
 */
struct balancer_handle *
balancer_create(
	struct agent *agent,
	const char *name,
	struct balancer_session_table_chain *session_table_chain,
	struct balancer_session_timeouts *timeouts,
	const struct balancer_vs_config *vs,
	uint32_t vs_count,
	const char *common_counter_name,
	const char *l4_counter_name,
	yanet_error **error
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
balancer_install(
	struct agent *agent, struct balancer_handle *handle, yanet_error **error
);

/*
 * Frees a balancer handle. The session table chain attached to the
 * balancer is not freed — the caller owns it.
 */
void
balancer_free(struct agent *agent, struct balancer_handle *handle);

/*
 * Updates per-real weights for a VS. The weights array must have
 * length equal to the number of reals configured for the VS and be
 * indexed in the same order as they were passed at VS creation.
 * Returns 0 on success, -1 on error.
 */
int
balancer_vs_update_real_weights(
	struct balancer_handle *balancer,
	uint32_t vs_idx,
	const uint32_t *weights,
	yanet_error **error
);

/*
 * Updates per-real enabled flags for a VS. The states array must have
 * length equal to the number of reals configured for the VS and be
 * indexed in the same order as they were passed at VS creation.
 *
 * Reals start disabled after balancer_create. Until this function is
 * called with a state of true for a given real, that real does not
 * receive traffic.
 *
 * Returns 0 on success, -1 on error.
 */
int
balancer_vs_update_real_states(
	struct balancer_handle *balancer,
	uint32_t vs_idx,
	const bool *states,
	yanet_error **error
);

/*
 * A session table holds active session entries — one per tracked
 * flow — mapping a connection key to its selected real. The table
 * has a fixed capacity, set at creation time, that bounds the number
 * of concurrent sessions it can store.
 *
 * A session table is used by a balancer through a session table
 * chain; see the balancer_session_table_chain documentation above
 * for how front and back tables interact during lookups and inserts.
 */
struct balancer_session_table;

/*
 * Creates a session table with the given capacity (number of session
 * entries).
 */
struct balancer_session_table *
balancer_create_session_table(
	struct agent *agent, size_t capacity, yanet_error **error
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
	struct balancer_session_table *front_table,
	yanet_error **error
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
	struct balancer_session_table_chain *session_table_chain,
	yanet_error **error
);

void
balancer_free_session_table(
	struct agent *agent, struct balancer_session_table *table
);

// TODO:
// session table iter.

/*
 * Creates a session table chain seeded with the given front table.
 * The table is not owned by the chain and must outlive it.
 * Returns NULL on allocation failure.
 */
struct balancer_session_table_chain *
balancer_create_session_table_chain(
	struct agent *agent,
	struct balancer_session_table *front_table,
	yanet_error **error
);

/*
 * Frees the session table chain. The session tables it referenced
 * are not freed — the caller owns them.
 */
void
balancer_free_session_table_chain(
	struct agent *agent, struct balancer_session_table_chain *chain
);
