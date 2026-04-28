#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "types/session.h"

struct session {
	struct balancer_session_id id;
	uint32_t timeout;
	bool can_reschedule;
};

struct packet_context;

/*
 * Parse packet headers into session state for a batch of packets.
 *
 * For each packet in pkt_ctxs, fills the corresponding entry in
 * sessions with the session id (vs_id + client_ip + client_port),
 * the timeout derived from the transport protocol, and the
 * can_reschedule flag that tells select_real whether a new real
 * may be assigned to this flow.
 *
 * pkt_ctxs and sessions are parallel arrays of pkt_ctx_count
 * elements.
 */
void
fill_sessions(
	struct session *sessions,
	struct packet_context *pkt_ctxs,
	size_t count,
	struct balancer_session_timeouts *timeouts,
	bool is_ipv6
);
