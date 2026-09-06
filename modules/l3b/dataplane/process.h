#pragma once

#include "config.h"

#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "lib/dataplane/worker/worker.h"

#include "lib/l3state/lookup.h"

#include "objects/l3b/api/l3b_session_table_object.h"
#include "objects/l3b/api/l3b_virtual_service_object.h"

#include "common/network.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/dscp.h"
#include "lib/dataplane/packet/encap.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"

#include <lib/filter/query.h>

// Per-service source filter: classifies incoming packets by source network and
// destination (service) port.
FILTER_QUERY_DECLARE(l3b_source_filter_ip4, net4_src, port_dst);
FILTER_QUERY_DECLARE(l3b_source_filter_ip6, net6_src, port_dst);

// Module-level destination filter: classifies incoming packets by destination
// network and protocol into a virtual service index.
FILTER_QUERY_DECLARE(l3b_destination_filter_ip4, net4_dst, proto_range);
FILTER_QUERY_DECLARE(l3b_destination_filter_ip6, net6_dst, proto_range);

/*
 * Resolve a module's object link to the virtual service it names.
 *
 * Walks the per-worker object link at link_idx to the linked object's
 * execution context and returns the service embedded in the l3b_virtual_service
 * object, or NULL when the link is missing or unresolved.
 */
static inline struct virtual_service *
l3b_module_ectx_virtual_service(
	struct module_ectx *module_ectx, uint64_t link_idx
) {
	struct module_object_link_ectx *link =
		object_link_get_address(module_ectx, link_idx);
	if (link == NULL) {
		return NULL;
	}

	struct object_ectx *object_ectx = ADDR_OF(&link->object_ectx);
	if (object_ectx == NULL) {
		return NULL;
	}

	struct cp_object *cp_object = ADDR_OF(&object_ectx->cp_object);
	if (cp_object == NULL) {
		return NULL;
	}

	struct l3b_virtual_service_object *object = container_of(
		cp_object, struct l3b_virtual_service_object, cp_object
	);
	return &object->virtual_service;
}

/*
 * Encapsulate packet into an IP-in-IP tunnel towards real_server.
 *
 * Returns -1 immediately when the server is disabled or the inner network
 * type is unsupported. The outer destination is the real server address; the
 * outer source is
 *
 *	source_net.addr XOR (inner_source AND ~source_net.mask)
 *
 * evaluated over the width of the real server address family.
 *
 * Returns 0 on success, -1 on failure.
 */
static inline int
l3b_real_server_process(
	struct real_server *real_server, struct packet *packet
) {
	if (real_server->state == real_state_disabled) {
		return -1;
	}

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	uint16_t inner_type = packet->network_header.type;

	const uint8_t *real_addr;
	const uint8_t *real_mask;
	const uint8_t *real_dst;
	size_t width;

	if (real_server->type == ip_family_ip4) {
		real_addr = real_server->source_net.v4.addr;
		real_mask = real_server->source_net.v4.mask;
		real_dst = real_server->destination_addr.v4.bytes;
		width = NET4_LEN;
	} else if (real_server->type == ip_family_ip6) {
		real_addr = real_server->source_net.v6.addr;
		real_mask = real_server->source_net.v6.mask;
		real_dst = real_server->destination_addr.v6.bytes;
		width = NET6_LEN;
	} else {
		return -1;
	}

	uint8_t inner_src[NET6_LEN] = {0};
	if (inner_type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *inner_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		memcpy(inner_src, &inner_hdr->src_addr, NET4_LEN);
	} else if (inner_type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		struct rte_ipv6_hdr *inner_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		memcpy(inner_src, &inner_hdr->src_addr, NET6_LEN);
	} else {
		return -1;
	}

	uint8_t outer_src[NET6_LEN];
	for (size_t idx = 0; idx < width; ++idx) {
		outer_src[idx] =
			real_addr[idx] ^ (inner_src[idx] & ~real_mask[idx]);
	}

	if (real_server->type == ip_family_ip4) {
		return packet_ip4_encap(
			packet, real_dst, outer_src, DSCP_MARK_ALWAYS
		);
	}
	return packet_ip6_encap(packet, real_dst, outer_src, DSCP_MARK_ALWAYS);
}

/*
 * Map a scheduler value onto a real server array index through the ring.
 *
 * Reads the ring under its seqlock: the selection is retried while the
 * control plane is replacing the entries, so a reader never acts on a mixture
 * of the old and new rings. An empty or permanently unstable ring fails with
 * -1 and the packet is dropped.
 */
static inline int
l3b_real_ring_select(
	struct real_ring *ring, uint32_t value, uint32_t *real_index
) {
	for (uint32_t attempt = 0; attempt < 4; ++attempt) {
		uint32_t sequence =
			__atomic_load_n(&ring->sequence, __ATOMIC_ACQUIRE);
		if (sequence & 1) {
			continue;
		}

		uint32_t count =
			__atomic_load_n(&ring->count, __ATOMIC_ACQUIRE);
		if (count == 0) {
			return -1;
		}

		uint32_t *server_indexes = ADDR_OF(&ring->server_indexes);
		uint32_t candidate = server_indexes[value % count];

		if (__atomic_load_n(&ring->sequence, __ATOMIC_ACQUIRE) ==
		    sequence) {
			*real_index = candidate;
			return 0;
		}
	}

	return -1;
}

/*
 * Whether the real server at the index can take traffic.
 */
static inline bool
l3b_real_is_ready(
	const struct virtual_service *virtual_service, uint32_t real_index
) {
	if (real_index >= virtual_service->real_server_count) {
		return false;
	}

	struct real_server *real_servers =
		ADDR_OF(&virtual_service->real_servers);
	return real_servers[real_index].state == real_state_enabled;
}

/*
 * Pick the real server for a flow through the scheduler ring.
 *
 * Returns 0 and stores the index on success, -1 when no server is ready.
 */
static inline int
l3b_schedule_real(
	struct virtual_service *virtual_service,
	struct packet *packet,
	uint32_t *real_index
) {
	uint32_t value = packet->hash & virtual_service->scheduler_hash_mask;
	value &= virtual_service->scheduler_index_mask;

	if (l3b_real_ring_select(
		    &virtual_service->real_ring, value, real_index
	    ) < 0) {
		return -1;
	}

	if (!l3b_real_is_ready(virtual_service, *real_index)) {
		return -1;
	}
	return 0;
}

/*
 * Process a single packet through a virtual service.
 *
 * The per-family filter is queried first; a non-match aborts with -1. A live
 * session record then pins the flow to its real server; only flows without
 * one go through the hash-derived scheduler, and the choice is written back
 * as a new session record. The chosen real performs the encapsulation.
 *
 * Returns 0 on success, -1 on failure.
 */
static inline int
l3b_virtual_service_process(
	struct dp_worker *dp_worker,
	struct virtual_service *virtual_service,
	struct packet *packet
) {
	uint16_t type = packet->network_header.type;
	const struct filter_query *query;
	struct filter *filter;

	if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		filter = &virtual_service->filter_ip4;
		query = l3b_source_filter_ip4;
	} else if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		filter = &virtual_service->filter_ip6;
		query = l3b_source_filter_ip6;
	} else {
		return -1;
	}

	struct packet *packets[1] = {packet};
	uint32_t result[1];
	filter_query(filter, query, packets, result, 1);
	if (result[0] == FILTER_RULE_INVALID) {
		return -1;
	}

	// An existing session keeps its real server; a session whose real
	// went away or was disabled falls through to the scheduler and
	// re-pins.
	uint32_t real_index;
	struct l3s_key key;
	l3s_key_of_packet(packet, &key);

	struct l3b_session_table_object *session_table =
		ADDR_OF(&virtual_service->session_table);
	if (session_table != NULL &&
	    l3s_table_lookup(
		    &session_table->table,
		    dp_worker->current_time,
		    &key,
		    &real_index
	    ) == 0 &&
	    l3b_real_is_ready(virtual_service, real_index)) {
		struct real_server *real_servers =
			ADDR_OF(&virtual_service->real_servers);
		return l3b_real_server_process(
			&real_servers[real_index], packet
		);
	}

	if (l3b_schedule_real(virtual_service, packet, &real_index) < 0) {
		return -1;
	}

	if (session_table != NULL) {
		l3s_table_insert(
			&session_table->table,
			dp_worker->idx,
			dp_worker->current_time,
			l3s_default_ttl(),
			&key,
			real_index
		);
	}

	struct real_server *real_servers =
		ADDR_OF(&virtual_service->real_servers);
	return l3b_real_server_process(&real_servers[real_index], packet);
}
