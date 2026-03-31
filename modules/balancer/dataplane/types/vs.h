#pragma once

#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>

#include "selector.h"

enum balancer_vs_flags {
	balancer_vs_pure_l3 = 1u << 0,
	balancer_vs_fix_mss = 1u << 1,
	balancer_vs_gre = 1u << 2,
	balancer_vs_ops = 1u << 3,
	balancer_vs_wlc = 1u << 4,
};

struct filter;

struct balancer_vs {
	uint64_t counter_id;
	struct balancer_real_selector selector;
	struct filter *acl;

	/*
	 * Controlplane limits the number of rules per VS to billion,
	 * so we can allocate a single array for all rules
	 * in the shared memory without fragmentation issues.
	 */
	uint64_t *rule_counters;

	size_t reals_count;
	size_t first_real_idx;
	uint8_t flags;

	bool acl_reused;
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

	/* Bytes successfully forwarded to real servers (IP layer). */
	uint64_t outgoing_bytes;
};
