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
 *
 * COMPONENTS:
 * - Session timeouts: Control when idle sessions expire
 * - Virtual services: List of load-balanced services
 * - Source addresses: Used for generated packets (ICMP, health checks)
 * - Decapsulation: Optional tunnel unwrapping before processing
 *
 * MEMORY MANAGEMENT:
 * - Caller allocates and manages all arrays (vs, decap_v4, decap_v6)
 * - Arrays must remain valid for the lifetime of the configuration
 * - Use balancer_update_packet_handler() to apply changes
 */
struct packet_handler_config {
	/**
	 * Session timeout configuration.
	 *
	 * Defines how long sessions remain active based on the last
	 * observed packet type (TCP SYN, FIN, UDP, etc.). Different
	 * timeouts allow fine-grained control over session lifecycle.
	 */
	struct sessions_timeouts sessions_timeouts;

	/** Number of virtual services in the 'vs' array */
	size_t vs_count;

	/**
	 * Array of virtual service configurations.
	 *
	 * Each entry defines a load-balanced service including:
	 * - Service identifier (IP, port, protocol)
	 * - List of real servers (backends)
	 * - Scheduling flags (WLC, OPS, Pure L3, etc.)
	 *
	 * Ownership: Caller allocates and manages this array
	 */
	struct named_vs_config *vs;

	/**
	 * IPv4 source address for generated packets.
	 *
	 * Used when the balancer generates packets such as:
	 * - ICMP error responses
	 * - Health check probes (if implemented)
	 * - Other control plane traffic
	 */
	struct net4_addr source_v4;

	/**
	 * IPv6 source address for generated packets.
	 *
	 * Used when the balancer generates IPv6 packets such as:
	 * - ICMPv6 error responses
	 * - Health check probes (if implemented)
	 * - Other control plane traffic
	 */
	struct net6_addr source_v6;

	/** Number of IPv4 decapsulation endpoints in 'decap_v4' array */
	size_t decap_v4_count;

	/**
	 * Array of IPv4 addresses for tunnel decapsulation.
	 *
	 * Packets arriving with these destination addresses will be
	 * decapsulated (tunnel unwrapped) before load balancing.
	 * Useful for GRE, IPIP, or other tunnel protocols.
	 *
	 * Ownership: Caller allocates and manages this array
	 */
	struct net4_addr *decap_v4;

	/** Number of IPv6 decapsulation endpoints in 'decap_v6' array */
	size_t decap_v6_count;

	/**
	 * Array of IPv6 addresses for tunnel decapsulation.
	 *
	 * Packets arriving with these destination addresses will be
	 * decapsulated (tunnel unwrapped) before load balancing.
	 * Useful for GRE, IPIP, or other tunnel protocols.
	 *
	 * Ownership: Caller allocates and manages this array
	 */
	struct net6_addr *decap_v6;
};