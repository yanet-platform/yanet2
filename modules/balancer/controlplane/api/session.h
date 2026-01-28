#pragma once

#include "common/network.h"
#include "real.h"
#include <stdint.h>

/**
 * Session timeout configuration per transport/state.
 *
 * Time values are expressed in seconds and are used to expire idle sessions
 * depending on the last observed packet type.
 */
struct sessions_timeouts {
	/** Timeout for sessions created/updated by TCP SYN-ACK */
	uint32_t tcp_syn_ack;

	/** Timeout for sessions created/updated by TCP SYN */
	uint32_t tcp_syn;

	/** Timeout for sessions updated by TCP FIN */
	uint32_t tcp_fin;

	/** Default timeout for TCP packets */
	uint32_t tcp;

	/** Default timeout for UDP packets */
	uint32_t udp;

	/** Fallback timeout for other/non-matching packets */
	uint32_t def;
};

/**
 * Unique key that identifies a session between a client and a real.
 *
 * Consists of client address/port and the selected real endpoint.
 */
struct session_identifier {
	/** Client source IP (IPv4/IPv6) */
	struct net_addr client_ip;

	/** Client source port (host byte order) */
	uint16_t client_port;

	/** Selected real endpoint */
	struct real_identifier real;
};

/**
 * Runtime session metadata.
 *
 * All timestamps are monotonic time values.
 */
struct session_info {
	/** Session creation time */
	uint32_t create_timestamp;

	/** Last packet time observed */
	uint32_t last_packet_timestamp;

	/** Current timeout applied to this session */
	uint32_t timeout;
};

/**
 * Session information paired with its identifier.
 *
 * Combines the unique session key (client + real) with runtime metadata
 * (timestamps, timeout). Used when enumerating active sessions.
 */
struct named_session_info {
	/** Unique session identifier (client IP/port + real endpoint) */
	struct session_identifier identifier;

	/** Runtime session metadata (timestamps, timeout) */
	struct session_info info;
};

/**
 * Container for a collection of active sessions.
 *
 * Holds a snapshot of all active sessions tracked by the balancer at a
 * specific point in time. Used by balancer_sessions() to return session
 * enumeration results.
 *
 * MEMORY MANAGEMENT:
 * - The 'sessions' array is heap-allocated by balancer_sessions()
 * - Caller must free with balancer_sessions_free() when done
 * - Safe to call balancer_sessions_free() on partially-initialized structures
 *
 * USAGE PATTERN:
 * ```c
 * struct sessions sessions;
 * balancer_sessions(handle, &sessions, now);
 * for (size_t i = 0; i < sessions.sessions_count; i++) {
 *     // Process sessions.sessions[i]
 * }
 * balancer_sessions_free(&sessions);
 * ```
 */
struct sessions {
	/**
	 * Number of active sessions in the 'sessions' array.
	 *
	 * This is the count of sessions that were active at the time
	 * balancer_sessions() was called. The count may change between
	 * calls as sessions are created and expire.
	 */
	size_t sessions_count;

	/**
	 * Array of active session information.
	 *
	 * Contains detailed information for each active session including:
	 * - Client IP address and port
	 * - Selected real server endpoint
	 * - Creation and last activity timestamps
	 * - Current timeout value
	 *
	 * OWNERSHIP:
	 * - Allocated by balancer_sessions()
	 * - Must be freed with balancer_sessions_free()
	 * - Array length is sessions_count
	 */
	struct named_session_info *sessions;
};