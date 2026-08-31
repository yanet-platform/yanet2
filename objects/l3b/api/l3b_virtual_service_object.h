#pragma once

#include <stdbool.h>
#include <stdint.h>

#include <lib/filter/filter.h>
#include <lib/filter/rule.h>

#include "common/network.h"
#include "lib/controlplane/config/cp_object.h"
#include "lib/errors/errors.h"

struct agent;

// Shared-memory object type under which virtual services are registered.
#define L3B_VIRTUAL_SERVICE_OBJECT_TYPE "l3b_virtual_service"

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
 * to a real server chosen through real_ring by a hash-derived scheduler.
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

	// Masks applied to the packet hash to derive a ring slot.
	uint32_t scheduler_hash_mask;
	uint32_t scheduler_index_mask;

	// Per-family classification of incoming packets.
	struct filter filter_ip6;
	struct filter filter_ip4;
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

	// Capacity of the real server ring; set at configuration time. The ring
	// starts empty and is populated via l3b_virtual_service_update_ring.
	uint32_t ring_capacity;
};

// Allocate a named virtual service object in the agent's shared memory from
// its control-plane descriptor. The object is registered under
// (L3B_VIRTUAL_SERVICE_OBJECT_TYPE, name) and is published to the dataplane
// through agent_update_objects; module configurations reference it by name
// via cp_module_link_object.
struct cp_object *
l3b_virtual_service_create(
	struct agent *agent,
	const char *name,
	const struct l3b_virtual_service *virtual_service,
	yanet_error **err
);

// Destroy the virtual service object when it is dangling — referenced by no
// live configuration generation. A refused destroy is reported through err
// and the caller must retry later.
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
