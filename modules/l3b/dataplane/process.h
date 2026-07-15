#pragma once

#include "config.h"

#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>

#include "common/network.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/encap.h"
#include "lib/dataplane/packet/packet.h"

#include <filter/query.h>

// Per-service source filter: classifies incoming packets by source network and
// destination (service) port.
FILTER_QUERY_DECLARE(l3b_source_filter_ip4, net4_src, port_dst);
FILTER_QUERY_DECLARE(l3b_source_filter_ip6, net6_src, port_dst);

// Module-level destination filter: classifies incoming packets by destination
// network and protocol into a virtual service index.
FILTER_QUERY_DECLARE(l3b_destination_filter_ip4, net4_dst, proto_range);
FILTER_QUERY_DECLARE(l3b_destination_filter_ip6, net6_dst, proto_range);

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
		return packet_ip4_encap(packet, real_dst, outer_src);
	}
	return packet_ip6_encap(packet, real_dst, outer_src);
}

/*
 * Map a scheduler value onto a real server array index through the ring.
 *
 * Returns 0 and stores the index on success, or -1 when the ring is empty.
 */
static inline int
l3b_real_ring_select(
	struct real_ring *ring, uint32_t value, uint32_t *real_index
) {
	if (ring->size == 0) {
		return -1;
	}

	uint32_t *server_indexes = ADDR_OF(&ring->server_indexes);
	*real_index = server_indexes[value % ring->size];
	return 0;
}

/*
 * Process a single packet through a virtual service.
 *
 * The per-family filter is queried first; a non-match aborts with -1. The
 * packet hash is then reduced by the scheduler masks to pick a real server
 * from the ring, which performs the encapsulation.
 *
 * Returns 0 on success, -1 on failure.
 */
static inline int
l3b_virtual_service_process(
	struct virtual_service *virtual_service, struct packet *packet
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

	uint32_t value = packet->hash & virtual_service->scheduler_hash_mask;
	value &= virtual_service->scheduler_index_mask;

	uint32_t real_index;
	if (l3b_real_ring_select(
		    &virtual_service->real_ring, value, &real_index
	    ) < 0) {
		return -1;
	}

	if (real_index >= virtual_service->real_server_count) {
		return -1;
	}

	struct real_server *real_servers =
		ADDR_OF(&virtual_service->real_servers);
	return l3b_real_server_process(&real_servers[real_index], packet);
}
