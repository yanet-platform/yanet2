#pragma once

#include "common/network.h"
#include "flow/common.h"
#include "handler/real.h"
#include "icmp/error/info.h"
#include "lib/dataplane/packet/packet.h"

#include "api/stats.h"
#include "lookup.h"
#include "meta.h"
#include "rte_byteorder.h"
#include "rte_icmp.h"
#include "session_table.h"
#include "state/state.h"

#include <netinet/in.h>

////////////////////////////////////////////////////////////////////////////////

enum validate_packet_result {
	// Packet is invalid
	validate_packet_error = -1,

	// Not found session with the real on the current balancer
	validate_packet_session_not_found = 0,

	// Virtual service not recognized
	validate_packet_vs_not_found = 1,

	// Found session with real on the current balancer
	validate_packet_session_found = 2
};

////////////////////////////////////////////////////////////////////////////////

static inline void
packet_swap_headers(
	struct packet *packet,
	struct network_header *network,
	struct transport_header *transport
) {
	// set network heder
	{
		struct network_header tmp = packet->network_header;
		packet->network_header = *network;
		*network = tmp;
	}

	// set transport header
	{
		struct transport_header tmp = packet->transport_header;
		packet->transport_header = *transport;
		*transport = tmp;
	}
}

static inline void
packet_swap_src_dst(struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	// Swap IP addresses
	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *inner_ip_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		uint32_t tmp = inner_ip_hdr->src_addr;
		inner_ip_hdr->src_addr = inner_ip_hdr->dst_addr;
		inner_ip_hdr->dst_addr = tmp;
	} else { // ipv6
		struct rte_ipv6_hdr *inner_ip_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		uint8_t tmp[16];
		memcpy(tmp, inner_ip_hdr->src_addr, NET6_LEN);
		memcpy(inner_ip_hdr->src_addr, inner_ip_hdr->dst_addr, NET6_LEN
		);
		memcpy(inner_ip_hdr->dst_addr, tmp, NET6_LEN);
	}

	// Swap transport ports
	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		uint16_t tmp_port = tcp->src_port;
		tcp->src_port = tcp->dst_port;
		tcp->dst_port = tmp_port;
	} else if (packet->transport_header.type == IPPROTO_UDP) {
		struct rte_udp_hdr *udp = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);
		uint16_t tmp_port = udp->src_port;
		udp->src_port = udp->dst_port;
		udp->dst_port = tmp_port;
	}
}

static inline int
validate_packet_ipv4(
	struct packet_ctx *ctx, struct packet_metadata *meta, struct vs **vs
) {
	struct packet *packet = ctx->packet;
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv4_hdr *outer_ip_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	meta->network_proto = IPPROTO_IP;
	struct icmp_packet_info info;
	info.network.type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	// ICMPv4 error messages use 8-byte header (type + code + checksum +
	// 4-byte unused) This matches sizeof(struct rte_icmp_hdr) which is 8
	// bytes
	info.network.offset = packet->transport_header.offset + 8;

	if (fill_icmp_packet_info_ipv4(mbuf, &info) != 0) {
		ICMP_V4_STATS_INC(payload_too_short_ip, ctx);
		return -1;
	}

	struct rte_ipv4_hdr *inner_ip_hdr = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_ipv4_hdr *,
		packet->transport_header.offset + sizeof(struct rte_icmp_hdr)
	);
	if (inner_ip_hdr->src_addr != outer_ip_hdr->dst_addr) {
		ICMP_V4_STATS_INC(unmatching_src_from_original, ctx);
		return -1;
	}

	if (mbuf->pkt_len < info.transport.offset + 2 * sizeof(rte_be16_t)) {
		ICMP_V4_STATS_INC(payload_too_short_port, ctx);
		return -1;
	}

	// swap source address and destination address
	// on the inner packet. after that, destination address should be equal
	// to the virtual service address. also, we need to swap transport
	// proto source and destination.
	packet_swap_headers(ctx->packet, &info.network, &info.transport);
	packet_swap_src_dst(ctx->packet);

	// fill packet metadata
	if (fill_packet_metadata(packet, meta)) {
		ICMP_V4_STATS_INC(unexpected_transport, ctx);
		packet_swap_src_dst(ctx->packet);
		packet_swap_headers(
			ctx->packet, &info.network, &info.transport
		);
		return -1;
	}

	// lookup virtual service
	*vs = vs_v4_lookup(ctx);
	if (*vs == NULL) {
		ICMP_V4_STATS_INC(unrecognized_vs, ctx);
	}

	// swap headers and src dst back
	packet_swap_src_dst(ctx->packet);
	packet_swap_headers(ctx->packet, &info.network, &info.transport);

	return 0;
}

static inline int
validate_packet_ipv6(
	struct packet_ctx *ctx, struct packet_metadata *meta, struct vs **vs
) {
	struct packet *packet = ctx->packet;
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *outer_ip_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	meta->network_proto = IPPROTO_IPV6;
	struct icmp_packet_info info;
	info.network.type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

	// ICMPv6 error messages use 8-byte header (type + code + checksum +
	// 4-byte unused) This is different from rte_icmp_hdr which is for echo
	// messages
	info.network.offset = packet->transport_header.offset + 8;

	if (fill_icmp_packet_info_ipv6(mbuf, &info) != 0) {
		ICMP_V6_STATS_INC(payload_too_short_ip, ctx);
		return -1;
	}

	struct rte_ipv6_hdr *inner_ip_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, info.network.offset
	);

	if (memcmp(inner_ip_hdr->src_addr, outer_ip_hdr->dst_addr, 16)) {
		ICMP_V6_STATS_INC(unmatching_src_from_original, ctx);
		return -1;
	}

	if (mbuf->pkt_len < info.transport.offset + 2 * sizeof(rte_be16_t)) {
		ICMP_V6_STATS_INC(payload_too_short_port, ctx);
		return -1;
	}

	// swap source address and destination address
	// on the inner packet. after that, destination address should be equal
	// to the virtual service address. also, we need to swap transport
	// proto source and destination.
	packet_swap_headers(ctx->packet, &info.network, &info.transport);
	packet_swap_src_dst(ctx->packet);

	// fill packet metadata
	if (fill_packet_metadata(packet, meta)) {
		ICMP_V6_STATS_INC(unexpected_transport, ctx);
		packet_swap_src_dst(ctx->packet);
		packet_swap_headers(
			ctx->packet, &info.network, &info.transport
		);
		return -1;
	}

	// lookup virtual service
	*vs = vs_v6_lookup(ctx);
	if (*vs == NULL) {
		ICMP_V6_STATS_INC(unrecognized_vs, ctx);
	}

	// swap headers and src dst back
	packet_swap_src_dst(ctx->packet);
	packet_swap_headers(ctx->packet, &info.network, &info.transport);

	return 0;
}

////////////////////////////////////////////////////////////////////////////////

static inline int
validate_and_parse_packet(struct packet_ctx *ctx) {
	// Fill packet metadata and find virtual service for which
	// original packet is intended to.
	//
	// After that, try to find session with real
	// in the current balancer state.

	struct packet_metadata meta;
	struct vs *vs;

	// validate packet, set metadata and packet icmp info
	// (in the packet context).
	// if validation failed, update corresponding counters.
	int validate_result;
	switch (ctx->packet->transport_header.type) {
	case IPPROTO_ICMP: {
		validate_result = validate_packet_ipv4(ctx, &meta, &vs);
		break;
	}
	case IPPROTO_ICMPV6: {
		validate_result = validate_packet_ipv6(ctx, &meta, &vs);
		break;
	}
	default: {
		// impossible, because previously it was
		// checked packet is icmp or icmpv6
		assert(false);
	}
	}

	// if failed to validate packet, return error.
	if (validate_result) {
		return validate_packet_error;
	}

	// if virtual service not found,
	// there can not be session with real on the current balancer.
	// so, we return corresponding status.
	if (vs == NULL) {
		return validate_packet_vs_not_found;
	} else {
		packet_ctx_set_vs(ctx, vs);
	}

	// try to find session by id

	// fill session id
	struct session_id session_id;
	fill_session_id(&session_id, &meta, vs);

	// begin critical section
	uint64_t current_gen = session_table_begin_cs(
		&ctx->balancer_state->session_table, ctx->worker->idx
	);

	// get real for the session
	uint32_t real_id = get_session_real(
		&ctx->balancer_state->session_table,
		current_gen,
		&session_id,
		ctx->now
	);

	// end critical section
	session_table_end_cs(
		&ctx->balancer_state->session_table, ctx->worker->idx
	);

	if (real_id == (uint32_t)-1) { // real not found
		// end critical section
		return validate_packet_session_not_found;
	} else { // real found
		struct real *reals = ADDR_OF(&ctx->handler->reals);
		struct real *real = &reals[real_id];
		packet_ctx_set_vs(ctx, vs);
		packet_ctx_set_real(ctx, real);
		return validate_packet_session_found;
	}
}