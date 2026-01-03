#include "service.h"

#include "api/real.h"
#include "api/vs.h"
#include <netinet/in.h>
#include <stdatomic.h>
#include <string.h>

static void
real_stats_add(struct real_stats *to, struct real_stats *stats) {
	to->bytes += atomic_load_explicit(&stats->bytes, memory_order_relaxed);
	to->created_sessions += atomic_load_explicit(
		&stats->created_sessions, memory_order_relaxed
	);
	to->packets_real_disabled += atomic_load_explicit(
		&stats->packets_real_disabled, memory_order_relaxed
	);
	to->ops_packets +=
		atomic_load_explicit(&stats->ops_packets, memory_order_relaxed);
	to->packets +=
		atomic_load_explicit(&stats->packets, memory_order_relaxed);
}

void
real_info_accum(
	struct real_info *dst, union service_info *src, size_t workers
) {
	memset(dst, 0, sizeof(struct real_info));
	for (size_t i = 0; i < workers; ++i) {
		struct real_info *cur = &src->real.shard[i];
		uint32_t last_packet_timestamp = atomic_load_explicit(
			&cur->last_packet_timestamp, memory_order_relaxed
		);
		if (cur->last_packet_timestamp > last_packet_timestamp) {
			dst->last_packet_timestamp = last_packet_timestamp;
		}
		real_stats_add(&dst->stats, &cur->stats);
	}
}

////////////////////////////////////////////////////////////////////////////////

static void
vs_stats_add(struct vs_stats *dst, struct vs_stats *src) {
	dst->incoming_packets += atomic_load_explicit(
		&src->incoming_packets, memory_order_relaxed
	);
	dst->incoming_bytes += atomic_load_explicit(
		&src->incoming_bytes, memory_order_relaxed
	);

	dst->packet_src_not_allowed += atomic_load_explicit(
		&src->packet_src_not_allowed, memory_order_relaxed
	);
	dst->no_reals +=
		atomic_load_explicit(&src->no_reals, memory_order_relaxed);
	dst->ops_packets +=
		atomic_load_explicit(&src->ops_packets, memory_order_relaxed);
	dst->session_table_overflow += atomic_load_explicit(
		&src->session_table_overflow, memory_order_relaxed
	);
	dst->real_is_disabled += atomic_load_explicit(
		&src->real_is_disabled, memory_order_relaxed
	);
	dst->not_rescheduled_packets += atomic_load_explicit(
		&src->not_rescheduled_packets, memory_order_relaxed
	);
	dst->created_sessions += atomic_load_explicit(
		&src->created_sessions, memory_order_relaxed
	);
	dst->outgoing_packets += atomic_load_explicit(
		&src->outgoing_packets, memory_order_relaxed
	);
	dst->outgoing_bytes += atomic_load_explicit(
		&src->outgoing_bytes, memory_order_relaxed
	);
}

void
vs_info_accum(struct vs_info *dst, union service_info *src, size_t workers) {
	memset(dst, 0, sizeof(struct vs_state));
	for (size_t i = 0; i < workers; ++i) {
		struct vs_info *cur = &src->vs.shard[i];
		uint32_t last_packet_timestamp = atomic_load_explicit(
			&cur->last_packet_timestamp, memory_order_relaxed
		);
		if (last_packet_timestamp > dst->last_packet_timestamp) {
			dst->last_packet_timestamp = last_packet_timestamp;
		}
		vs_stats_add(&dst->stats, &cur->stats);
	}
}
