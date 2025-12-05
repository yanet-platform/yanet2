#pragma once

#include "icmp/error/info.h"
#include "lib/dataplane/packet/packet.h"

#include "lookup.h"
#include "meta.h"
#include "modules/balancer/api/vs.h"
#include "modules/balancer/state/session_table.h"
#include "rte_icmp.h"

#include <netinet/in.h>

#include "../../vs.h"

#include "../../../state/session.h"

////////////////////////////////////////////////////////////////////////////////

enum validate_packet_result {
	// Packet is invalid
	validate_packet_error = -1,

	// Not found session with the real on the current balancer
	validate_packet_session_not_found = 0,

	// Found session with real on the current balancer
	validate_packet_session_found = 1
};

////////////////////////////////////////////////////////////////////////////////

static inline int
fill_packet_meta_transport(
	struct packet_metadata *meta,
	struct icmp_packet_info *info,
	struct rte_mbuf *mbuf
) {
	if (info->inner.transport.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_tcp_hdr *, info->inner.transport.offset
		);
		fill_packet_metadata_tcp(tcp, meta);
		return 0;
	} else if (info->inner.transport.type == IPPROTO_UDP) {
		struct rte_udp_hdr *udp = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_udp_hdr *, info->inner.transport.offset
		);
		fill_packet_metadata_udp(udp, meta);
		return 0;
	} else {
		return -1;
	}
}

static inline int
validate_packet_ipv4(
	struct packet_ctx *ctx,
	struct packet_metadata *meta,
	struct virtual_service **vs
) {
	struct packet *packet = ctx->packet;
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv4_hdr *outer_ip_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	meta->network_proto = IPPROTO_IP;
	struct icmp_packet_info *info = &ctx->icmp_info;
	info->inner.network.type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	info->inner.network.offset =
		packet->transport_header.offset + sizeof(struct rte_icmp_hdr);

	struct balancer_icmp_module_stats *counter = ctx->counter.icmp;

	if (!fill_icmp_packet_info_ipv4(mbuf, info)) {
		counter->drop_icmpv4_payload_too_short_ip += 1;
		return -1;
	}

	struct rte_ipv4_hdr *inner_ip_hdr = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_ipv4_hdr *,
		packet->transport_header.offset + sizeof(struct rte_icmp_hdr)
	);
	if (inner_ip_hdr->src_addr != outer_ip_hdr->dst_addr) {
		counter->drop_icmpv4_umatching_src_from_original += 1;
		return -1;
	}

	if (mbuf->pkt_len <
	    info->inner.transport.offset + 2 * sizeof(rte_be16_t)) {
		counter->drop_icmpv4_payload_too_short_port += 1;
		return -1;
	}

	fill_packet_metadata_ipv4(inner_ip_hdr, meta);
	if (fill_packet_meta_transport(meta, info, mbuf)) {
		counter->drop_icmpv4_unexpected_transport += 1;
		return -1;
	}

	*vs = vs_v4_lookup(ctx);

	return 0;
}

static inline int
validate_packet_ipv6(
	struct packet_ctx *ctx,
	struct packet_metadata *meta,
	struct virtual_service **vs
) {
	struct packet *packet = ctx->packet;
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *outer_ip_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	struct icmp_packet_info *info = &ctx->icmp_info;
	info->inner.network.type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);
	info->inner.network.offset =
		packet->transport_header.offset + sizeof(struct rte_icmp_hdr);

	struct balancer_icmp_module_stats *counter = ctx->counter.icmp;

	if (!fill_icmp_packet_info_ipv6(mbuf, info)) {
		counter->drop_icmpv6_payload_too_short_ip += 1;
		return -1;
	}

	struct rte_ipv6_hdr *inner_ip_hdr = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_ipv6_hdr *,
		packet->transport_header.offset + sizeof(struct rte_icmp_hdr)
	);

	if (memcmp(inner_ip_hdr->src_addr, outer_ip_hdr->dst_addr, 16)) {
		counter->drop_icmpv6_umatching_src_from_original += 1;
		return -1;
	}

	if (mbuf->pkt_len <
	    info->inner.transport.offset + 2 * sizeof(rte_be16_t)) {
		counter->drop_icmpv6_payload_too_short_port += 1;
		return -1;
	}

	fill_packet_metadata_ipv6(inner_ip_hdr, meta);
	if (fill_packet_meta_transport(meta, info, mbuf)) {
		counter->drop_icmpv6_unexpected_transport += 1;
		return -1;
	}

	*vs = vs_v4_lookup(ctx);

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
	struct virtual_service *vs;

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
		validate_result = -1;
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
		return validate_packet_session_not_found;
	}

	// try to find session by id

	// fill session id
	struct session_id session_id;
	fill_session_id(
		&session_id, &meta, vs->flags & BALANCER_VS_PURE_L3_FLAG
	);

	// get real for the session
	uint32_t real_id = get_session_real(
		&ctx->state->session_table,
		&session_id,
		ctx->now,
		ctx->worker->idx
	);

	if (real_id == (uint32_t)-1) { // real not found
		return validate_packet_session_not_found;
	} else { // real found
		struct real *reals = ADDR_OF(&ctx->config->reals);
		struct real *real = &reals[real_id];
		packet_ctx_select_vs_icmp(ctx, vs, true);
		packet_ctx_select_real_icmp(ctx, real);
		return validate_packet_session_found;
	}
}