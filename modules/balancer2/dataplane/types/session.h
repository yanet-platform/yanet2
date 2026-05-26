#pragma once

#include "real.h"

#include <stdbool.h>
#include <stdint.h>

struct balancer_session_id {
	uint16_t client_port;
	uint16_t vs_port;
	struct net_addr vip;
	struct net_addr client_ip;
	enum transport_proto transport;
};

struct balancer_session_state {
	uint32_t last_packet_timestamp;
	uint32_t create_timestamp;
	uint32_t timeout;
	struct real_ip real_ip;
};

struct balancer_session_timeouts {
	uint32_t tcp_syn_ack;
	uint32_t tcp_syn;
	uint32_t tcp_fin;
	uint32_t tcp;
	uint32_t udp;
};