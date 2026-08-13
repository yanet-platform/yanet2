#pragma once

#include "common/network.h"

// Max TTL (ns) storable in fw_state_value::last_ttl.
#define FWSTATE_TTL48_MAX ((uint64_t)((1ULL << 48) - 1))

/**
 * FWState configuration structures.
 *
 * The sync parameters split by consumer: the fwstate module matches
 * incoming sync packets, inserts state, and rewrites forwarded internal
 * frames, so its config carries the receive-side fields. A module that
 * synthesizes sync packets (ACL CREATE_STATE) needs only the emission
 * addressing.
 */

struct fwstate_timeouts {
	uint64_t tcp_syn_ack; // default
	uint64_t tcp_syn;     // default
	uint64_t tcp_fin;     // default
	uint64_t tcp;	      // default (120)
	uint64_t udp;	      // 30
	uint64_t default_;    // 16
};

// Receive-side sync parameters for the fwstate module: packet matching,
// state insertion, and the internal-frame rewrite.
struct fwstate_sync_config {
	// Source IPv6 address stamped on forwarded internal sync frames.
	uint8_t src_addr[16];

	// Multicast destination the module matches incoming sync packets
	// against.
	uint8_t dst_addr_multicast[16];
	/**
	 * Multicast destination port in network byte order (big-endian).
	 * Stored in BE so it can be used directly in UDP header fields
	 * and compared with udp_hdr->dst_port without conversion.
	 * The controlplane performs the host-to-network conversion when
	 * populating this field.
	 */
	uint16_t port_multicast;

	struct fwstate_timeouts timeouts;

	// Sync suppression window in nanoseconds. A sync frame for an already
	// alive entry whose new expiry deadline lands within this window of the
	// entry's current deadline is discarded (the fwmap record is left
	// untouched), debouncing refreshes for frequently touched sessions.
	// Every entry TTL is inflated by this value so the effective keep-alive
	// never falls below the configured timeout. Zero disables suppression.
	uint64_t sync_suppress_timeout;
};

// Emission-side sync parameters for a module that synthesizes state-sync
// packets (ACL CREATE_STATE): the outer addressing of the frames it
// emits. The IPv6 source is left zeroed — the fwstate module stamps its
// own src_addr when forwarding internal frames.
struct fwstate_sync_emit_config {
	struct ether_addr dst_ether;
	uint8_t dst_addr_multicast[16];
	/**
	 * Multicast destination port in network byte order (big-endian).
	 * Same convention as fwstate_sync_config::port_multicast.
	 */
	uint16_t port_multicast;
	uint8_t dst_addr_unicast[16];
	/**
	 * Unicast destination port in network byte order (big-endian).
	 * Same convention as port_multicast - converted by the controlplane.
	 */
	uint16_t port_unicast;
};
