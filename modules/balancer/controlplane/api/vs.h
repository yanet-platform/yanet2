#pragma once

#include "common/network.h"

#include <stddef.h>
#include <stdint.h>

// Virtual service flags

/// If virtual service serves all ports.
/// Destination port of the packet will be saved.
#define VS_PURE_L3_FLAG ((uint8_t)(1ull << 0))

/// Fix MSS TCP option.
#define VS_FIX_MSS_FLAG ((uint8_t)(1ull << 1))

/// Use GRE tunneling protocol when transfering packet to real.
#define VS_GRE_FLAG ((uint8_t)(1ull << 2))

/// One Packet Scheduling option disables sessions with the virtual service.
/// Packets with the same source will be scheduled independently.
#define VS_OPS_FLAG ((uint8_t)(1ull << 3))

/**
 * Identifier of a virtual service.
 *
 * Consists of service address, transport protocol and destination port.
 * For L3-only services (VS_PURE_L3_FLAG), port may be zero.
 */
struct vs_identifier {
	struct net_addr addr;	 // Virtual service address (IPv4/IPv6)
	uint8_t ip_proto;	 // IPPROTO_IPV4 or IPPROTO_IPV6
	uint16_t port;		 // Destination port (0 if VS_PURE_L3_FLAG)
	uint8_t transport_proto; // IPPROTO_TCP or IPPROTO_UDP
};

/**
 * Virtual service scheduler algorithm.
 *
 * - source_hash: Stable selection based on client source address/port.
 * - round_robin: Rotates reals for successive flows (stateless per-flow).
 */
enum vs_scheduler {
	source_hash = 0,
	round_robin = 1,
};

struct named_real_config;

/**
 * Static configuration of a virtual service.
 */
struct vs_config {
	uint8_t flags; // Bitmask of VS_* flags

	enum vs_scheduler scheduler; // Algorithm to choose real for new flows

	size_t real_count; // Number of elements in 'reals'
	struct named_real_config
		*reals; // Array of real backends (length: real_count)

	size_t allowed_src_count; // Number of entries in 'allowed_src'
	struct net_addr_range
		*allowed_src; // Client source allowlist (CIDRs), optional

	size_t peers_v4_count; // Number of IPv4 peers in 'peers_v4'
	struct net4_addr
		*peers_v4; // IPv4 peer balancers for ICMP broadcast/responses

	size_t peers_v6_count; // Number of IPv6 peers in 'peers_v6'
	struct net6_addr
		*peers_v6; // IPv6 peer balancers for ICMP broadcast/responses
};

/**
 * Virtual service configuration paired with its identifier.
 */
struct named_vs_config {
	struct vs_identifier identifier; // Virtual service key
	struct vs_config config;	 // Static configuration
};

/**
 * Per-virtual-service runtime counters.
 */
struct vs_stats {
	uint64_t incoming_packets; // Packets received for this VS
	uint64_t incoming_bytes;   // Total bytes received for this VS

	uint64_t packet_src_not_allowed; // Dropped due to disallowed
					 // client source
	uint64_t no_reals; // Failed real selection (all reals disabled)

	uint64_t ops_packets; // OPS: sent to real without creating session
	uint64_t session_table_overflow; // Failed to create session due
					 // to table allocation

	uint64_t echo_icmp_packets;  // ICMP echo packets processed
	uint64_t error_icmp_packets; // ICMP error packets forwarded

	uint64_t real_is_disabled; // Session exists but selected real
				   // is disabled
	uint64_t real_is_removed;  // Session exists but selected real
				   // not in the packet handler config

	uint64_t not_rescheduled_packets; // No established session and
					  // packet does not start one

	uint64_t broadcasted_icmp_packets; // ICMP with VS src
					   // broadcasted to peers

	uint64_t created_sessions; // Sessions created for this VS

	uint64_t outgoing_packets; // Packets successfully sent to selected real
	uint64_t outgoing_bytes;   // Bytes successfully sent to selected real
};

struct named_vs_stats {
	struct vs_identifier identifier;
	struct vs_stats stats;

	size_t reals_count;
	struct named_real_stats *reals;
};

struct named_vs_info {
	struct vs_identifier identifier;

	uint32_t last_packet_timestamp;
	size_t active_sessions;

	size_t reals_count;
	struct named_real_info *reals;
};
