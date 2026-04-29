#pragma once

#include <stdint.h>

struct balancer_session_timeouts {
	uint32_t tcp_syn_ack;
	uint32_t tcp_syn;
	uint32_t tcp_fin;
	uint32_t tcp;
	uint32_t udp;
};