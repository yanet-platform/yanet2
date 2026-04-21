#pragma once

#include <stdint.h>

struct packet_front;
struct balancer_config;
struct counter_storage;

struct worker_context {
	struct packet_front *packet_front;
	struct balancer_config *config;
	struct counter_storage *counter_storage;

	struct balancer_common_stats *common_stats;
	struct balancer_l4_stats *l4_stats;
	struct balancer_icmp_stats *icmp_v4_stats;
	struct balancer_icmp_stats *icmp_v6_stats;

	uint32_t worker_idx;

	/* Current time in seconds. */
	uint32_t now;
};