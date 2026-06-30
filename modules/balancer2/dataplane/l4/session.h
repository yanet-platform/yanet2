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
 * For each packet, the session identity captures the client and
 * virtual-service endpoints (addresses and ports) together with the
 * transport protocol and IP family. Each entry also receives the
 * timeout derived from the transport protocol and a flag indicating
 * whether the packet may trigger session creation or rescheduling.
 *
 * The two arrays are parallel and must each have at least count
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
