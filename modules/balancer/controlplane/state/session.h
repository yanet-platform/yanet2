#pragma once

#include "common/network.h"
#include "common/ttlmap/detail/lock.h"

typedef ttlmap_lock_t session_lock_t;

/**
 * Key that identifies a session in the state layer.
 */
struct session_id {
	struct net_addr client_ip; // Client source IP (IPv4/IPv6)
	uint16_t client_port;	   // Client source port (network byte order)
	uint32_t vs_id;		   // Target virtual service
};

/**
 * Stored session metadata in the state layer.
 */
struct session_state {
	uint32_t real_id; // Global real registry ID for this session
	uint32_t timeout; // Current timeout applied (seconds)
	uint32_t last_packet_timestamp; // Last packet timestamp (monotonic)
	uint32_t create_timestamp;	// Creation timestamp (monotonic)
};