#pragma once

#include "types/real.h"
#include "types/session.h"
#include "types/vs.h"

struct l4_packet_context {
	struct packet *packet;
	struct balancer_vs *matched_vs;
	struct balancer_vs_stats *matched_vs_stats;
	struct balancer_real *resolved_real;
	struct balancer_real_stats *resolved_real_stats;
	struct balancer_session_id session_id;
	uint8_t session_timeout;
	bool can_reschedule;
	bool is_dropped;
};
