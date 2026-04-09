#pragma once

#include <stddef.h>
#include <stdint.h>

struct balancer_real;

void
balancer_real_sessions(
	struct balancer_real *real,
	size_t workers,
	uint64_t *active_sessions,
	uint32_t *last_packet_timestamp
);