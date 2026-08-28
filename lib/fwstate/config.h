#pragma once

#include "common/network.h"

// Max TTL (ns) storable in fw_state_value::last_ttl.
#define FWSTATE_TTL48_MAX ((uint64_t)((1ULL << 48) - 1))

/**
 * FWState configuration structures.
 *
 * The fwstate module owns synchronization end to end: receive matching,
 * local state insertion, suppression, and emission.
 */

struct fwstate_timeouts {
	uint64_t tcp_syn_ack; // default
	uint64_t tcp_syn;     // default
	uint64_t tcp_fin;     // default
	uint64_t tcp;	      // default (120)
	uint64_t udp;	      // 30
	uint64_t default_;    // 16
};

struct fwstate_sync_config {
	// IPv6 source stamped on locally emitted sync frames.
	uint8_t src_addr[16];

	// Ethernet destination shared by multicast and unicast emission.
	struct ether_addr dst_ether;
	// Multicast IPv6 destination used for wire matching and emission.
	uint8_t dst_addr_multicast[16];
	/**
	 * Multicast destination port in network byte order (big-endian).
	 * Stored in BE so it can be used directly in UDP header fields
	 * and compared with udp_hdr->dst_port without conversion.
	 * The controlplane performs the host-to-network conversion when
	 * populating this field.
	 */
	uint16_t port_multicast;
	// Unicast IPv6 destination used only for local emission.
	uint8_t dst_addr_unicast[16];
	/**
	 * Unicast destination port in network byte order (big-endian).
	 * The control plane converts it before publishing the config.
	 */
	uint16_t port_unicast;

	struct fwstate_timeouts timeouts;

	// Sync suppression window in nanoseconds. A sync frame for an already
	// alive entry whose new expiry deadline lands within this window of the
	// entry's current deadline is discarded (the fwmap record is left
	// untouched), debouncing refreshes for frequently touched sessions.
	// Every entry TTL is inflated by this value so the effective keep-alive
	// never falls below the configured timeout. Zero disables suppression.
	uint64_t sync_suppress_timeout;
};
