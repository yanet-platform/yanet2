#pragma once

#include <stdint.h>

struct balancer_common_stats {
	uint64_t incoming_packets;
	uint64_t incoming_bytes;
	uint64_t unexpected_network_proto;
	uint64_t unexpected_transport_proto;
	uint64_t decap_successful;
	uint64_t decap_failed;
	uint64_t outgoing_packets;
	uint64_t outgoing_bytes;
};

struct balancer_l4_stats {
	uint64_t incoming_packets;
	uint64_t select_vs_failed;
	uint64_t tunnel_failed;
	uint64_t select_real_failed;
	uint64_t outgoing_packets;
};
