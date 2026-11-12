#pragma once

#include <stdint.h>

#include <netinet/in.h>

#include "types.h"

/**
 * @file nat64state.h
 * @brief Session keys and values of the stateful NAT64 map objects
 * (nat64state_lan_map, nat64state_wan_map).
 *
 * The lan table is keyed by the original IPv6 flow and answers "which pool
 * identity does this client flow translate to". The wan table is keyed by
 * the translated IPv4 flow and answers "which IPv6 client does this return
 * packet belong to". Both live in fwstate map objects and expire through
 * the same layer-sweep machinery as firewall state.
 */

/**
 * @brief Key of a lan-table (IPv6) session entry
 *
 * The transport fields are host-order and shared with the fw_state_key_hdr
 * layout so the same 5-tuple helpers apply. Addresses are network-order
 * bytes.
 */
struct nat64state_lan_key {
	struct fw_state_key_hdr hdr; /**< proto, source port, destination port
					 as seen on the IPv6 side */
	uint32_t nat64s_id;	     /**< owning module instance */
	uint8_t src_addr[16];	     /**< IPv6 client address */
	uint8_t dst_addr[16];	     /**< IPv6 destination (prefix + IPv4) */
};

/**
 * @brief Key of a wan-table (IPv4) session entry
 *
 * hdr.src_port is the allocated local (pool) port and hdr.dst_port the
 * remote port, matching the translated packet's tuple.
 */
struct nat64state_wan_key {
	struct fw_state_key_hdr hdr; /**< proto, local port, remote port */
	uint32_t nat64s_id;	     /**< owning module instance */
	uint32_t local_addr;	     /**< pool IPv4 address (network order) */
	uint32_t remote_addr;	     /**< remote IPv4 address (network order) */
};

/**
 * @brief Session timeout knobs of a stateful NAT64 instance
 *
 * Fields are seconds, matching the fwstate timeouts; zero selects the
 * default.
 */
struct nat64state_timeouts {
	uint16_t tcp_syn;
	uint16_t tcp_ack;
	uint16_t tcp_fin;
	uint16_t udp;
	uint16_t icmp;
	uint16_t other;
};

#define NAT64STATE_TIMEOUT_DEFAULT 60
#define NAT64STATE_TIMEOUT_MAX 3600

/**
 * @brief Value of a session entry in either table
 *
 * A lan entry carries the pool allocation (ip4 + port) chosen at flow
 * creation; a wan entry carries the original IPv6 source (src6) a return
 * packet translates back to. The other union side stays zeroed.
 */
struct nat64state_value {
	/** Timestamp when the session was created */
	uint64_t created_at;
	/** Timestamp of the last refresh retained in the map */
	uint64_t updated_at;
	/** TTL (ns) of the last refresh; expiry is updated_at + last_ttl */
	uint64_t last_ttl : 48;
	uint64_t _pad0 : 16;
	/** TCP flags seen on the session, in fwstate_flags_from_tcp encoding */
	uint8_t tcp_flags;
	uint8_t _pad1[3];
	/** Pool IPv4 address (network order) of the lan-side allocation */
	uint32_t ip4;
	/** Pool port (host order) of the lan-side allocation */
	uint16_t port;
	uint16_t _pad2;
	/** Original IPv6 client address of the wan-side reverse mapping */
	uint8_t src6[16];
};

/**
 * @brief Select a session TTL from the protocol and the TCP flags seen
 *
 * SYN-only flows age on the syn timeout, FIN-seen flows on the fin timeout,
 * established TCP on the ack timeout, and non-TCP on its protocol timeout.
 *
 * @param proto IP protocol number of the session
 * @param tcp_flags raw TCP flags in fwstate_flags_from_tcp encoding
 * @param timeouts per-protocol timeout knobs
 *
 * @return TTL in nanoseconds
 */
static inline uint64_t
nat64state_entry_ttl(
	uint16_t proto, uint8_t tcp_flags, const struct nat64state_timeouts *t
) {
	if (proto == IPPROTO_TCP) {
		if (tcp_flags & FWSTATE_FIN) {
			return (uint64_t)t->tcp_fin * 1000000000ull;
		}
		if (!(tcp_flags & FWSTATE_ACK)) {
			return (uint64_t)t->tcp_syn * 1000000000ull;
		}
		return (uint64_t)t->tcp_ack * 1000000000ull;
	}
	if (proto == IPPROTO_UDP) {
		return (uint64_t)t->udp * 1000000000ull;
	}
	if (proto == IPPROTO_ICMP || proto == IPPROTO_ICMPV6) {
		return (uint64_t)t->icmp * 1000000000ull;
	}
	return (uint64_t)t->other * 1000000000ull;
}
