#pragma once

#include <stdint.h>

#include "common/detail/ttlmap/lock.h"

////////////////////////////////////////////////////////////////////////////////

struct balancer_session_id {
    uint8_t transport_proto;
	uint8_t network_proto;

	uint8_t ip_source[16];
	uint8_t ip_destination[16];

	uint16_t port_source;
	uint16_t port_destination;
};

struct balancer_session_state {
    uint32_t real_id; // global id of real
	uint32_t create_timestamp;
	uint32_t last_packet_timestamp;
	uint32_t timeout;
};

typedef ttlmap_lock_t balancer_session_lock_t;