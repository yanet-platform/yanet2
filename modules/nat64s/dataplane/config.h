#pragma once

#include <stdatomic.h>
#include <stdint.h>

#include "lib/controlplane/config/cp_module.h"
#include "lib/fwstate/nat64state.h"
#include "objects/fwstate/api/fwstate_map_object.h"

#define NAT64S_OBJECT_LINK_NONE UINT64_MAX
#define NAT64S_PREFIX_LEN 12

/* Counter slots of the stats array */
enum nat64s_stat {
	NAT64S_TRANSLATED_V6_TO_V4 = 0,
	NAT64S_TRANSLATED_V4_TO_V6,
	NAT64S_WAN_STATE_MISS,
	NAT64S_NO_STATE_LINK,
	NAT64S_POOL_EMPTY,
	NAT64S_INSERT_FAILED,
	NAT64S_FRAGMENTED,
	NAT64S_MALFORMED,
	NAT64S_STAT_COUNT,
};

/*
 * Configuration of one stateful NAT64 instance.
 *
 * Routing around the instance is the pipeline's business: this config
 * carries only the translation identity. The NAT64 prefix is the one the
 * IPv4 side's addresses embed under (v4->v6 destinations); the pool is the
 * public IPv4 address space the lan-side flows translate to.
 *
 * Session state lives in two linked map objects: nat64state_lan_map keyed
 * by the original IPv6 flow and nat64state_wan_map keyed by the translated
 * IPv4 flow. A NULL table (no link) makes the module drop every packet,
 * counting it: without sessions stateful translation cannot answer the
 * return path.
 *
 * v1 keeps the flow's own source port: the pool allocation picks only the
 * public IPv4 address, by a hash of the lan key, and no transport rewrite
 * happens after the shared translation core runs. Two clients that pick
 * the same source port towards the same destination share the session's
 * wan tuple; the value ops keep the first allocation. Port translation is
 * future work.
 */
struct nat64s_module_config {
	struct cp_module cp_module;

	/* NAT64 prefix (first 12 bytes) for v4->v6 destinations */
	uint8_t prefix[NAT64S_PREFIX_LEN];

	/* Public IPv4 pool in network byte order, allocated in the module's
	 * memory context */
	struct {
		uint32_t *pool;
		uint64_t count;
	} pool;

	struct nat64state_timeouts timeouts;

	struct {
		uint16_t ipv4;
		uint16_t ipv6;
	} mtu;

	/* Index of the linked nat64state_lan_map object,
	 * NAT64S_OBJECT_LINK_NONE when the config declared none */
	uint64_t lan_object_link_idx;
	/* Index of the linked nat64state_wan_map object */
	uint64_t wan_object_link_idx;

	/* Instance discriminator embedded in the session keys; separates
	 * instances sharing one map object */
	uint32_t nat64s_id;

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
