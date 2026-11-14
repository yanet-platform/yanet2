/*
 * Stateful NAT64 module dataplane.
 *
 * The RFC7915 packet translation is the shared core of
 * lib/dataplane/packet/nat64; this module owns the session policy: an
 * IPv6 flow translates to a public IPv4 address picked from the pool, the
 * session is recorded in both directions (nat64state_lan_map keyed by the
 * original IPv6 flow, nat64state_wan_map keyed by the translated IPv4
 * flow), and return traffic finds its client through the wan table.
 *
 * Sessions age out through the fwstate layer sweep, exactly like firewall
 * state: untouched entries drain from older layers, touched ones are
 * promoted with their identity preserved.
 */

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

/* DPDK headers */
#include <rte_common.h>
#include <rte_config.h>
#include <rte_ether.h>
#include <rte_icmp.h>
#include <rte_ip.h>
#include <rte_log.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "yanet_build_config.h"

/* Project headers */
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/icmp.h"
#include "lib/dataplane/packet/nat64/nat64.h"
#include "lib/dataplane/packet/nat64/nat64_log.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/fwstate/stash_ectx.h"
#include "lib/statemap/fwtable.h"

#include "objects/fwstate/api/fwstate_map_nat64_lan_object.h"
#include "objects/fwstate/api/fwstate_map_nat64_wan_object.h"

#include "modules/nat64s/dataplane/config.h"
#include "modules/nat64s/dataplane/dataplane.h"

struct nat64s_module {
	struct module module;
};

// 64-bit FNV-1a over the key bytes: picks the pool address of a new
// session deterministically, so every worker derives the same allocation
// for the same flow.
static inline uint64_t
nat64s_key_hash(const void *data, size_t len) {
	const uint8_t *bytes = data;
	uint64_t hash = 0xcbf29ce484222325ull;
	for (size_t i = 0; i < len; ++i) {
		hash ^= bytes[i];
		hash *= 0x100000001b3ull;
	}
	return hash;
}

static inline void
nat64s_stats_bump(uint64_t *stats, enum nat64s_stat stat) {
	__atomic_add_fetch(&stats[stat], 1, __ATOMIC_RELAXED);
}

static inline struct nat64_ctx
nat64s_ctx(struct nat64s_module_config *config) {
	return (struct nat64_ctx){
		.mtu_ipv4 = config->mtu.ipv4,
		.mtu_ipv6 = config->mtu.ipv6,
		.malformed = &config->stats[NAT64S_MALFORMED],
	};
}

// The transport identity of a packet: protocol number and the two 16-bit
// ports (source and destination; echo identifiers for ICMP).
//
// Echo messages key on the identifier. Other ICMP messages carry no
// stable flow identity at this layer, so they read whatever the field
// holds: such a session is created on the lan side but never matches its
// reply through the wan table, which drops it.
static inline int
nat64s_transport_of(
	struct packet *packet,
	uint16_t *proto,
	uint16_t *src_port,
	uint16_t *dst_port
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	*proto = packet->transport_header.type;
	switch (*proto) {
	case IPPROTO_TCP: {
		struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		*src_port = rte_be_to_cpu_16(tcp->src_port);
		*dst_port = rte_be_to_cpu_16(tcp->dst_port);
		return 0;
	}
	case IPPROTO_UDP: {
		struct rte_udp_hdr *udp = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);
		*src_port = rte_be_to_cpu_16(udp->src_port);
		*dst_port = rte_be_to_cpu_16(udp->dst_port);
		return 0;
	}
	case IPPROTO_ICMPV6:
	case IPPROTO_ICMP: {
		struct yanet_icmp6_hdr *icmp = rte_pktmbuf_mtod_offset(
			mbuf,
			struct yanet_icmp6_hdr *,
			packet->transport_header.offset
		);
		*src_port = rte_be_to_cpu_16(icmp->yanet_icmp6_id);
		*dst_port = *src_port;
		return 0;
	}
	default:
		return -1;
	}
}

// TCP flags of the packet in the fwstate encoding, zero for non-TCP.
static inline uint8_t
nat64s_tcp_flags(struct packet *packet) {
	if (packet->transport_header.type != IPPROTO_TCP) {
		return 0;
	}
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_tcp_hdr *, packet->transport_header.offset
	);
	return fwstate_flags_from_tcp(tcp->tcp_flags);
}

// The lan side: translate an IPv6 flow to IPv4, creating or refreshing its
// session in both tables. A new session picks its public address from the
// pool by a hash of the lan key; an existing one keeps its allocation (the
// value ops enforce first-writer-wins on refresh). The flow keeps its own
// ports.
static int
nat64s_handle_v6(
	struct nat64s_module_config *config,
	struct nat64s_prepared *prepared,
	uint64_t worker_idx,
	uint64_t now,
	struct packet *packet
) {
	uint64_t *stats = prepared->stats;
	if (prepared->lan.table == NULL || prepared->wan.table == NULL) {
		nat64s_stats_bump(stats, NAT64S_NO_STATE_LINK);
		return -1;
	}

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv6_hdr *ipv6 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	uint16_t proto;
	uint16_t sport;
	uint16_t dport;
	if (nat64s_transport_of(packet, &proto, &sport, &dport)) {
		LOG_DBG(NAT64, "nat64s: no keyable transport header\n");
		return -1;
	}

	struct nat64state_lan_key key = {
		.hdr = {.proto = proto, .src_port = sport, .dst_port = dport},
		.nat64s_id = config->nat64s_id,
	};
	rte_memcpy(key.src_addr, ipv6->src_addr, 16);
	rte_memcpy(key.dst_addr, ipv6->dst_addr, 16);

	uint64_t ttl = nat64state_entry_ttl(
		proto, nat64s_tcp_flags(packet), &config->timeouts
	);

	struct nat64state_value value = {0};
	rwlock_t *lock = NULL;
	void *found = NULL;
	bool from_stale = false;
	if (fwtable_lookup(
		    prepared->lan.table, now, &key, &found, &lock, &from_stale
	    ) >= 0) {
		const struct nat64state_value *existing = found;
		value.ip4 = existing->ip4;
		value.port = existing->port;
		rwlock_read_unlock(lock);
	} else {
		uint32_t *pool = ADDR_OF(&config->pool.pool);
		if (config->pool.count == 0) {
			nat64s_stats_bump(stats, NAT64S_POOL_EMPTY);
			return -1;
		}
		value.ip4 =
			pool[nat64s_key_hash(&key, sizeof(key)) %
			     config->pool.count];
		value.port = sport;
	}

	value.updated_at = now;
	value.last_ttl = ttl;
	value.tcp_flags = nat64s_tcp_flags(packet);

	// Both directions are refreshed on every packet: return traffic only
	// reads the wan entry, so the lan side is what keeps an active flow's
	// reverse mapping alive.
	//
	// The wan entry goes first. Should the lan insert then fail, the
	// orphan wan entry merely ages out and the next packet retries both;
	// the opposite order would leave a lan entry whose flow can never be
	// answered until it expires.
	uint32_t remote_addr = 0;
	rte_memcpy(&remote_addr, &ipv6->dst_addr[12], sizeof(remote_addr));
	struct nat64state_wan_key wan_key = {
		.hdr =
			{
				.proto = proto,
				.src_port = value.port,
				.dst_port = dport,
			},
		.nat64s_id = config->nat64s_id,
		.local_addr = value.ip4,
		.remote_addr = remote_addr,
	};
	struct nat64state_value wan_value = {0};
	wan_value.updated_at = now;
	wan_value.last_ttl = ttl;
	wan_value.tcp_flags = value.tcp_flags;
	rte_memcpy(wan_value.src6, ipv6->src_addr, 16);

	if (fwtable_insert(
		    prepared->wan.table,
		    worker_idx,
		    now,
		    ttl,
		    &wan_key,
		    &wan_value,
		    &lock
	    ) < 0) {
		nat64s_stats_bump(stats, NAT64S_INSERT_FAILED);
		return -1;
	}
	rwlock_write_unlock(lock);

	if (fwtable_insert(
		    prepared->lan.table,
		    worker_idx,
		    now,
		    ttl,
		    &key,
		    &value,
		    &lock
	    ) < 0) {
		nat64s_stats_bump(stats, NAT64S_INSERT_FAILED);
		return -1;
	}
	rwlock_write_unlock(lock);

	struct nat64_ctx ctx = nat64s_ctx(config);
	int rc = nat64_translate_v6_to_v4(&ctx, packet, value.ip4);
	if (rc == 0) {
		nat64s_stats_bump(stats, NAT64S_TRANSLATED_V6_TO_V4);
	}
	return rc;
}

// The wan side: return traffic finds its client through the wan table,
// refreshes the entry and translates back. A miss is an attack on the pool
// or a reply whose session expired: dropped either way.
static int
nat64s_handle_v4(
	struct nat64s_module_config *config,
	struct nat64s_prepared *prepared,
	uint64_t worker_idx,
	uint64_t now,
	struct packet *packet
) {
	uint64_t *stats = prepared->stats;
	if (prepared->wan.table == NULL) {
		nat64s_stats_bump(stats, NAT64S_NO_STATE_LINK);
		return -1;
	}

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv4_hdr *ipv4 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	uint16_t proto;
	uint16_t sport;
	uint16_t dport;
	if (nat64s_transport_of(packet, &proto, &sport, &dport)) {
		LOG_DBG(NAT64, "nat64s: no keyable transport header\n");
		return -1;
	}

	struct nat64state_wan_key key = {
		.hdr = {.proto = proto, .src_port = dport, .dst_port = sport},
		.nat64s_id = config->nat64s_id,
		.local_addr = ipv4->dst_addr,
		.remote_addr = ipv4->src_addr,
	};

	void *found = NULL;
	rwlock_t *lock = NULL;
	bool from_stale = false;
	if (fwtable_lookup(
		    prepared->wan.table, now, &key, &found, &lock, &from_stale
	    ) < 0) {
		nat64s_stats_bump(stats, NAT64S_WAN_STATE_MISS);
		return -1;
	}
	const struct nat64state_value *session = found;
	uint8_t src6[16];
	rte_memcpy(src6, session->src6, sizeof(src6));
	rwlock_read_unlock(lock);

	// Return traffic keeps its own entry alive: a flow whose client is
	// silent for a while must not lose its reverse mapping while the
	// remote still talks.
	struct nat64state_value refresh = {0};
	refresh.updated_at = now;
	refresh.tcp_flags = nat64s_tcp_flags(packet);
	uint64_t ttl = nat64state_entry_ttl(
		proto, refresh.tcp_flags, &config->timeouts
	);
	refresh.last_ttl = ttl;
	rte_memcpy(refresh.src6, src6, sizeof(src6));
	if (fwtable_insert(
		    prepared->wan.table,
		    worker_idx,
		    now,
		    ttl,
		    &key,
		    &refresh,
		    &lock
	    ) < 0) {
		nat64s_stats_bump(stats, NAT64S_INSERT_FAILED);
		return -1;
	}
	rwlock_write_unlock(lock);

	struct nat64_ctx ctx = nat64s_ctx(config);
	int rc = nat64_translate_v4_to_v6(&ctx, packet, config->prefix, src6);
	if (rc == 0) {
		nat64s_stats_bump(stats, NAT64S_TRANSLATED_V4_TO_V6);
	}
	return rc;
}

void
nat64s_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct nat64s_module_config *config = container_of(
		module_ectx->abs_cp_module,
		struct nat64s_module_config,
		cp_module
	);
	struct nat64s_prepared *prepared = module_ectx->abs_module_prepared;
	uint64_t worker_idx = dp_worker->idx;
	uint64_t now = dp_worker->current_time;

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		int result = -1;

		// A session keys on the transport identity, so a fragment
		// without one cannot be translated.
		if (packet->fragment_offset != 0) {
			nat64s_stats_bump(prepared->stats, NAT64S_FRAGMENTED);
			result = -1;
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			result = nat64s_handle_v4(
				config, prepared, worker_idx, now, packet
			);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			result = nat64s_handle_v6(
				config, prepared, worker_idx, now, packet
			);
		}

		if (result) {
			packet_front_drop(packet_front, packet);
		} else {
			packet_front_output(packet_front, packet);
		}
	}
}

// Resolve the linked map objects into per-worker table links and cache the
// config's stats block.
static void
nat64s_module_commit_ectx(
	struct module_ectx *module_ectx, struct cp_module *cp_module
) {
	struct nat64s_module_config *config =
		container_of(cp_module, struct nat64s_module_config, cp_module);

	struct nat64s_prepared *prepared = module_ectx->abs_module_prepared;
	if (prepared == NULL) {
		return;
	}
	prepared->stats = config->stats;

	struct cp_object *lan_object = NULL;
	struct cp_object *wan_object = NULL;
	if (config->lan_object_link_idx != NAT64S_OBJECT_LINK_NONE) {
		struct module_object_link_ectx *link = object_link_get_address(
			module_ectx, config->lan_object_link_idx
		);
		if (link != NULL) {
			lan_object = link->abs_object_ectx->abs_cp_object;
		}
	}
	if (config->wan_object_link_idx != NAT64S_OBJECT_LINK_NONE) {
		struct module_object_link_ectx *link = object_link_get_address(
			module_ectx, config->wan_object_link_idx
		);
		if (link != NULL) {
			wan_object = link->abs_object_ectx->abs_cp_object;
		}
	}

	uint64_t worker_idx = fwstate_stash_worker_idx(module_ectx);
	fwstate_map_nat64_lan_object_link(
		lan_object, worker_idx, &prepared->lan
	);
	fwstate_map_nat64_wan_object_link(
		wan_object, worker_idx, &prepared->wan
	);
}

static void
nat64s_module_commit(struct dp_config *dp_config, struct cp_module *cp_module) {
	(void)dp_config;
	(void)cp_module;
}

struct module *
new_module_nat64s() {
	struct nat64s_module *module =
		(struct nat64s_module *)malloc(sizeof(*module));

	if (module == NULL) {
		RTE_LOG(ERR, NAT64, "nat64s: failed to allocate module\n");
		return NULL;
	}

	// The loader copies every field of the returned descriptor, so
	// heap garbage must not survive in the ones this constructor
	// leaves unset.
	memset(module, 0, sizeof(*module));

	snprintf(
		module->module.name, sizeof(module->module.name), "%s", "nat64s"
	);
	module->module.handler = nat64s_handle_packets;
	module->module.commit_handler = nat64s_module_commit;
	module->module.commit_ectx_handler = nat64s_module_commit_ectx;
	module->module.prepared_size = sizeof(struct nat64s_prepared);

	return &module->module;
}
