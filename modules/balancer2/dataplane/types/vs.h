#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "common/network.h"

#include "selector.h"

enum balancer_vs_flags {
	balancer_vs_pure_l3 = 1u << 0,
	balancer_vs_fix_mss = 1u << 1,
	balancer_vs_gre = 1u << 2,
	balancer_vs_ops = 1u << 3,
	balancer_vs_wrr = 1u << 4,
};

struct filter;
struct filter_port_range;
struct balancer_real;
struct balancer_real_selector;

enum {
	balancer_vs_acl_max_tag_len = 20,
};

struct balancer_vs_allowed_source {
	struct net *nets;
	uint32_t nets_count;

	struct filter_port_range *port_ranges;
	uint32_t port_ranges_count;

	char tag[balancer_vs_acl_max_tag_len + 1];
};

/*
 * Reals within a VS are stored in a contiguous array with stable
 * positions. Each position has a fixed config index that never
 * changes across config updates:
 *
 *  - When a real is replaced at position N, the new real occupies
 *    the same slot with an incremented epoch in its stable_idx.
 *  - When a real is removed, the slot is marked removed=true
 *    but not compacted.
 *  - When a new real is added, it may reuse a removed slot or
 *    be appended at the end.
 *
 * Sessions in the session table store the real's stable_idx,
 * which encodes (epoch << 32) | config_index. On session reuse,
 * the dataplane extracts the config index for O(1) array access
 * and compares the full stable_idx to detect replacements.
 */
struct balancer_vs {
	struct balancer_real *reals;
	uint32_t reals_count;

	uint64_t id;
	uint64_t counter_id;

	struct balancer_real_selector *selector;
	struct filter *acl;

	uint64_t *rule_counter_ids;

	uint16_t flags;
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
