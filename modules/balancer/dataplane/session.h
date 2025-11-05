#pragma once

#include "common/ttlmap.h"
#include "meta.h"

#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

struct session_id {
	uint8_t transport_proto;
	uint8_t network_proto;

	uint8_t ip_source[16];
	uint8_t ip_destination[16];

	uint16_t port_source;
	uint16_t port_destination;
};

struct session_state {
	uint32_t real_id; // global id of real
	uint32_t create_timestamp;
	uint32_t last_packet_timestamp;
	uint32_t timeout;
};

typedef ttlmap_lock_t session_lock_t;

struct balancer_sessions_timeouts {
	uint32_t tcp_syn_ack;
	uint32_t tcp_syn;
	uint32_t tcp_fin;
	uint32_t tcp;
	uint32_t udp;
	uint32_t default_timeout;
};

static inline void
fill_session_id(
	struct session_id *id,
	struct packet_metadata *data,
	bool balancer_pure_l3_flag
) {
	id->transport_proto = data->transport_proto;
	id->network_proto = data->network_proto;
	memcpy(id->ip_source, data->src_addr, 16);
	memcpy(id->ip_destination, data->dst_addr, 16);
	if (balancer_pure_l3_flag) {
		id->port_source = 0;
		id->port_destination = 0;
	} else {
		id->port_source = data->src_port;
		id->port_destination = data->dst_port;
	}
}

////////////////////////////////////////////////////////////////////////////////

static inline uint32_t
session_timeout(
	struct balancer_sessions_timeouts *timeouts,
	struct packet_metadata *metadata
) {
	if (metadata->transport_proto == IPPROTO_UDP) {
		return timeouts->udp;
	}
	if (metadata->transport_proto != IPPROTO_TCP) {
		return timeouts->default_timeout;
	}

	if ((metadata->tcp_flags & RTE_TCP_SYN_FLAG) == RTE_TCP_SYN_FLAG) {
		if ((metadata->tcp_flags & RTE_TCP_ACK_FLAG) ==
		    RTE_TCP_ACK_FLAG) {
			return timeouts->tcp_syn_ack;
		}
		return timeouts->tcp_syn;
	}
	if (metadata->tcp_flags & RTE_TCP_FIN_FLAG) {
		return timeouts->tcp_fin;
	}
	return timeouts->tcp;
}