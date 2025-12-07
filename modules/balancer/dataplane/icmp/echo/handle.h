#pragma once

#include "common/network.h"
#include "flow/helpers.h"
#include "lib/dataplane/packet/packet.h"

#include "../../checksum.h"

#include "../../flow/common.h"
#include "../../flow/context.h"
#include "../../flow/stats.h"

#include <netinet/icmp6.h>
#include <netinet/in.h>
#include <netinet/ip_icmp.h>

#include "rte_icmp.h"
#include "rte_ip.h"
#include "rte_mbuf_core.h"

////////////////////////////////////////////////////////////////////////////////

static inline void
setup_icmp_header_on_echo_request(struct rte_icmp_hdr *icmp, int type) {
	icmp->icmp_type = type;
	icmp->icmp_code = 0;
}

////////////////////////////////////////////////////////////////////////////////

static inline void
send_packet(struct packet_ctx *ctx) {
	// update counters
	ICMP_V4_STATS_INC(echo_responses, ctx);
	packet_ctx_update_common_stats_on_outgoing_packet(ctx);

	// send packet to the next module
	packet_ctx_send_packet(ctx);
}

static inline void
handle_icmp_echo_ipv4(struct packet_ctx *ctx) {
	struct packet *packet = ctx->packet;
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	// setup icmp header (type and code)
	struct rte_icmp_hdr *icmp = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_icmp_hdr *, packet->transport_header.offset
	);
	setup_icmp_header_on_echo_request(icmp, ICMP_ECHOREPLY);

	// get ip header
	struct rte_ipv4_hdr *ip = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	// swap src and dst address to echo reply
	uint32_t tmp = ip->src_addr;
	ip->src_addr = ip->dst_addr;
	ip->dst_addr = tmp;

	// setup ttl, as it is reply
	ip->time_to_live = 64;

	// recalculate ip check sum
	ip->hdr_checksum = 0;
	ip->hdr_checksum = rte_ipv4_cksum(ip);

	// recalculate icmp checksum
	uint16_t icmp_checksum = ~icmp->icmp_cksum;
	icmp_checksum = csum_minus(icmp_checksum, ICMP_ECHO);
	icmp_checksum = csum_plus(icmp_checksum, ICMP_ECHOREPLY);
	icmp->icmp_cksum = ~icmp_checksum;

	// update counters and pass packet
	ctx->counter.icmp_v4->echo_responses += 1;
	send_packet(ctx); // updates common counters under the hood.
}

static inline void
handle_icmp_echo_ipv6(struct packet_ctx *ctx) {
	struct packet *packet = ctx->packet;
	// not forward echo packet to real. instead, response from
	// the balancer.
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_icmp_hdr *icmp = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_icmp_hdr *, packet->transport_header.offset
	);
	setup_icmp_header_on_echo_request(icmp, ICMP6_ECHO_REPLY);

	struct rte_ipv6_hdr *ip = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);
	uint8_t tmp[NET6_LEN];
	memcpy(tmp, ip->src_addr, NET6_LEN);
	memcpy(ip->src_addr, ip->dst_addr, NET6_LEN);
	memcpy(ip->dst_addr, tmp, NET6_LEN);

	ip->hop_limits = 64;

	uint16_t checksum = ~icmp->icmp_cksum;
	checksum = csum_minus(checksum, ICMP6_ECHO_REQUEST);
	checksum = csum_plus(checksum, ICMP6_ECHO_REPLY);
	icmp->icmp_cksum = ~checksum;

	// update counter and pass packet
	ctx->counter.icmp_v6->echo_responses += 1;
	send_packet(ctx); // updates common counters under the hood.
}