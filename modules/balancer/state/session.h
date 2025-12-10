#pragma once

#include "../api/module.h"
#include "../api/state.h"
#include "../dataplane/meta.h"
#include "common/ttlmap/ttlmap.h"

#include "../dataplane/vs.h"

#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

typedef ttlmap_lock_t session_lock_t;

////////////////////////////////////////////////////////////////////////////////

static inline void
fill_session_id(
	struct balancer_session_id *id,
	struct packet_metadata *data,
	struct virtual_service *vs
) {
	memset(id, 0, sizeof(*id));
	memcpy(id->client_ip, data->src_addr, 16);
	id->client_port = data->src_port;
	id->vs_id = vs->registry_idx;
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