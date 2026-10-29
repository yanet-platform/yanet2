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

// Largest number of synchronization endpoints one config carries. The
// controlplane refuses longer lists before the config reaches shm.
#define FWSTATE_SYNC_ENDPOINTS_MAX 8

/**
 * One synchronization endpoint: a source and destination IPv6 address and
 * the destination port of locally emitted sync packets.
 *
 * A multicast destination doubles as the receive contract: an incoming
 * packet is claimed when it matches any endpoint's destination address and
 * port (and carries a multicast Ethernet destination). A unicast
 * destination is emission-only. The Ethernet header of emitted packets
 * stays zeroed: the module downstream of fwstate, route, resolves and
 * stamps both MAC addresses.
 */
struct fwstate_sync_endpoint {
	// IPv6 source stamped on packets emitted toward dst_addr.
	uint8_t src_addr[16];

	// IPv6 destination of emitted packets.
	uint8_t dst_addr[16];

	/**
	 * Destination port in network byte order (big-endian). Stored in BE
	 * so it can be used directly in UDP header fields and compared with
	 * udp_hdr->dst_port without conversion. The controlplane performs
	 * the host-to-network conversion when populating this field. Zero
	 * disables the endpoint.
	 */
	uint16_t port;
};

struct fwstate_sync_config {
	// Synchronization endpoints; the first endpoint_count entries are
	// initialized.
	struct fwstate_sync_endpoint endpoints[FWSTATE_SYNC_ENDPOINTS_MAX];
	uint32_t endpoint_count;

	struct fwstate_timeouts timeouts;

	// Sync suppression window in nanoseconds. A sync frame for an already
	// alive entry whose new expiry deadline lands within this window of the
	// entry's current deadline is discarded (the fwmap record is left
	// untouched), debouncing refreshes for frequently touched sessions.
	// Every entry TTL is inflated by this value so the effective keep-alive
	// never falls below the configured timeout. Zero disables suppression.
	uint64_t sync_suppress_timeout;

	// Largest IPv6 packet, headers included, of one locally emitted sync
	// packet; it bounds how many frames a packet batches. Zero selects
	// the default of 1500.
	uint16_t sync_mtu;
};
