#include <string.h>

#include "common/likely.h"
#include "common/memory_address.h"
#include "common/network.h"
#include "lib/dataplane/module/packet_front.h"

#include "filter/query.h"

#include "context.h"
#include "dataplane.h"
#include "group.h"
#include "packet.h"
#include "real_helpers.h"
#include "resolve.h"
#include "session/table.h"
#include "tunnel.h"
#include "types/session.h"
#include "types/stats.h"
#include "vs_helpers.h"

FILTER_QUERY_DECLARE(
	ipv4_vs_matcher, net4_fast_dst, port_fast_dst, proto_range_fast
);
FILTER_QUERY_DECLARE(ipv4_vs_acl, net4_fast_src, port_fast_src);

FILTER_QUERY_DECLARE(
	ipv6_vs_matcher, net6_fast_dst, port_fast_dst, proto_range_fast
);
FILTER_QUERY_DECLARE(ipv6_vs_acl, net6_fast_src, port_fast_src);

/*
 * Batch ACL query for a group of packets sharing the same VS.
 * Updates incoming and ACL stats, drops packets that fail.
 * Appends passing packets to pkt_ctxs.
 * Returns the number of packets that passed.
 */
static size_t
filter_vs_group(
	struct worker_context *context,
	struct balancer_vs *vs,
	struct packet **group_pkts,
	size_t group_size,
	struct l4_packet_context *pkt_ctxs,
	bool is_ipv6
) {
	struct balancer_vs_stats *vs_stats =
		vs_get_stats(vs, context->worker_idx, context->counter_storage);

	struct value_range *acl_results[group_size];
	if (is_ipv6) {
		filter_query(
			ADDR_OF(&vs->acl),
			ipv6_vs_acl,
			group_pkts,
			acl_results,
			group_size
		);
	} else {
		filter_query(
			ADDR_OF(&vs->acl),
			ipv4_vs_acl,
			group_pkts,
			acl_results,
			group_size
		);
	}

	size_t passed = 0;
	for (size_t i = 0; i < group_size; ++i) {
		if (unlikely(acl_results[i]->count == 0)) {
			vs_stats->packet_src_not_allowed += 1;
			packet_front_drop(context->packet_front, group_pkts[i]);
			continue;
		}

		vs_stats->incoming_packets += 1;
		vs_stats->incoming_bytes += group_pkts[i]->mbuf->pkt_len;

		uint32_t rule_idx = ADDR_OF(&acl_results[i]->values)[0];
		uint64_t *rule_counter = vs_get_acl_stats(
			vs,
			context->worker_idx,
			context->counter_storage,
			rule_idx
		);

		/* Dont track stats for rules with empty tag. */
		if (rule_counter != NULL) {
			*rule_counter += 1;
		}

		pkt_ctxs[passed++] = (struct l4_packet_context){
			.packet = group_pkts[i],
			.matched_vs = vs,
			.matched_vs_stats = vs_stats,
		};
	}

	return passed;
}

/*
 * Match packets to virtual services, then filter by ACL.
 *
 * 1. Batch VS lookup — classify all packets at once.
 * 2. Group matched packets by VS.
 * 3. Batch ACL per group — drop packets that fail.
 *
 * Returns the number of packets that passed both checks.
 * is_ipv6 is a compile-time constant so the compiler
 * eliminates the dead branches after inlining.
 */
static size_t
match_and_filter(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count,
	struct l4_packet_context *pkt_ctxs,
	bool is_ipv6
) {
	/* Batch VS lookup. */
	struct value_range *vs_results[packets_count];
	if (is_ipv6) {
		filter_query(
			ADDR_OF(&context->packet_handler->ipv6_vs_matcher),
			ipv6_vs_matcher,
			packets,
			vs_results,
			packets_count
		);
	} else {
		filter_query(
			ADDR_OF(&context->packet_handler->ipv4_vs_matcher),
			ipv4_vs_matcher,
			packets,
			vs_results,
			packets_count
		);
	}

	/* Drop unmatched, record indices and vs_ids of the rest. */
	uint8_t order[packets_count];
	uint32_t vs_ids[packets_count];
	size_t matched_count = 0;

	for (size_t i = 0; i < packets_count; ++i) {
		if (unlikely(vs_results[i]->count == 0)) {
			context->l4_stats->select_vs_failed += 1;
			packet_front_drop(context->packet_front, packets[i]);
			continue;
		}

		uint32_t vs_id = ADDR_OF(&vs_results[i]->values)[0];

		order[matched_count] = i;
		vs_ids[matched_count] = vs_id;
		matched_count++;
	}

	if (unlikely(matched_count == 0)) {
		return 0;
	}

	/*
	 * Group by VS for batched ACL queries. If grouping fails
	 * (too many distinct VSes), the loop below still works
	 * correctly with ungrouped packets — just smaller batches.
	 */
	group_by_id(vs_ids, order, matched_count);

	struct balancer_vs *virtual_services =
		ADDR_OF(&context->packet_handler->vs);

	/* Run ACL per VS group. */
	size_t total_passed = 0;
	size_t pkt_idx = 0;
	struct packet *group_pkts[matched_count];
	while (pkt_idx < matched_count) {
		uint32_t cur_vs_id = vs_ids[pkt_idx];

		/* Collect contiguous packets for this VS. */
		size_t group_size = 0;
		while (pkt_idx < matched_count && vs_ids[pkt_idx] == cur_vs_id
		) {
			group_pkts[group_size++] = packets[order[pkt_idx]];
			pkt_idx++;
		}

		total_passed += filter_vs_group(
			context,
			virtual_services + cur_vs_id,
			group_pkts,
			group_size,
			pkt_ctxs + total_passed,
			is_ipv6
		);
	}

	return total_passed;
}

static void
extract_ipv4_metadata(struct l4_packet_context *pkt_ctx) {
	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		pkt_ctx->packet->mbuf,
		struct rte_ipv4_hdr *,
		pkt_ctx->packet->network_header.offset
	);
	__builtin_memcpy(
		pkt_ctx->session_id.client_ip,
		(uint8_t *)&ipv4_hdr->src_addr,
		NET4_LEN
	);
}

static void
extract_ipv6_metadata(struct l4_packet_context *pkt_ctx) {
	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		pkt_ctx->packet->mbuf,
		struct rte_ipv6_hdr *,
		pkt_ctx->packet->network_header.offset
	);
	memcpy(pkt_ctx->session_id.client_ip, ipv6_hdr->src_addr, NET6_LEN);
}

static void
extract_network_metadata(struct l4_packet_context *pkt_ctx, bool is_ipv6) {
	if (is_ipv6) {
		extract_ipv6_metadata(pkt_ctx);
	} else {
		extract_ipv4_metadata(pkt_ctx);
	}
}

static uint8_t
tcp_session_timeout(
	struct balancer_session_timeouts *timeouts, uint16_t tcp_flags
) {
	if ((tcp_flags & RTE_TCP_SYN_FLAG) == RTE_TCP_SYN_FLAG) {
		if ((tcp_flags & RTE_TCP_ACK_FLAG) == RTE_TCP_ACK_FLAG) {
			return timeouts->tcp_syn_ack;
		}
		return timeouts->tcp_syn;
	}
	if (tcp_flags & RTE_TCP_FIN_FLAG) {
		return timeouts->tcp_fin;
	}
	return timeouts->tcp;
}

static void
extract_tcp_metadata(
	struct l4_packet_context *pkt_ctx,
	struct balancer_session_timeouts *timeouts
) {
	struct rte_tcp_hdr *hdr = rte_pktmbuf_mtod_offset(
		pkt_ctx->packet->mbuf,
		struct rte_tcp_hdr *,
		pkt_ctx->packet->transport_header.offset
	);

	uint8_t flags = hdr->tcp_flags;

	pkt_ctx->session_id.client_port = hdr->src_port;
	pkt_ctx->session_timeout = tcp_session_timeout(timeouts, flags);
	pkt_ctx->can_reschedule = (flags & (RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG)
				  ) == RTE_TCP_SYN_FLAG;
}

static void
extract_udp_metadata(
	struct l4_packet_context *pkt_ctx,
	struct balancer_session_timeouts *timeouts
) {
	struct rte_udp_hdr *hdr = rte_pktmbuf_mtod_offset(
		pkt_ctx->packet->mbuf,
		struct rte_udp_hdr *,
		pkt_ctx->packet->transport_header.offset
	);

	pkt_ctx->session_id.client_port = hdr->src_port;
	pkt_ctx->session_timeout = timeouts->udp;
	pkt_ctx->can_reschedule = true;
}

static void
extract_transport_metadata(
	struct l4_packet_context *pkt_ctx,
	struct balancer_session_timeouts *timeouts
) {
	if (pkt_ctx->packet->transport_header.type == IPPROTO_TCP) {
		extract_tcp_metadata(pkt_ctx, timeouts);
	} else {
		extract_udp_metadata(pkt_ctx, timeouts);
	}
}

static void
extract_session_metadata(
	struct l4_packet_context *pkt_ctxs,
	size_t pkt_ctx_count,
	struct balancer_session_timeouts *timeouts,
	bool is_ipv6
) {
	const size_t prefetch_distance = 2;

	for (size_t pkt_idx = 0; pkt_idx < pkt_ctx_count; ++pkt_idx) {
		if (pkt_idx + prefetch_distance < pkt_ctx_count) {
			rte_prefetch0(rte_pktmbuf_mtod(pkt_ctxs[pkt_idx + prefetch_distance].packet->mbuf, void *));
		}

		struct l4_packet_context *pkt_ctx = &pkt_ctxs[pkt_idx];
		pkt_ctx->session_id.vs_stable_idx =
			pkt_ctx->matched_vs->stable_idx;

		/*
		 * Zero client_ip + padding so the session ID hashes
		 * deterministically. extract_network_metadata overwrites
		 * the relevant prefix (4 bytes for IPv4, 16 for IPv6).
		 */
		__builtin_memset(
			pkt_ctx->session_id.client_ip + NET4_LEN,
			0,
			NET6_LEN - NET4_LEN + BALANCER_SESSION_ID_PADDING
		);

		extract_network_metadata(pkt_ctx, is_ipv6);
		extract_transport_metadata(pkt_ctx, timeouts);
	}
}

static void
select_reals(
	struct worker_context *context,
	struct l4_packet_context *pkt_ctxs,
	size_t pkt_ctx_count,
	struct balancer_session_table *session_table,
	uint64_t current_table_gen
) {
	const size_t prefetch_distance = 4;

	for (size_t pkt_idx = 0; pkt_idx < pkt_ctx_count; ++pkt_idx) {
		if (pkt_idx + prefetch_distance < pkt_ctx_count) {
			st_prefetch_session(
				session_table,
				current_table_gen,
				&pkt_ctxs[pkt_idx + prefetch_distance]
					 .session_id
			);
		}

		struct l4_packet_context *pkt_ctx = &pkt_ctxs[pkt_idx];

		struct balancer_real *real = resolve_real(
			context, pkt_ctx, session_table, current_table_gen
		);

		if (unlikely(real == NULL)) {
			pkt_ctx->is_dropped = true;
			packet_front_drop(
				context->packet_front, pkt_ctx->packet
			);
			continue;
		}

		pkt_ctx->resolved_real = real;
		pkt_ctx->resolved_real_stats = real_get_stats(
			real, context->worker_idx, context->counter_storage
		);
	}
}

static void
tunnel_packets(
	struct worker_context *context,
	struct l4_packet_context *pkt_ctxs,
	size_t pkt_ctx_count,
	bool is_ipv6
) {
	struct packet_front *packet_front = context->packet_front;
	for (size_t pkt_idx = 0; pkt_idx < pkt_ctx_count; ++pkt_idx) {
		struct l4_packet_context *pkt_ctx = &pkt_ctxs[pkt_idx];
		if (unlikely(pkt_ctx->is_dropped)) {
			continue;
		}

		if (is_ipv6) {
			tunnel_ipv6_packet(pkt_ctx);
		} else {
			tunnel_ipv4_packet(pkt_ctx);
		}

		packet_front_output(packet_front, pkt_ctx->packet);

		context->l4_stats->outgoing_packets += 1;
		context->common_stats->outgoing_packets += 1;
		context->common_stats->outgoing_bytes +=
			pkt_ctx->packet->mbuf->pkt_len;
	}
}

/*
 * L4 packet processing pipeline.
 *
 * 1. match_and_filter:       VS lookup + ACL check (batched).
 *                            Drops packets with no VS or failing ACL.
 *
 * 2. extract_session_metadata: parse IP/transport headers into
 *                            session_id, timeout, and reschedule flag.
 *
 * 3. select_reals:           look up or create a session, pick a
 *                            real server. Runs inside two critical
 *                            sections:
 *   - reals_selector_guard:  RCU guard for real selector rings,
 *                            held across both select and tunnel
 *                            so the ring isn't freed mid-use.
 *   - session table CS:      pins the current session map generation,
 *                            released after all lookups are done.
 *
 * 4. tunnel_packets:         encapsulate and forward to the chosen real.
 */
static void
balancer_handle_l4_packets(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count,
	bool is_ipv6
) {
	context->l4_stats->incoming_packets += packets_count;

	/*
	 * Not zero-initialized: filter_vs_group populates each used
	 * entry via compound literal, which zeros all unset fields
	 * (including is_dropped = false).
	 */
	struct l4_packet_context pkt_ctxs[packets_count];

	size_t pkt_ctx_count = match_and_filter(
		context, packets, packets_count, pkt_ctxs, is_ipv6
	);

	extract_session_metadata(
		pkt_ctxs,
		pkt_ctx_count,
		&context->packet_handler->session_timeouts,
		is_ipv6
	);

	struct balancer_session_table *session_table =
		ADDR_OF(&context->packet_handler->session_table);

	rcu_t *reals_selector_guard = &context->packet_handler->rcu;
	rcu_read_begin(reals_selector_guard, context->worker_idx);
	uint64_t current_table_gen =
		st_begin_cs(session_table, context->worker_idx);

	select_reals(
		context,
		pkt_ctxs,
		pkt_ctx_count,
		session_table,
		current_table_gen
	);

	st_end_cs(session_table, context->worker_idx);
	rcu_read_end(reals_selector_guard, context->worker_idx);

	tunnel_packets(context, pkt_ctxs, pkt_ctx_count, is_ipv6);
}

void
balancer_handle_l4_ipv4(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count
) {
	const bool is_ipv6 = false;
	balancer_handle_l4_packets(context, packets, packets_count, is_ipv6);
}

void
balancer_handle_l4_ipv6(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count
) {
	const bool is_ipv6 = true;
	balancer_handle_l4_packets(context, packets, packets_count, is_ipv6);
}
