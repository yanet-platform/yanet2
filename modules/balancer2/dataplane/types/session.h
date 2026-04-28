#pragma once

#include "common/network.h"

#include <stdint.h>

enum balancer_session_flags {
	balancer_session_tcp = 1 << 0,
	balancer_session_ip6 = 1 << 1,
};

struct balancer_session_id {
	uint16_t client_port;
	uint16_t vs_port;
	uint8_t vip[NET6_LEN];
	uint8_t client_ip[NET6_LEN];
	uint8_t flags;
};

struct balancer_session_state {
	uint32_t last_packet_timestamp;
	uint32_t create_timestamp;
	uint32_t timeout;
	uint8_t real_ip[NET6_LEN];
	uint8_t flags;
};

struct balancer_session_timeouts {
	uint32_t tcp_syn_ack;
	uint32_t tcp_syn;
	uint32_t tcp_fin;
	uint32_t tcp;
	uint32_t udp;
};