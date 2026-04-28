#pragma once

#include <stdint.h>

// TODO: docs
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

// TODO: docs
struct balancer_l4_stats {
	uint64_t incoming_packets;
	uint64_t select_vs_failed;
	uint64_t tunnel_failed;
	uint64_t select_real_failed;
	uint64_t outgoing_packets;
};

/**
 * Per-virtual-service runtime counters.
 *
 * Tracks packet processing statistics for a specific virtual service,
 * including successful forwards, various failure conditions, and
 * session management metrics.
 */
struct balancer_vs_stats {
	/* Total packets received matching this virtual service. */
	uint64_t incoming_packets;

	/* Total bytes received matching this virtual service. */
	uint64_t incoming_bytes;

	/* Packets dropped due to source address not in allowlist. */
	uint64_t packet_src_not_allowed;

	/*
	 * Packets that failed real server selection.
	 *
	 * Incremented when:
	 * - No real servers are configured
	 * - All real servers are disabled
	 * - All real servers have zero weight.
	 */
	uint64_t no_reals;

	/* Session creation failures due to table capacity. */
	uint64_t session_table_overflow;

	/* ICMP echo packets processed. */
	uint64_t echo_icmp_packets;

	/*
	 * ICMP error packets forwarded to real servers.
	 *
	 * Tracks ICMP errors (destination unreachable, time exceeded,
	 * etc.) that were matched to sessions and forwarded to the
	 * appropriate real server.
	 */
	uint64_t error_icmp_packets;

	/*
	 * Packets for sessions where the real server is disabled.
	 *
	 * Incremented when:
	 * - Session exists for a specific real
	 * - That real is currently disabled
	 * - Packet arrives for the session
	 *
	 * These packets are dropped.
	 */
	uint64_t real_is_disabled;

	/*
	 * Packets for sessions where the real server was removed.
	 *
	 * Incremented when:
	 * - Session exists for a specific real
	 * - That real is no longer in the configuration
	 * - Packet arrives for the session
	 *
	 * These packets are dropped.
	 */
	uint64_t real_is_removed;

	/*
	 * Packets that couldn't be rescheduled.
	 *
	 * Incremented when:
	 * - No existing session found
	 * - Packet doesn't start a new session (e.g., TCP non-SYN)
	 *
	 * Common for:
	 * - TCP packets without SYN flag when no session exists
	 * - Packets arriving after session timeout
	 */
	uint64_t not_rescheduled_packets;

	/*
	 * ICMP packets broadcasted to peer balancers.
	 *
	 * Incremented when:
	 * - ICMP error has this VS as source
	 * - Packet is cloned and sent to configured peers
	 * - Used for distributed ICMP error handling
	 */
	uint64_t broadcasted_icmp_packets;

	/*
	 * Total sessions created for this virtual service.
	 *
	 * Tracks the cumulative number of sessions created since
	 * the balancer started or statistics were reset. Does not
	 * include OPS packets (which don't create sessions).
	 */
	uint64_t created_sessions;

	/* Packets successfully forwarded to real servers. */
	uint64_t outgoing_packets;

	/* Failed to fix MSS because of malformed TCP options. */
	uint64_t malformed_tcp;

	/* Failed to fix MSS because of rte_mbuf_prepend failure. */
	uint64_t mss_prepend_failed;

	/* Bytes successfully forwarded to real servers (IP layer). */
	uint64_t outgoing_bytes;
};

/*
 * Per-real-server statistics.
 *
 * Tracks packet processing and session creation for a specific real
 * server within a virtual service.
 */
struct balancer_real_stats {
	/*
	 * Packets for sessions assigned to this real when it was disabled.
	 *
	 * Incremented when:
	 * - A session exists for this real
	 * - The real is currently disabled
	 * - A packet arrives for that session
	 *
	 * This indicates packets that were dropped or rescheduled because
	 * the real was disabled after the session was created.
	 */
	uint64_t packets_real_disabled;

	/* ICMP error packets forwarded to this real server. */
	uint64_t error_icmp_packets;

	/* Total number of new sessions created with this real as backend. */
	uint64_t created_sessions;

	/* Total packets forwarded to this real server. */
	uint64_t packets;

	/* Total bytes forwarded to this real server. */
	uint64_t bytes;
};
