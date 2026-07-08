#pragma once

#include <stdint.h>

struct balancer_common_stats {
	uint64_t incoming_packets;
	uint64_t incoming_bytes;
	uint64_t unexpected_network_proto;
	uint64_t unexpected_transport_proto;
	/* Non-initial fragment of a TCP/UDP datagram; has no port header. */
	uint64_t non_initial_l4_fragments;
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

/* Per-virtual-service runtime counters. */
struct balancer_vs_stats {
	uint64_t incoming_packets;
	uint64_t incoming_bytes;
	/* Dropped: source address not in the configured allowlist. */
	uint64_t packet_src_not_allowed;
	/* No eligible real server was available. */
	uint64_t no_reals;
	/* Session slot could not be allocated; the table was full. */
	uint64_t session_table_overflow;
	uint64_t echo_icmp_packets;
	uint64_t error_icmp_packets;
	/* Packets whose assigned real was disabled at lookup time. */
	uint64_t real_is_disabled;
	/* Reserved; not yet incremented. */
	uint64_t real_is_removed;
	/* Non-reschedulable packets (TCP non-SYN) with no valid session. */
	uint64_t not_rescheduled_packets;
	/* ICMP error packets replicated to peer balancers. */
	uint64_t broadcasted_icmp_packets;
	uint64_t created_sessions;
	uint64_t outgoing_packets;
	/* MSS clamping rejected a malformed TCP options field. */
	uint64_t mss_malformed_packet;
	/* MSS clamping failed because prepend headroom was exhausted. */
	uint64_t mss_no_headroom;
	uint64_t outgoing_bytes;
};

/* Per-real-server runtime counters. */
struct balancer_real_stats {
	/* Packets whose session points to this real while it was disabled. */
	uint64_t packets_real_disabled;
	uint64_t error_icmp_packets;
	uint64_t created_sessions;
	uint64_t packets;
	uint64_t bytes;
};
