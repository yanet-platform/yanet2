#pragma once

#include <stdbool.h>
#include <stdint.h>

#include <lib/filter/filter.h>
#include <lib/filter/rule.h>

#include "common/network.h"
#include "lib/controlplane/config/cp_object.h"
#include "lib/errors/errors.h"

struct agent;
struct cp_object;
struct l3b_session_table_object;

// Shared-memory object type under which virtual services are registered.
#define L3B_VIRTUAL_SERVICE_OBJECT_TYPE "l3b_virtual_service"

// Name of the per-link packets counter every service object registers in its
// link counter registry: one value per worker, describing the relation
// between the service object and each module configuration linking it.
#define L3B_LINK_COUNTER_PACKETS "packets"

// Service flag: clamp the TCP MSS option on SYN packets to
// L3B_FIX_MSS_SIZE, so tunnels over small MTUs do not blackhole flows.
#define L3B_FLAG_FIX_MSS (1u << 0)

// Service flag: pure L3 balancing — the client's source port stays out of
// the session identity (zeroed in the key), so all flows of one source
// address share a single pin; classification uses the full port range.
#define L3B_FLAG_PURE_L3 (1u << 1)

// DSCP marking modes for the outer tunnel header, after the
// first-generation encoding: never (inherit the inner DSCP), only_default
// (mark when the inherited DSCP is zero) and always (mark unconditionally).
#define L3B_DSCP_MARK_NEVER (0u)
#define L3B_DSCP_MARK (1u)
#define L3B_DSCP_MARK_ALWAYS (2u)

// The MSS SYN packets are clamped down to; matches the first-generation
// balancer's value.
#define L3B_FIX_MSS_SIZE 1220

/*
 * Session lifetime policy, in seconds, after the first generation's
 * balancer: each record's deadline follows the packet that touched it last —
 * TCP by its flags, UDP its own value, everything else the catch-all.
 */
struct l3b_session_timeouts {
	uint32_t tcp;
	uint32_t tcp_syn;
	uint32_t tcp_syn_ack;
	uint32_t tcp_fin;
	uint32_t udp;
	uint32_t other;
};

// The default session lifetime policy, after the first-generation
// balancer's release defaults: every class 60 seconds.
struct l3b_session_timeouts
l3b_session_timeouts_defaults(void);

// Scheduler flag: derive the ring slot from the module link's per-worker
// packet counter (one-packet scheduling) instead of the packet hash; the
// scheduler masks then select counter bits.
#define L3B_SCHEDULER_COUNTER (1u << 0)

// Object-scoped counter names, every one a [packets, bytes] pair on the
// service object's own per-worker storages.
#define L3B_COUNTER_INCOMING "incoming"
#define L3B_COUNTER_FILTER_REJECTED "filter_rejected"
#define L3B_COUNTER_RING_EMPTY "ring_empty"
#define L3B_COUNTER_REAL_DISABLED "real_disabled"
// Generated ICMP echo replies on behalf of the service address.
#define L3B_COUNTER_ICMP_REPLIED "icmp_replied"
// Per-real throughput counters, "<name>/<real index>".

enum real_state {
	real_state_disabled = 0,
	real_state_enabled = 1,
};

/*
 * A single backend (real server) reachable through an IP-in-IP tunnel.
 *
 * The address family of the tunnel is selected by type; source_net and
 * destination_addr carry the matching union member.
 */
struct real_server {
	// Outer tunnel address family (ip_family_ip4 or ip_family_ip6).
	enum ip_family type;
	// Source network used to derive the outer source address.
	struct net source_net;
	// Real server destination address used as the outer destination.
	struct net_addr destination_addr;
	// Whether the server is eligible to receive traffic.
	enum real_state state;
};

/*
 * Source-side classification criteria of a virtual service.
 *
 * Each array is a relative pointer into object shared memory and is paired
 * with a matching count field. A packet matches when it falls into any of the
 * listed networks and port ranges.
 */
struct source_filter {
	uint32_t net6_count;
	struct net6 *net6s;

	uint32_t net4_count;
	struct net4 *net4s;

	uint32_t port_range_count;
	struct filter_port_range *port_ranges;
};

/*
 * Round-robin index ring used by the scheduler to pick a real server.
 *
 * server_indexes is a relative pointer to an array of real_server array
 * indexes; the dataplane selects among the first count entries.
 *
 * Replacement protocol: the control plane flips sequence to odd (unstable),
 * rewrites the entries, publishes the new count and flips sequence back to
 * even (stable). The dataplane reads sequence, count and the selected entry,
 * then re-reads sequence, retrying while the value changes — a reader that
 * acquired the old count can therefore never act on a mixture of the old and
 * new rings.
 */
struct real_ring {
	uint32_t *server_indexes;
	// Number of indexes currently populated; the dataplane selects among
	// the first count entries.
	uint32_t count;
	// Maximum number of indexes the array can hold; set at configuration
	// time.
	uint32_t capacity;
	// Seqlock version: even while the ring is stable, odd while the
	// control plane is replacing it.
	uint32_t sequence;
};

/*
 * A virtual service exposed to clients.
 *
 * Incoming traffic that matches one of the per-family filters is dispatched
 * to a real server: the session table first pins each client flow to a real,
 * and only flows without a live session go through the hash-derived
 * real_ring scheduler.
 *
 * Contract: the controlplane must filter_init both filter_ip4 and filter_ip6
 * before publishing a virtual service; the dataplane queries them directly
 * (value_table_get assumes a non-NULL backing table).
 */
struct virtual_service {
	// Backends available for this service.
	uint32_t real_server_count;
	struct real_server *real_servers;

	// Scheduler index ring over the real_servers array.
	struct real_ring real_ring;

	// Masks applied to the scheduling value to derive a ring slot.
	uint32_t scheduler_hash_mask;
	uint32_t scheduler_index_mask;
	// Scheduling mode bits; see L3B_SCHEDULER_COUNTER.
	uint32_t scheduler_flags;
	// Service behavior flags; see L3B_FLAG_FIX_MSS.
	uint32_t flags;
	// Session lifetime policy; zero fields fall back to the defaults.
	struct l3b_session_timeouts session_timeouts;
	// Outer-header DSCP marking, (dscp << 2) | mode with the modes above;
	// zero inherits the inner DSCP unchanged.
	uint32_t dscp_flags;

	// Object-scoped counter registry ids, COUNTER_INVALID when the
	// counter is absent. Incoming counts every dispatched packet; the
	// rest count the drop reasons in turn.
	uint64_t counter_incoming;
	uint64_t counter_filter_rejected;
	uint64_t counter_ring_empty;
	uint64_t counter_real_disabled;
	uint64_t counter_icmp_replied;
	// Per-real throughput counter id per real server entry.
	uint64_t *real_counter_ids;

	// Per-family classification of incoming packets.
	struct filter filter_ip6;
	struct filter filter_ip4;

	// Hash index of client flows pinned to real servers; created and
	// owned together with the service object.
	struct l3b_session_table_object *session_table;
};

/*
 * A named virtual service published as a standalone shared-memory object.
 *
 * The cp_object header carries the (type, name) identity, the reference
 * accounting across configuration generations and the object's own memory
 * context backing every allocation below; module configurations reference
 * the service by name through cp_module_link_object and resolve it to the
 * embedded struct at execution-context build time.
 */
struct l3b_virtual_service_object {
	struct cp_object cp_object;
	struct virtual_service virtual_service;
};

/*
 * Control-plane descriptors a virtual service object is built from.
 *
 * These structures are prefixed l3b_ to distinguish them from the unprefixed
 * shared-memory structures they are compiled into.
 */

/*
 * Source-side match criteria of a virtual service: the IPv6/IPv4 networks and
 * L4 port ranges a packet may originate from.
 */
struct l3b_source_filter_rule {
	struct filter_net6s net6s;
	struct filter_net4s net4s;
	struct filter_port_ranges port_ranges;
};

/*
 * A single backend reached through an IP-in-IP tunnel.
 */
struct l3b_real_server {
	// Outer tunnel address family (ip_family_ip4 or ip_family_ip6).
	enum ip_family type;
	// Real server destination address used as the outer destination.
	struct net_addr destination_addr;
	// Source network used to derive the outer source address.
	struct net source_net;
};

/*
 * A virtual service: the source filter rules that select its traffic, the
 * backends it dispatches to, and the scheduler masks applied to the packet
 * hash to pick a backend through the real-server ring.
 */
struct l3b_virtual_service {
	struct l3b_source_filter_rule *source_filter_rules;
	uint32_t source_filter_rule_count;

	struct l3b_real_server *real_servers;
	uint32_t real_server_count;

	uint32_t hash_mask;
	uint32_t index_mask;
	// Scheduling mode bits; see L3B_SCHEDULER_COUNTER.
	uint32_t scheduler_flags;
	// Service behavior flags; see L3B_FLAG_FIX_MSS.
	uint32_t flags;
	// Session lifetime policy; zero fields fall back to the defaults.
	struct l3b_session_timeouts session_timeouts;
	// Outer-header DSCP marking, (dscp << 2) | mode with the modes above;
	// zero inherits the inner DSCP unchanged.
	uint32_t dscp_flags;

	// Capacity of the real server ring; set at configuration time. The ring
	// starts empty and is populated via l3b_virtual_service_update_ring.
	uint32_t ring_capacity;

	// Hash index size of the service's session table; zero selects the
	// default.
	uint32_t session_index_size;
};

/*
 * Creation parameters of a virtual service object.
 *
 * adopt_session_table names the table object of the service being replaced:
 * the new service points at it, so every pinned flow survives the update with
 * its real server. NULL creates a fresh, empty table; the descriptor's
 * session_index_size then sizes it (and is ignored on adoption).
 */
struct l3b_virtual_service_create_config {
	struct agent *agent;
	const char *name;
	// Worker count covering every worker that will pin sessions.
	uint16_t worker_count;
	// Session table to adopt on an update; NULL for a fresh one.
	struct cp_object *adopt_session_table;
	// Control-plane descriptor of the service.
	const struct l3b_virtual_service *virtual_service;
};

// Allocate a named virtual service object from the creation parameters.
//
// The service (and, when created, its session table) are registered under the
// given name and published to the dataplane through agent_update_objects;
// module configurations reference the service by name via
// cp_module_link_object. *session_table receives the table's cp_object for
// publishing — the table the service adopted on an update, or the fresh one.
struct cp_object *
l3b_virtual_service_create(
	const struct l3b_virtual_service_create_config *config,
	struct cp_object **session_table,
	yanet_error **err
);

// Return the session table the service reads and writes, or NULL while the
// service carries none.
struct l3b_session_table_object *
l3b_virtual_service_session_table(const struct cp_object *cp_object);

// Read one [packets, bytes] counter of the named service object from a
// worker's published execution context. Values are per worker; callers
// aggregate across workers. Returns 0 on success, -1 when the object, its
// storage or the counter is absent.
int
l3b_virtual_service_counter_read(
	uint64_t ectx, const char *service, const char *counter, uint64_t *out
);

// Destroy the virtual service object once it is dangling — referenced by no
// live configuration generation. The session table is left alone: it is owned
// by the caller's handle, survives service updates and is destroyed through
// l3b_session_table_object_free when the service is deleted.
int
l3b_virtual_service_free(struct cp_object *cp_object, yanet_error **err);

// Populate the real server ring of a virtual service object. The count must
// not exceed the configured capacity and every index must reference a valid
// real server; indexes are written before the count is updated.
int
l3b_virtual_service_update_ring(
	struct cp_object *cp_object,
	const uint32_t *server_indexes,
	uint32_t server_index_count,
	yanet_error **err
);

// Enable or disable a single real server within a virtual service object,
// addressed by its index.
int
l3b_virtual_service_set_real_server_state(
	struct cp_object *cp_object,
	uint32_t real_server_index,
	bool enabled,
	yanet_error **err
);
