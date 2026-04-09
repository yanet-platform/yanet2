#pragma once

#include <stdint.h>

struct balancer_common_stats {
	uint64_t incoming_packets;
	uint64_t incoming_bytes;
	uint64_t unexpected_network_proto;
	uint64_t decap_successful;
	uint64_t decap_failed;
	uint64_t outgoing_packets;
	uint64_t outgoing_bytes;
};

struct balancer_l4_stats {
	uint64_t incoming_packets;
	uint64_t select_vs_failed;
	uint64_t invalid_packets;
	uint64_t select_real_failed;
	uint64_t outgoing_packets;
};

struct balancer_icmp_stats {
	uint64_t incoming_packets;
	uint64_t src_not_allowed;
	uint64_t echo_responses;
	uint64_t payload_too_short_ip;
	uint64_t unmatching_src_from_original;
	uint64_t payload_too_short_port;
	uint64_t unexpected_transport;
	uint64_t unrecognized_vs;
	uint64_t forwarded_packets;
	uint64_t broadcasted_packets;
	uint64_t packet_clones_sent;
	uint64_t packet_clones_received;
	uint64_t packet_clone_failures;
};
