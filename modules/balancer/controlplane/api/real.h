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
 * Real server identifier within a virtual service context.
 *
 * Identifies a specific real server by its IP address and port, relative
 * to its parent virtual service. This is the "relative" identifier because
 * it doesn't include the VS information.
 *
 * PORT SEMANTICS:
 * - Currently RESERVED FOR FUTURE USE
 * - The actual destination port is determined by:
 *   * Standard mode (pure_l3=false): Uses the virtual service port
 *   * Pure L3 mode (pure_l3=true): Uses the client's original destination port
 * - This field is reserved for future functionality where real servers
 *   might listen on different ports than the virtual service
 */
struct relative_real_identifier {
	/** Real server IP address (IPv4 or IPv6) */
	struct net_addr addr;

	/**
	 * IP protocol version indicator.
	 *
	 * Values:
	 * - 0: IPPROTO_IP (IPv4)
	 * - 41: IPPROTO_IPV6 (IPv6)
	 *
	 * This is derived from the address type and used internally
	 * for protocol-specific processing.
	 */
	uint8_t ip_proto;

	/**
	 * Destination port on the real server.
	 *
	 * CURRENT STATUS: RESERVED FOR FUTURE USE
	 *
	 * The actual port used when forwarding to the real is currently
	 * determined by the virtual service configuration:
	 * - Standard mode: VS port is used
	 * - Pure L3 mode: Client's original destination port is preserved
	 *
	 * FUTURE USE:
	 * This field is reserved for port translation functionality where
	 * real servers could listen on different ports than the VS.
	 */
	uint16_t port;
};

/**
 * Identifier of a real endpoint within a virtual service.
 *
 * Combines the parent VS identifier with address, transport protocol and port.
 */
struct real_identifier {
	/** Parent virtual service identifier */
	struct vs_identifier vs_identifier;

	/** Identifier of real relative to its virtual service */
	struct relative_real_identifier relative;
};

/**
 * Static configuration of a real server.
 */
struct real_config {
	/** Source network/addresses used when sending to this real. */
	struct net src;

	/** Scheduler weight [0..MAX_REAL_WEIGHT] */
	uint16_t weight;
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
	/** Real key to update */
	struct real_identifier identifier;

	/** New weight (ignored if DONT_UPDATE_REAL_WEIGHT) */
	uint16_t weight;

	/** (ignored if DONT_UPDATE_REAL_ENABLED) */
	/** 0 = disabled, non-zero = enabled */
	uint8_t enabled;
};

/**
 * Per-real-server statistics.
 *
 * Tracks packet processing and session creation for a specific real
 * server within a virtual service.
 */
struct real_stats {
	/**
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

	/**
	 * One-Packet Scheduling packets sent without creating a session.
	 *
	 * Incremented when VS_OPS_FLAG is set and packets are forwarded
	 * to this real without session tracking.
	 */
	uint64_t ops_packets;

	/**
	 * ICMP error packets forwarded to this real server.
	 *
	 * Includes ICMP errors related to sessions assigned to this real,
	 * such as destination unreachable or time exceeded messages.
	 */
	uint64_t error_icmp_packets;

	/**
	 * Total number of new sessions created with this real as backend.
	 *
	 * Incremented each time a new session is created and this real
	 * is selected by the scheduler. Does not include OPS packets.
	 */
	uint64_t created_sessions;

	/**
	 * Total packets forwarded to this real server.
	 *
	 * Includes:
	 * - Regular session packets
	 * - OPS packets (if VS_OPS_FLAG is set)
	 * - ICMP error packets
	 */
	uint64_t packets;

	/**
	 * Total bytes forwarded to this real server.
	 *
	 * Includes all packet types (regular, OPS, ICMP).
	 * Measured at the IP layer (includes IP header and payload).
	 */
	uint64_t bytes;
};

/**
 * Real server statistics with identifier.
 *
 * Associates statistics with a specific real server within a virtual
 * service context.
 */
struct named_real_stats {
	/** Real server identifier (relative to its VS) */
	struct relative_real_identifier real;

	/** Statistics for this real server */
	struct real_stats stats;
};

/**
 * Real server runtime information.
 *
 * Provides runtime information about a specific real server including
 * active session count and last activity timestamp.
 */
struct named_real_info {
	/** Real server identifier (relative to its VS) */
	struct relative_real_identifier real;

	/**
	 * Timestamp of the last packet processed for this real server.
	 *
	 * Monotonic timestamp of when any packet
	 * was forwarded to this real.
	 *
	 * Updated in real-time by the dataplane when:
	 * - Packets are forwarded to the real
	 * - ICMP errors are forwarded to the real.
	 */
	uint32_t last_packet_timestamp;

	/**
	 * Number of active sessions currently assigned to this real server.
	 *
	 * This count represents sessions tracked by the balancer where
	 * this real was selected as the backend. Does not include
	 * OPS packets (no session tracking).
	 */
	size_t active_sessions;
};

/**
 * Real server configuration with identifier.
 *
 * Associates configuration with a specific real server within a virtual
 * service context.
 */
struct named_real_config {
	/** Real server identifier (relative to its VS) */
	struct relative_real_identifier real;

	/** Configuration for this real server */
	struct real_config config;
};

/**
 * Packet handler internal indices for a real server.
 *
 * Maps a real server to its position in the packet handler's internal
 * data structures. Used for low-level operations that need direct access
 * to packet handler arrays.
 *
 * USAGE:
 * Primarily used internally by the manager layer to coordinate between
 * high-level balancer API and low-level packet handler implementation.
 */
struct real_ph_index {
	/**
	 * Index of the virtual service in the packet handler's VS array.
	 *
	 * This is the position of the parent VS in the
	 * packet_handler_config.vs array.
	 */
	size_t vs_idx;

	/**
	 * Index of the real within the virtual service's real array.
	 *
	 * This is the position of the real in the vs_config.reals array
	 * for the parent virtual service.
	 */
	size_t real_idx;
};