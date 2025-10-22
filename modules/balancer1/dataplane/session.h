#pragma once

#include "common/ttlmap.h"

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

typedef ttlmap_lock_t balancer_session_lock_t;

struct sessions_timeouts {
	uint32_t tcp_syn_ack_timeout;
	uint32_t tcp_syn_timeout;
	uint32_t tcp_fin_timeout;
	uint32_t tcp_timeout;
	uint32_t udp_timeout;
	uint32_t default_timeout;
};