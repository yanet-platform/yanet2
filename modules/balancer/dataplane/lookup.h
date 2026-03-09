#pragma once

#include "common/lpm.h"
#include "common/memory_address.h"
#include "common/network.h"

#include "counters/counters.h"
#include "flow/helpers.h"
#include "lib/dataplane/packet/packet.h"

#include <filter/query.h>

#include <assert.h>

#include <rte_ether.h>
#include <rte_ip.h>

#include "handler/handler.h"

#include "flow/common.h"
#include "flow/context.h"

////////////////////////////////////////////////////////////////////////////////

FILTER_QUERY_DECLARE(
	vs_lookup_ipv4, net4_fast_dst, port_fast_dst, proto_range_fast
);
FILTER_QUERY_DECLARE(vs_acl_ipv4, net4_fast_src, port_fast_src);

static inline uint32_t
vs_v4_table_lookup(struct packet_handler *handler, struct packet *packet) {
	struct value_range *result;
	FILTER_QUERY(
		ADDR_OF(&handler->vs_ipv4.filter),
		vs_lookup_ipv4,
		&packet,
		&result,
		1
	);
	if (result->count == 0) {
		return -1;
	}
	/// @todo: actions_count > 1 ?
	uint32_t service_id = ADDR_OF(&result->values)[0];
	return service_id;
}

////////////////////////////////////////////////////////////////////////////////

FILTER_QUERY_DECLARE(
	vs_lookup_ipv6, net6_fast_dst, port_fast_dst, proto_range_fast
);
FILTER_QUERY_DECLARE(vs_acl_ipv6, net6_fast_src, port_fast_src);

static inline uint32_t
vs_v6_table_lookup(struct packet_handler *handler, struct packet *packet) {
	struct value_range *result;
	FILTER_QUERY(
		ADDR_OF(&handler->vs_ipv6.filter),
		vs_lookup_ipv6,
		&packet,
		&result,
		1
	);
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
	struct vs *vs = ADDR_OF(&handler->vs_ipv4.vs) + service_id;

	// set virtual service
	packet_ctx_set_vs(ctx, vs);

	return vs;
}

static inline bool
check_fw_and_inc_stats(
	struct packet_ctx *ctx, struct vs *vs, struct value_range *result
) {
	if (result->count > 0) {
		assert(result->count == 1);
		uint32_t rule_idx = ADDR_OF(&result->values)[0];
		uint64_t counter_id = ADDR_OF(&vs->rule_counters)[rule_idx];
		if (counter_id != (uint64_t)-1) {
			counter_get_address(
				counter_id, ctx->worker_idx, ctx->stats.storage
			)[0] += 1;
		}
		return true;
	}
	return false;
}

static inline bool
vs_v4_fw(struct packet_ctx *ctx, struct vs *vs, struct packet *packet) {
	(void)ctx;
	struct value_range *result;
	FILTER_QUERY(ADDR_OF(&vs->acl), vs_acl_ipv4, &packet, &result, 1);
	return check_fw_and_inc_stats(ctx, vs, result);
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
		       &handler->vs_ipv4.announce,
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
		       &handler->vs_ipv6.announce,
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
	struct vs *vs = ADDR_OF(&handler->vs_ipv6.vs) + service_id;

	// set virtual service
	packet_ctx_set_vs(ctx, vs);

	return vs;
}

static inline bool
vs_v6_fw(struct packet_ctx *ctx, struct vs *vs, struct packet *packet) {
	(void)ctx;
	struct value_range *result;
	FILTER_QUERY(ADDR_OF(&vs->acl), vs_acl_ipv6, &packet, &result, 1);
	return check_fw_and_inc_stats(ctx, vs, result);
}

////////////////////////////////////////////////////////////////////////////////

static inline struct vs *
vs_lookup_and_fw(struct packet_ctx *ctx) {
	struct packet *packet = ctx->packet;
	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct vs *vs = vs_v4_lookup(ctx);
		if (vs == NULL) {
			return NULL;
		}
		if (!vs_v4_fw(ctx, vs, packet)) {
			packet_ctx_vs_stats(ctx)->packet_src_not_allowed += 1;
			return NULL;
		}
		return vs;
	} else { // ipv6
		struct vs *vs = vs_v6_lookup(ctx);
		if (vs == NULL) {
			return NULL;
		}
		if (!vs_v6_fw(ctx, vs, packet)) {
			packet_ctx_vs_stats(ctx)->packet_src_not_allowed += 1;
			return NULL;
		}
		return vs;
	}
}
