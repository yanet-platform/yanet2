#include <string.h>

#include "common/likely.h"
#include "common/memory_address.h"

#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"

#include "filter/query.h"

#include "config.h"
#include "context.h"
#include "group.h"
#include "packet.h"
#include "select.h"
#include "session.h"
#include "tunnel.h"
#include "vs.h"

#include "../session.h"

#include "types/stats.h"

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
	struct virtual_service *vs,
	struct packet **group_pkts,
	size_t group_size,
	struct packet_context *pkt_ctxs,
	bool is_ipv6
) {
	struct balancer_vs_stats *vs_stats = vs_fetch_stats(
		vs, context->worker_idx, context->counter_storage
	);

	uint32_t acl_results[group_size];
	if (is_ipv6) {
		filter_query(
			&vs->acl,
			ipv6_vs_acl,
			group_pkts,
			acl_results,
			group_size
		);
	} else {
		filter_query(
			&vs->acl,
			ipv4_vs_acl,
			group_pkts,
			acl_results,
			group_size
		);
	}

	size_t passed = 0;
	for (size_t i = 0; i < group_size; ++i) {
		uint32_t rule_idx = acl_results[i];
		if (rule_idx == FILTER_RULE_INVALID) {
			vs_stats->packet_src_not_allowed += 1;
			packet_front_drop(context->packet_front, group_pkts[i]);
			continue;
		}

		vs_stats->incoming_packets += 1;
		vs_stats->incoming_bytes += packet_data_len(group_pkts[i]);

		uint64_t *rule_counter = vs_fetch_acl_stats(
			vs,
			context->worker_idx,
			context->counter_storage,
			rule_idx
		);

		/* Dont track stats for rules with empty tag. */
		if (rule_counter != NULL) {
			*rule_counter += 1;
		}

		pkt_ctxs[passed++] = (struct packet_context){
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
service_lookup(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count,
	struct packet_context *pkt_ctxs,
	bool is_ipv6
) {
	/* Batch VS lookup. */
	uint32_t vs_results[packets_count];
	if (is_ipv6) {
		filter_query(
			&context->config->vs_matcher_ip6,
			ipv6_vs_matcher,
			packets,
			vs_results,
			packets_count
		);
	} else {
		filter_query(
			&context->config->vs_matcher_ip4,
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
		uint32_t vs_id = vs_results[i];
		if (vs_id == FILTER_RULE_INVALID) {
			context->l4_stats->select_vs_failed += 1;
			packet_front_drop(context->packet_front, packets[i]);
			continue;
		}

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

	struct virtual_service *virtual_services =
		ADDR_OF(&context->config->vs);

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

static size_t
select_reals(
	struct worker_context *context,
	struct packet_context *pkt_ctxs,
	struct session *sessions,
	size_t packet_count,
	struct balancer_session_table_chain *st_chain,
	uint64_t st_chain_gen
) {
	const size_t prefetch_distance = 4;

	size_t kept = 0;
	for (size_t pkt_idx = 0; pkt_idx < packet_count; ++pkt_idx) {
		if (pkt_idx + prefetch_distance < packet_count) {
			st_chain_prefetch_session(
				st_chain,
				st_chain_gen,
				&sessions[pkt_idx + prefetch_distance].id
			);
		}

		struct real *real = select_real(
			context,
			&pkt_ctxs[pkt_idx],
			&sessions[pkt_idx],
			st_chain,
			st_chain_gen
		);

		if (unlikely(real == NULL)) {
			packet_front_drop(
				context->packet_front, pkt_ctxs[pkt_idx].packet
			);
			continue;
		}

		if (kept != pkt_idx) {
			pkt_ctxs[kept] = pkt_ctxs[pkt_idx];
		}
		pkt_ctxs[kept].selected_real = real;
		pkt_ctxs[kept].selected_real_stats = real_fetch_stats(
			real, context->worker_idx, context->counter_storage
		);
		kept++;
	}

	return kept;
}

static void
tunnel_packets(
	struct worker_context *context,
	struct packet_context *pkt_ctxs,
	size_t packet_count,
	bool is_ipv6
) {
	struct packet_front *packet_front = context->packet_front;
	for (size_t pkt_idx = 0; pkt_idx < packet_count; ++pkt_idx) {
		struct packet_context *pkt_ctx = &pkt_ctxs[pkt_idx];

		int res;
		if (is_ipv6) {
			res = tunnel_ip6_packet(pkt_ctx);
		} else {
			res = tunnel_ip4_packet(pkt_ctx);
		}

		if (unlikely(res != 0)) {
			context->l4_stats->tunnel_failed += 1;
			packet_front_drop(packet_front, pkt_ctx->packet);
			continue;
		}

		packet_front_output(packet_front, pkt_ctx->packet);

		uint64_t pkt_len = packet_data_len(pkt_ctx->packet);

		context->common_stats->outgoing_packets += 1;
		context->common_stats->outgoing_bytes += pkt_len;

		context->l4_stats->outgoing_packets += 1;

		pkt_ctx->matched_vs_stats->outgoing_packets += 1;
		pkt_ctx->matched_vs_stats->outgoing_bytes += pkt_len;

		pkt_ctx->selected_real_stats->packets += 1;
		pkt_ctx->selected_real_stats->bytes += pkt_len;
	}
}

/*
 * L4 packet processing pipeline.
 *
 * 1. match_and_filter:       VS lookup + ACL check (batched).
 *                            Drops packets with no VS or failing ACL.
 *
 * 2. fill_sessions:          parse IP/transport headers into
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

	struct packet_context pkt_ctxs[packets_count];
	struct session sessions[packets_count];

	size_t count = service_lookup(
		context, packets, packets_count, pkt_ctxs, is_ipv6
	);

	fill_sessions(
		sessions,
		pkt_ctxs,
		count,
		&context->config->session_timeouts,
		is_ipv6
	);

	struct balancer_session_table_chain *st_chain =
		ADDR_OF(&context->config->st_chain);

	rcu_t *reals_selector_guard = &context->config->rcu;
	rcu_read_begin(reals_selector_guard, context->worker_idx);
	uint64_t st_chain_gen =
		st_chain_begin_cs(st_chain, context->worker_idx);

	count = select_reals(
		context, pkt_ctxs, sessions, count, st_chain, st_chain_gen
	);

	st_chain_end_cs(st_chain, context->worker_idx);

	rcu_read_end(reals_selector_guard, context->worker_idx);

	tunnel_packets(context, pkt_ctxs, count, is_ipv6);
}

void
balancer_handle_l4_ip4(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count
) {
	const bool is_ipv6 = false;
	balancer_handle_l4_packets(context, packets, packets_count, is_ipv6);
}

void
balancer_handle_l4_ip6(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count
) {
	const bool is_ipv6 = true;
	balancer_handle_l4_packets(context, packets, packets_count, is_ipv6);
}
