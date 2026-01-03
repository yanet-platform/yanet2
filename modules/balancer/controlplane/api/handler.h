#pragma once

#include "common/network.h"

#include "session.h"

#include <stddef.h>

/**
 * Packet handler configuration.
 *
 * Defines runtime parameters for session handling and the set of virtual
 * services available for scheduling, as well as optional decapsulation
 * behavior at the start of the processing pipeline.
 */
struct packet_handler_config {
	struct sessions_timeouts
		sessions_timeouts;  // Per-state/session timeouts
	size_t vs_count;	    // Number of virtual services in 'vs' array
	struct named_vs_config *vs; // Array of VS configs of length vs_count
	struct net4_addr
		source_v4; // IPv4 source used for generated/egress packets
	struct net6_addr
		source_v6;     // IPv6 source used for generated/egress packets
	size_t decap_v4_count; // Number of IPv4 decapsulation endpoints
	struct net4_addr *decap_v4; // IPv4 addresses to decapsulate on input
	size_t decap_v6_count;	    // Number of IPv6 decapsulation endpoints
	struct net6_addr *decap_v6; // IPv6 addresses to decapsulate on input
};