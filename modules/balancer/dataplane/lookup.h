#pragma once

#include "common/lpm.h"
#include "common/memory_address.h"
#include "common/network.h"

#include "lib/dataplane/packet/packet.h"

#include <filter/query.h>

#include <assert.h>

#include <rte_ether.h>
#include <rte_ip.h>

#include "handler/handler.h"

#include "flow/common.h"
#include "flow/context.h"
#include "flow/helpers.h"

////////////////////////////////////////////////////////////////////////////////

FILTER_QUERY_DECLARE(vs_v4_sig, net4_dst, port_dst, proto);

static inline uint32_t
vs_v4_table_lookup(struct packet_handler *handler, struct packet *packet) {
	struct value_range *result;
	FILTER_QUERY(&handler->vs_v4, vs_v4_sig, &packet, &result, 1);
	if (result->count == 0) {
		return -1;
	}
	/// @todo: actions_count > 1 ?
	uint32_t service_id = ADDR_OF(&result->values)[0];
	return service_id;
}

////////////////////////////////////////////////////////////////////////////////

FILTER_QUERY_DECLARE(vs_v6_sig, net6_dst, port_dst, proto);

static inline uint32_t
vs_v6_table_lookup(struct packet_handler *handler, struct packet *packet) {
	struct value_range *result;
	FILTER_QUERY(&handler->vs_v6, vs_v6_sig, &packet, &result, 1);
	if (result->count == 0) {
		return -1;
	}
	/// @todo: actions_count > 1 ?
	uint32_t service_id = ADDR_OF(&result->values)[0];
	return service_id;
}

////////////////////////////////////////////////////////////////////////////////

static inline struct vs *
vs_v4_lookup(struct packet_ctx *ctx) {
	struct packet_handler *handler = ctx->handler;
	// get id of the virtual service
	uint32_t service_id = vs_v4_table_lookup(handler, ctx->packet);
	if (service_id == (uint32_t)-1) {
		return NULL;
	}
	struct vs *vs = ADDR_OF(&handler->vs) + service_id;

	// set virtual service
	packet_ctx_set_vs(ctx, vs);

	return vs;
}

static inline bool
vs_v4_fw(struct packet_ctx *ctx, struct vs *vs, struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	if (lpm_lookup(
		    &vs->src_filter, NET4_LEN, (uint8_t *)&ipv4_hdr->src_addr
	    ) == LPM_VALUE_INVALID) {

		// update counter
		VS_STATS_INC(packet_src_not_allowed, ctx);

		return false;
	}

	return true;
}

static inline bool
vs_v4_announced(struct packet_ctx *ctx) {
	struct packet_handler *handler = ctx->handler;
	struct packet *packet = ctx->packet;
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);
	return lpm_lookup(
		       &handler->announce_ipv4,
		       NET4_LEN,
		       (uint8_t *)&ipv4_hdr->dst_addr
	       ) != LPM_VALUE_INVALID;
}

static inline bool
vs_v6_announced(struct packet_ctx *ctx) {
	struct packet_handler *handler = ctx->handler;
	struct packet *packet = ctx->packet;
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);
	return lpm_lookup(
		       &handler->announce_ipv6,
		       NET6_LEN,
		       (uint8_t *)&ipv6_hdr->dst_addr
	       ) != LPM_VALUE_INVALID;
}

////////////////////////////////////////////////////////////////////////////////

static inline struct vs *
vs_v6_lookup(struct packet_ctx *ctx) {
	struct packet_handler *handler = ctx->handler;
	uint32_t service_id = vs_v6_table_lookup(handler, ctx->packet);
	if (service_id == (uint32_t)-1) {
		return NULL;
	}
	struct vs *vs = ADDR_OF(&handler->vs) + service_id;

	// set virtual service
	packet_ctx_set_vs(ctx, vs);

	return vs;
}

static inline bool
vs_v6_fw(struct packet_ctx *ctx, struct vs *vs, struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	if (lpm_lookup(
		    &vs->src_filter, NET6_LEN, (uint8_t *)&ipv6_hdr->src_addr
	    ) == LPM_VALUE_INVALID) {

		// update counter
		VS_STATS_INC(packet_src_not_allowed, ctx);

		return false;
	}

	return true;
}

////////////////////////////////////////////////////////////////////////////////

static inline struct vs *
vs_lookup_and_fw(struct packet_ctx *ctx) {
	struct packet *packet = ctx->packet;
	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct vs *vs = vs_v4_lookup(ctx);
		if (vs == NULL || !vs_v4_fw(ctx, vs, packet)) {
			return NULL;
		}
		return vs;
	} else { // ipv6
		struct vs *vs = vs_v6_lookup(ctx);
		if (vs == NULL || !vs_v6_fw(ctx, vs, packet)) {
			return NULL;
		}
		return vs;
	}
}
