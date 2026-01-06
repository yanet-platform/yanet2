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
	uint32_t tcp_syn_ack; // Timeout for sessions created/updated by TCP
			      // SYN-ACK
	uint32_t tcp_syn; // Timeout for sessions created/updated by TCP SYN
	uint32_t tcp_fin; // Timeout for sessions updated by TCP FIN
	uint32_t tcp;	  // Default timeout for TCP packets
	uint32_t udp;	  // Default timeout for UDP packets
	uint32_t def;	  // Fallback timeout for other/non-matching packets
};

/**
 * Unique key that identifies a session between a client and a real.
 *
 * Consists of client address/port and the selected real endpoint.
 */
struct session_identifier {
	struct net_addr client_ip;   // Client source IP (IPv4/IPv6)
	uint16_t client_port;	     // Client source port (host byte order)
	struct real_identifier real; // Selected real endpoint
};

/**
 * Runtime session metadata.
 *
 * All timestamps are monotonic time values.
 */
struct session_info {
	uint32_t create_timestamp;	// Session creation time
	uint32_t last_packet_timestamp; // Last packet time observed
	uint32_t timeout; // Current timeout applied to this session
};

/**
 * Session information paired with its identifier.
 */
struct named_session_info {
	struct session_identifier identifier;
	struct session_info info;
};

struct sessions {
	size_t sessions_count;
	struct named_session_info *sessions;
};