#pragma once

#include <stdatomic.h>
#include <stdint.h>

#include "lib/controlplane/config/cp_module.h"
#include "lib/fwstate/nat64state.h"
#include "objects/fwstate/api/fwstate_map_object.h"

#define NAT64S_OBJECT_LINK_NONE UINT64_MAX
#define NAT64S_PREFIX_LEN 12

// How many neighboring ports a fresh allocation probes for a free
// external tuple before giving up.
#define NAT64S_MAX_PORT_PROBES 64

/* Counter slots of the stats array */
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
	NAT64S_STAT_COUNT,
};

/*
 * Configuration of one stateful NAT64 instance.
 *
 * Routing around the instance is the pipeline's business: this config
 * carries only the translation identity. The NAT64 prefixes are the ones
 * the IPv4 side's addresses may embed under (v4->v6 destinations); the
 * pool is the public IPv4 address space the lan-side flows translate to.
 * An outbound packet uses the prefix its destination sits under, and the
 * session keeps that prefix for its return traffic.
 *
 * Session state lives in one linked nat64state_map object: its lan fwtable
 * keyed by the original IPv6 flow, its wan fwtable keyed by the translated
 * IPv4 flow. A NULL table (no link) makes the module drop every packet,
 * counting it: without sessions stateful translation cannot answer the
 * return path.
 *
 * Ports translate inside the configured window: a source port already
 * inside it is kept, any other is rewritten to a hash-derived port of the
 * window (deterministic in the lan key, so every worker picks the same
 * one). Return traffic restores the client's original port from the wan
 * entry. Two flows whose keys hash to the same in-window port towards the
 * same destination still share a wan tuple; the value ops keep the first
 * allocation.
 */
struct nat64s_module_config {
	struct cp_module cp_module;

	/* NAT64 prefixes (first 12 bytes each, /96) the destinations may
	 * embed under, allocated in the module's memory context */
	struct {
		uint8_t *list;
		uint64_t count;
	} prefixes;

	/* Public IPv4 pool in network byte order, allocated in the module's
	 * memory context */
	struct {
		uint32_t *pool;
		uint64_t count;
	} pool;

	/* Translated source-port window [port_lo, port_hi), host order. A
	 * flow whose source port already lies inside keeps it; the others
	 * are translated to a hash-derived port of the window. */
	uint16_t port_lo;
	uint16_t port_hi;

	struct nat64state_timeouts timeouts;

	struct {
		uint16_t ipv4;
		uint16_t ipv6;
	} mtu;

	/* Index of the linked nat64state_map object, NAT64S_OBJECT_LINK_NONE
	 * when the config declared none */
	uint64_t map_object_link_idx;

	/* Runtime statistics, updated with relaxed atomics */
	uint64_t stats[NAT64S_STAT_COUNT];
};

/* Per-worker state resolved from the linked objects at ectx build time:
 * the lan and wan tables with their stash links, and the cached stats
 * block of the config. A NULL table on either side drops the packets of
 * that direction. */
struct nat64s_prepared {
	struct fwstate_map_link lan;
	struct fwstate_map_link wan;
	uint64_t *stats;
};
