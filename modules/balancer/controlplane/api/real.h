#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "common/network.h"
#include "vs.h"

/**
 * Maximum allowed scheduler weight for a real server.
 */
#define MAX_REAL_WEIGHT ((uint16_t)1024)

/**
 * Identifier of a real endpoint within a virtual service.
 *
 * Combines the parent VS identifier with address, transport protocol and port.
 */
struct real_identifier {
	struct vs_identifier vs_identifier; // Parent virtual service identifier
	struct net_addr addr;		    // Real endpoint address (IPv4/IPv6)
	uint8_t ip_proto;		    // IPPROTO_IPV4 or IPPROTO_IPV6
	uint16_t port;			    // Destination port on the real
};

/**
 * Static configuration of a real server.
 *
 * - src: Source network/addresses used when sending to this real.
 * - weight: Relative load distribution weight in range [0..MAX_REAL_WEIGHT].
 */
struct real_config {
	struct net src; // Source network/addresses used to reach this real

	uint16_t weight; // Scheduler weight [0..MAX_REAL_WEIGHT]
};

/**
 * Real configuration paired with its identifier.
 */
struct named_real_config {
	struct real_identifier
		identifier;	   // Real key (VS + addr + proto + port)
	struct real_config config; // Static configuration for the real
};

/**
 * Sentinel value meaning "do not change weight" in real_update.
 */
#define DONT_UPDATE_REAL_WEIGHT ((uint16_t)-1)

/**
 * Sentinel value meaning "do not change enabled flag" in real_update.
 */
#define DONT_UPDATE_REAL_ENABLED ((uint8_t)-1)

/**
 * Partial update for a real server configuration.
 *
 * Use DONT_UPDATE_REAL_WEIGHT or DONT_UPDATE_REAL_ENABLED to skip fields.
 */
struct real_update {
	struct real_identifier identifier; // Real key to update

	uint16_t weight; // New weight (ignored if DONT_UPDATE_REAL_WEIGHT)

	uint8_t enabled; // 0 = disabled, non-zero = enabled (ignored if
			 // DONT_UPDATE_REAL_ENABLED)
};

/**
 * Per-real runtime counters.
 *
 * Counts traffic and control-plane related events for a specific real.
 * Aligned to cacheline as stats are sharded between workers.
 */
struct real_stats {
	_Atomic uint64_t
		packets_real_disabled; // Number of packets that arrived while
				       // the real was disabled

	_Atomic uint64_t
		packets_real_not_present; // Packets for which the real is
					  // absent in current config

	_Atomic uint64_t ops_packets; // One-Packet Scheduling packets sent
				      // without creating a session

	_Atomic uint64_t error_icmp_packets; // ICMP error packets associated
					     // with this real

	_Atomic uint64_t
		created_sessions; // Sessions created with this real as backend

	_Atomic uint64_t packets; // Total packets sent to the real (including
				  // OPS and ICMP)

	_Atomic uint64_t
		bytes; // Total bytes sent to the real (including OPS and ICMP)
} __attribute__((aligned(64)));

/**
 * Real statistics paired with its identifier.
 */
struct named_real_stats {
	struct real_identifier identifier; // Real key
	struct real_stats stats;	   // Stats snapshot for the real
};

/**
 * Runtime information for a real endpoint.
 *
 * Includes last packet timestamp, active session count and per-real stats.
 * Aligned to cacheline as stats are sharded between workers.
 */
struct real_info {
	_Atomic uint32_t last_packet_timestamp; // Last packet time observed
	size_t active_sessions;			// Active sessions to this real
	struct real_stats stats;		// Per-real statistics
} __attribute__((aligned(64)));

/**
 * Real info paired with its identifier and enabled state.
 */
struct named_real_info {
	struct real_identifier identifier; // Real key
	struct real_info info;		   // Runtime info snapshot
	bool enabled; // Whether this real is accepting traffic
};
