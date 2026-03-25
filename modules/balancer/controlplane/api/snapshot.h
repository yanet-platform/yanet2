#pragma once

#include "balancer.h"
#include "stats.h"
#include <stdbool.h>

struct named_vs_snapshot;

struct balancer_snapshot {
	struct balancer_common_stats common_stats;
	struct balancer_icmp_stats icmp_ipv4_stats;
	struct balancer_icmp_stats icmp_ipv6_stats;
	struct balancer_l4_stats l4_stats;
	size_t active_sessions;
	uint32_t last_packet_timestamp;
	size_t vs_count;
	struct named_vs_snapshot *vs_snapshots;
};

struct balancer_snapshot_params {
	bool include_active_sessions;
	bool include_vs_acl;
	struct packet_handler_ref *packet_handler_ref;
};