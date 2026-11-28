#pragma once

#include <stdint.h>

#include "lib/controlplane/config/cp_module.h"
#include "lib/fwstate/nat64state.h"
#include "objects/fwstate/api/fwstate_map_object.h"

#define NAT64S_OBJECT_LINK_NONE UINT64_MAX
#define NAT64S_PREFIX_LEN 12

// How many neighboring ports a fresh allocation probes for a free
// external tuple before giving up.
#define NAT64S_MAX_PORT_PROBES 64

/* Slots of the module's counters, each registered by name at creation */
enum nat64s_stat {
	NAT64S_TRANSLATED_V6_TO_V4 = 0,
	NAT64S_TRANSLATED_V4_TO_V6,
	NAT64S_WAN_STATE_MISS,
	NAT64S_NO_STATE_LINK,
	NAT64S_POOL_EMPTY,
	NAT64S_NO_PREFIX,
	NAT64S_HAIRPIN,
	NAT64S_NOT_SYN,
	NAT64S_INSERT_FAILED,
	NAT64S_FRAGMENTED,
	NAT64S_MALFORMED,
	NAT64S_ICMP_UNSUPPORTED,
	NAT64S_NON_GLOBAL,
	NAT64S_SESSION_GONE,
	NAT64S_STAT_COUNT,
};

/*
 * Configuration of one stateful NAT64 instance.
 *
 * Routing around the instance is the pipeline's business: this config
 * carries only the translation identity, the prefixes, the pool, the port
 * window, the session lifetimes, the MTU limits and the link to the
 * session tables, each with the rule the dataplane applies to it stated
 * at its field.
 */
struct nat64s_module_config {
	struct cp_module cp_module;

	/* NAT64 prefixes (first 12 bytes each, /96), allocated in the
	 * module's memory context.
	 *
	 * An outbound packet is translated under the prefix its destination
	 * sits under, and the session keeps that prefix for its return
	 * traffic. A source or destination no router forwards a packet under
	 * — the unspecified, the loopback, a link-local, a multicast or an
	 * IPv4-mapped address — is refused under every prefix, and so is a
	 * destination standing for an IPv4 address no unicast packet can be
	 * sent to. Under the Well-Known Prefix a source or destination
	 * standing for an address of a block no global traffic uses is
	 * refused as well, whatever the configuration carries (RFC 6052
	 * 3.1). All of it before a packet can take a session. */
	struct {
		uint8_t *list;
		uint64_t count;
	} prefixes;

	/* Public IPv4 pool in network byte order, kept sorted, allocated in
	 * the module's memory context: the public address space the
	 * lan-side flows translate to.
	 *
	 * A session takes one address of it by a hash of the client endpoint,
	 * and is served while that address is one of the pool's and its
	 * prefix one of the configured ones: an update that takes either away
	 * leaves the session unserved on both sides, its packets dropped and
	 * counted, for as long as its entries live. */
	struct {
		uint32_t *pool;
		uint64_t count;
	} pool;

	/* Translated source-port window [port_lo, port_hi), host order, the
	 * upper bound up to 65536 so that the window may end with port
	 * 65535.
	 *
	 * A flow whose source port already lies inside keeps it; the others
	 * are translated to a hash-derived port of the window, derived from
	 * the client's address, protocol and port alone so every worker picks
	 * the same one. Return traffic restores the client's original port
	 * from the wan entry, zero included. Towards one remote an external
	 * tuple belongs to one client: a new session whose tuple is already
	 * taken moves to a neighboring port of the window. */
	uint32_t port_lo;
	uint32_t port_hi;

	/* The three session lifetimes in seconds, as the request carried them:
	 * one for every session, a longer one for an established TCP session,
	 * and the transitory one a TCP session moves to once both ends have
	 * closed it. Their rules are stated with the structure. */
	struct nat64state_timeouts timeouts;

	/* The MTU limits the translation works with, in bytes, stored as a
	 * request's zero has already selected its family's default.
	 *
	 * A translated error that reports a packet too big carries a next-hop
	 * MTU held to both limits, each adjusted for the twenty bytes an IPv6
	 * header adds over an IPv4 one. Towards the client that is the IPv6
	 * limit as it is and the IPv4 one widened by them, floored at 1280;
	 * towards the remote the IPv6 one narrowed by them and the IPv4 limit
	 * as it is, floored at 68. An IPv6 limit stays within 32767: the
	 * translation keeps the difference between the limit and a packet's
	 * length in a signed 16-bit number. */
	struct {
		uint16_t ipv4;
		uint16_t ipv6;
	} mtu;

	/* Index of the linked nat64state_map object, NAT64S_OBJECT_LINK_NONE
	 * when the config declared none.
	 *
	 * The map holds the lan fwtable, keyed by the original IPv6 flow, and
	 * the wan fwtable, keyed by the translated IPv4 flow. Without a link
	 * the module drops every packet, counting it: without session state
	 * the return path cannot be answered. */
	uint64_t map_object_link_idx;

	/* Ids of the module's counters in its counter registry, one per slot
	 * of enum nat64s_stat */
	uint64_t counter_ids[NAT64S_STAT_COUNT];
};

/* Per-worker state resolved at ectx build time: the lan and wan tables of
 * the linked objects with their stash links, and the worker's own
 * counters. A NULL table on either side drops the packets of that
 * direction. */
struct nat64s_prepared {
	struct fwstate_map_link lan;
	struct fwstate_map_link wan;
	/* One address per slot of enum nat64s_stat */
	uint64_t *stats[NAT64S_STAT_COUNT];
	/* Where a counter the worker's storage does not carry is counted */
	uint64_t unset;
};
