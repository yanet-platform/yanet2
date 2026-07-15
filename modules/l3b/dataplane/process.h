#pragma once

#include "config.h"

#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>

#include "common/network.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/encap.h"
#include "lib/dataplane/packet/packet.h"

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
	struct l3b_real_server *real_server, struct packet *packet
) {
	if (real_server->state == l3b_real_state_disabled) {
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
	} else {
		real_addr = real_server->source_net.v6.addr;
		real_mask = real_server->source_net.v6.mask;
		real_dst = real_server->destination_addr.v6.bytes;
		width = NET6_LEN;
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
