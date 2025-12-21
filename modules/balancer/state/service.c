#include "service.h"

#include "../api/info.h"
#include "index.h"
#include <netinet/in.h>
#include <stdatomic.h>
#include <string.h>

void
balancer_real_stats_add(
	struct balancer_real_stats *to, struct balancer_real_stats *stats
) {
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
		uint32_t last_packet_timestamp = atomic_load_explicit(
			&state->last_packet_timestamp, memory_order_relaxed
		);
		if (state->last_packet_timestamp > last_packet_timestamp) {
			real_info->last_packet_timestamp =
				last_packet_timestamp;
		}
		balancer_real_stats_add(&real_info->stats, &state->stats.real);
	}
}

////////////////////////////////////////////////////////////////////////////////

void
balancer_vs_stats_add(
	struct balancer_vs_stats *to, struct balancer_vs_stats *stats
) {
	to->incoming_packets += atomic_load_explicit(
		&stats->incoming_packets, memory_order_relaxed
	);
	to->incoming_bytes += atomic_load_explicit(
		&stats->incoming_bytes, memory_order_relaxed
	);

	to->packet_src_not_allowed += atomic_load_explicit(
		&stats->packet_src_not_allowed, memory_order_relaxed
	);
	to->no_reals +=
		atomic_load_explicit(&stats->no_reals, memory_order_relaxed);
	to->ops_packets +=
		atomic_load_explicit(&stats->ops_packets, memory_order_relaxed);
	to->session_table_overflow += atomic_load_explicit(
		&stats->session_table_overflow, memory_order_relaxed
	);
	to->real_is_disabled += atomic_load_explicit(
		&stats->real_is_disabled, memory_order_relaxed
	);
	to->not_rescheduled_packets += atomic_load_explicit(
		&stats->not_rescheduled_packets, memory_order_relaxed
	);
	to->created_sessions += atomic_load_explicit(
		&stats->created_sessions, memory_order_relaxed
	);
	to->outgoing_packets += atomic_load_explicit(
		&stats->outgoing_packets, memory_order_relaxed
	);
	to->outgoing_bytes += atomic_load_explicit(
		&stats->outgoing_bytes, memory_order_relaxed
	);
}

void
service_info_accumulate_into_vs_info(
	struct service_info *service_info,
	struct balancer_virtual_service_info *vs_info,
	size_t workers
) {
	memset(vs_info, 0, sizeof(struct balancer_virtual_service_info));

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
		uint32_t last_packet_timestamp = atomic_load_explicit(
			&state->last_packet_timestamp, memory_order_relaxed
		);
		if (last_packet_timestamp > vs_info->last_packet_timestamp) {
			vs_info->last_packet_timestamp = last_packet_timestamp;
		}
		balancer_vs_stats_add(&vs_info->stats, &state->stats.vs);
	}
}

////////////////////////////////////////////////////////////////////////////////

void
service_info_init(
	struct service_info *service,
	uint8_t *vip_address,
	int vip_proto,
	uint8_t *ip_address,
	int ip_proto,
	uint16_t port,
	int transport_proto
) {
	service->vip_proto = vip_proto;
	memcpy(&service->vip_address,
	       vip_address,
	       (vip_proto == IPPROTO_IPV6 ? NET6_LEN : NET4_LEN));
	service->ip_proto = ip_proto;
	service->port = port;
	service->transport_proto = transport_proto;
	memcpy(&service->ip_address,
	       ip_address,
	       (ip_proto == IPPROTO_IPV6 ? NET6_LEN : NET4_LEN));
	for (size_t worker = 0; worker < MAX_WORKERS_NUM; ++worker) {
		struct service_state *service_state = &service->state[worker];
		memset(service_state, 0, sizeof(struct service_state));
	}
}