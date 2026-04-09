#pragma once

#include <stdint.h>

struct packet_front;
struct balancer_packet_handler;
struct counter_storage;

struct worker_context {
	struct packet_front *packet_front;
	struct balancer_packet_handler *packet_handler;
	struct counter_storage *counter_storage;
	uint32_t worker_idx;

	struct balancer_common_stats *common_stats;
	struct balancer_l4_stats *l4_stats;
	struct balancer_icmp_stats *icmp_v4_stats;
	struct balancer_icmp_stats *icmp_v6_stats;

	// current time in seconds
	uint32_t now;
};