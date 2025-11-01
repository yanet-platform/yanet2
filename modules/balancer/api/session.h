#pragma once

#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

struct balancer_sessions_timeouts;
struct agent;

/// Initialize timeouts of sessions with different types.
struct balancer_sessions_timeouts *
balancer_sessions_timeouts_create(
	struct agent *agent,
	uint32_t tcp_syn_ack,
	uint32_t tcp_syn,
	uint32_t tcp_fin,
	uint32_t tcp,
	uint32_t udp,
	uint32_t default_timeout
);