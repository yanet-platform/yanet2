#include "registry.h"
#include "../api/info.h"
#include "common/interval_counter.h"
#include "common/network.h"
#include <netinet/in.h>
#include <stdio.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

void
service_state_copy(struct service_state *dst, struct service_state *src) {
	dst->last_packet_timestamp = src->last_packet_timestamp;
	interval_counter_copy(&dst->active_sessions, &src->active_sessions);
	memcpy(&dst->stats, &src->stats, sizeof(dst->stats));
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_real_stats_add(
	struct balancer_real_stats *to, struct balancer_real_stats *stats
) {
	to->bytes += stats->bytes;
	to->created_sessions += stats->created_sessions;
	to->disabled += stats->disabled;
	to->ops_packets += stats->ops_packets;
	to->packets += stats->packets;
}

void
service_info_accumulate_into_real_info(
	struct service_info *service_info,
	struct balancer_real_info *real_info,
	size_t workers
) {
	memset(real_info, 0, sizeof(struct balancer_real_info));

	// set virtual ip
	memcpy(real_info->vip,
	       service_info->vip_address,
	       service_info->vip_proto == IPPROTO_IPV6 ? NET6_LEN : NET4_LEN);
	real_info->virtual_ip_proto = service_info->vip_proto;

	// set port
	real_info->virtual_port = service_info->port;

	// set transport proto
	real_info->transport_proto = service_info->transport_proto;

	// set real ip
	memcpy(real_info->ip,
	       service_info->ip_address,
	       service_info->ip_proto == IPPROTO_IPV6 ? NET6_LEN : NET4_LEN);
	real_info->real_ip_proto = service_info->ip_proto;

	// set stats
	for (size_t i = 0; i < workers; ++i) {
		struct service_state *state = &service_info->state[i];
		real_info->active_sessions +=
			interval_counter_current_count(&state->active_sessions);
		if (state->last_packet_timestamp >
		    real_info->last_packet_timestamp) {
			real_info->last_packet_timestamp =
				state->last_packet_timestamp;
		}
		balancer_real_stats_add(&real_info->stats, &state->stats.real);
	}
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_vs_stats_add(
	struct balancer_vs_stats *to, struct balancer_vs_stats *stats
) {
	to->incoming_packets += stats->incoming_packets;
	to->incoming_bytes += stats->incoming_bytes;
	to->packet_src_not_allowed += stats->packet_src_not_allowed;
	to->no_reals += stats->no_reals;
	to->ops_packets += stats->ops_packets;
	to->session_table_overflow += stats->session_table_overflow;
	to->real_is_disabled += stats->real_is_disabled;
	to->packet_not_rescheduled += stats->packet_not_rescheduled;
	to->created_sessions += stats->created_sessions;
	to->outgoing_packets += stats->outgoing_packets;
	to->outgoing_bytes += stats->outgoing_bytes;
}

void
service_info_accumulate_into_vs_info(
	struct service_info *service_info,
	struct balancer_vs_info *vs_info,
	size_t workers
) {
	memset(vs_info, 0, sizeof(struct balancer_vs_info));

	// set ip
	memcpy(vs_info->ip,
	       service_info->vip_address,
	       service_info->vip_proto == IPPROTO_IPV6 ? NET6_LEN : NET4_LEN);
	vs_info->ip_proto = service_info->vip_proto;

	// set port
	vs_info->virtual_port = service_info->port;

	// set proto
	vs_info->transport_proto = service_info->transport_proto;

	// set stats
	for (size_t i = 0; i < workers; ++i) {
		struct service_state *state = &service_info->state[i];
		vs_info->active_sessions +=
			interval_counter_current_count(&state->active_sessions);
		if (state->last_packet_timestamp >
		    vs_info->last_packet_timestamp) {
			vs_info->last_packet_timestamp =
				state->last_packet_timestamp;
		}
		balancer_vs_stats_add(&vs_info->stats, &state->stats.vs);
	}
}