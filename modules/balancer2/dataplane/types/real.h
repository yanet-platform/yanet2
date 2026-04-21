#pragma once

#include <stdbool.h>

#include "common/network.h"

struct balancer_sessions_tracker_shard;

enum balancer_real_flags {
	balancer_real_enabled = 1u << 0,
	balancer_real_ipv6 = 1u << 1,
	balancer_real_removed = 1u << 2,
};

/*
 * A real (backend) server within a virtual service.
 *
 * Stored in a per-VS contiguous array indexed by config index.
 * See struct balancer_vs for the array layout and indexing scheme.
 */
struct balancer_real {
	uint64_t counter_id;

	uint64_t id;

	struct balancer_sessions_tracker_shard *tracker_shards;

	/* Destination IP address of the real server (IPv4 or IPv6). */
	struct net_addr addr;

	/*
	 * Source network for the outer tunnel header.
	 *
	 * Contains both the base address (src.v4.addr / src.v6.addr)
	 * and the mask (src.v4.mask / src.v6.mask).
	 *
	 * INVARIANT: the address bytes must be pre-masked by the
	 * controlplane, i.e. (addr[i] & mask[i]) == addr[i] for every
	 * byte i. The tunnel code relies on this to embed client source
	 * IP bits into the unmasked positions without an extra AND:
	 *
	 *   outer_src[i] = addr[i] | (client_src[i] & ~mask[i])
	 *
	 * Use v4 when balancer_real_ipv6 is clear, v6 when set.
	 */
	struct net src;

	uint8_t flags;
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
