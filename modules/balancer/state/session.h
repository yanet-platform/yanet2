#pragma once

#include "../api/module.h"
#include "../api/state.h"
#include "../dataplane/meta.h"
#include "common/ttlmap/ttlmap.h"

#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

typedef ttlmap_lock_t session_lock_t;

////////////////////////////////////////////////////////////////////////////////

static inline void
fill_session_id(
	struct balancer_session_id *id,
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
		return timeouts->def;
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