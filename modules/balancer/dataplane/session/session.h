#pragma once

#include <stdint.h>

#include "common/network.h"

#define BALANCER_SESSION_ID_PADDING                                            \
	(32 - (sizeof(uint64_t) + sizeof(uint16_t) + NET6_LEN))

struct balancer_session_id {
	uint64_t vs_stable_idx;
	uint16_t client_port;
	uint8_t client_ip[NET6_LEN];
	uint8_t padding[BALANCER_SESSION_ID_PADDING];
};

struct balancer_session_state {
	/*
	 * stable_idx of the assigned real; see struct balancer_vs.
	 */
	uint64_t real_stable_idx;
	uint32_t last_packet_timestamp;
	uint32_t create_timestamp;
	uint8_t timeout;
};
