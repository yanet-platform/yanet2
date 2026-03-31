#include "common/likely.h"
#include "common/memory_address.h"

#include "lib/dataplane/module/packet_front.h"

#include "filter/query.h"

#include "context.h"
#include "dataplane.h"
#include "packet.h"
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
 * Unified match-and-filter for both IPv4 and IPv6.
 * Since callers pass a compile-time constant for is_ipv6,
 * the compiler will inline this and eliminate the dead branches.
 */
static size_t
match_and_filter(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count,
	struct vs_matched_packet *out,
    bool is_ipv6
) {
    /*
	 * Phase 1: batch VS lookup.
	 * Match all packets against the VS filter in one call
	 * to benefit from code-and-cache locality.
	 */
	struct value_range *results[packets_count];
    if (is_ipv6) {
        FILTER_QUERY(
            ADDR_OF(&context->packet_handler->ipv6_vs_matcher),
            ipv6_vs_matcher,
            packets, results, packets_count
        );
    } else {
        FILTER_QUERY(
            ADDR_OF(&context->packet_handler->ipv4_vs_matcher),
            ipv4_vs_matcher,
            packets, results, packets_count
        );
    }

    /*
	 * Phase 2: per-packet ACL check and stats.
	 * For each VS-matched packet, verify it passes the ACL
	 * and record statistics. Unmatched or filtered packets
	 * are dropped.
	 */
	size_t matched_count = 0;

	for (size_t i = 0; i < packets_count; ++i) {
		struct value_range *result = results[i];
        struct packet *packet = packets[i];

        /* No VS matched -- drop. */
		if (unlikely(result->count == 0)) {
			packet_front_drop(context->packet_front, packet);
			continue;
		}

		uint32_t vs_id = ADDR_OF(&result->values)[0];

		struct balancer_vs *vs =
			packet_handler_get_vs(context->packet_handler, vs_id);
		struct balancer_vs_stats *vs_stats = vs_get_stats(
			vs, context->worker_idx, context->counter_storage
		);

		vs_stats->incoming_packets += 1;
		vs_stats->incoming_bytes += packet->mbuf->pkt_len;

        /* ACL check -- drop if source not allowed. */
        if (is_ipv6) {
            FILTER_QUERY(
				ADDR_OF(&vs->acl), ipv6_vs_acl,
				&packet, &result, 1
			);
		} else {
			FILTER_QUERY(
				ADDR_OF(&vs->acl), ipv4_vs_acl,
				&packet, &result, 1
			);
		}

		if (unlikely(result->count == 0)) {
			packet_front_drop(context->packet_front, packet);
			continue;
		}

        uint32_t rule_idx = ADDR_OF(&result->values)[0];
		uint64_t *rule_counter = vs_get_acl_stats(
			vs, context->worker_idx,
			context->counter_storage,
			rule_idx
		);

		*rule_counter += 1;

		out[matched_count++] = (struct vs_matched_packet){
			.packet = packet,
			.matched_vs = vs,
			.matched_vs_stats = vs_stats,
		};
	}

	return matched_count;
}

void
balancer_handle_l4_ipv4(
	struct worker_context *context,
	struct packet **packets,
	size_t packets_count
) {
	struct vs_matched_packet matched[packets_count];
    const bool is_ipv6 = false;
	size_t matched_count =
		match_and_filter(context, packets, packets_count, matched, is_ipv6);
}

void
balancer_handle_l4_ipv6(
    struct worker_context *context,
    struct packet **packets,
    size_t packets_count
) {
    struct vs_matched_packet matched[packets_count];
    const bool is_ipv6 = true;
    size_t matched_count =
        match_and_filter(context, packets, packets_count, matched, is_ipv6);
}