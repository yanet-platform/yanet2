#include "registry.h"
#include "../api/info.h"
#include "common/interval_counter.h"
#include "common/network.h"
#include <netinet/in.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

void
service_state_copy(struct service_state *dst, struct service_state *src) {
	dst->last_packet_timestamp = src->last_packet_timestamp;
	interval_counter_copy(
		&dst->active_connections, &src->active_connections
	);
}

////////////////////////////////////////////////////////////////////////////////

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
		real_info->active_connections += interval_counter_current_count(
			&state->active_connections
		);
		if (state->last_packet_timestamp >
		    real_info->last_packet_timestamp) {
			real_info->last_packet_timestamp =
				state->last_packet_timestamp;
		}
		real_info->created_connections += state->created_connections;
		real_info->send_packets += state->out_packets;
		real_info->send_bytes += state->out_bytes;
		real_info->dropped_packets += state->dropped_packets;
	}
}

////////////////////////////////////////////////////////////////////////////////

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
		vs_info->active_connections += interval_counter_current_count(
			&state->active_connections
		);
		if (state->last_packet_timestamp >
		    vs_info->last_packet_timestamp) {
			vs_info->last_packet_timestamp =
				state->last_packet_timestamp;
		}
		vs_info->created_connections += state->created_connections;
		vs_info->in_packets += state->in_packets;
		vs_info->out_packets += state->out_packets;
		vs_info->in_bytes += state->in_bytes;
		vs_info->out_bytes += state->out_bytes;
		vs_info->denied_packets += state->denied_packets;
		vs_info->discarded_packets += state->discarded_packets;
		vs_info->dropped_packets += state->dropped_packets;
	}
}