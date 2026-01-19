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

struct relative_real_identifier {
	struct net_addr addr; // Real endpoint address (IPv4/IPv6)
	uint8_t ip_proto;     // IPPROTO_IPV4 or IPPROTO_IPV6
	uint16_t port;	      // Destination port on the real
};

/**
 * Identifier of a real endpoint within a virtual service.
 *
 * Combines the parent VS identifier with address, transport protocol and port.
 */
struct real_identifier {
	struct vs_identifier vs_identifier; // Parent virtual service identifier
	struct relative_real_identifier relative;
};

/**
 * Static configuration of a real server.
 *
 * - src: Source network/addresses used when sending to this real.
 * - weight: Relative load distribution weight in range [0..MAX_REAL_WEIGHT].
 */
struct real_config {
	struct net src;

	uint16_t weight; // Scheduler weight [0..MAX_REAL_WEIGHT]
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

	uint8_t enabled; // 0 = disabled, non-zero = enabled
			 // (ignored if DONT_UPDATE_REAL_ENABLED)
};

struct real_stats {
	// Number of packets that arrived while the real was disabled
	uint64_t packets_real_disabled;

	// One-Packet Scheduling packets sent without creating a session
	uint64_t ops_packets;

	// ICMP error packets associated with this real
	uint64_t error_icmp_packets;

	// Sessions created with this real as backend
	uint64_t created_sessions;

	// Total packets sent to the real (including OPS and ICMP)
	uint64_t packets;

	// Total bytes sent to the real (including OPS and ICMP)
	uint64_t bytes;
};

// Stats of the real relative
// to the virtual service
struct named_real_stats {
	struct relative_real_identifier real;
	struct real_stats stats;
};

struct named_real_info {
	struct relative_real_identifier real;
	uint32_t last_packet_timestamp; // Last packet time observed
	size_t active_sessions;		// Active sessions to this real
};

// Config of the real relative
// to the virtual service
struct named_real_config {
	struct relative_real_identifier real;
	struct real_config config;
};

struct real_ph_index {
	size_t vs_idx;
	size_t real_idx;
};