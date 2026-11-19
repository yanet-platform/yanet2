#pragma once

#include <stdbool.h>
#include <stdint.h>

#include <netinet/in.h>

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
 *
 * A map object holds the tables of exactly one module instance: two
 * instances never share one, so the keys need no instance discriminator.
 * The wan key already carries the instance's pool address besides.
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
	uint32_t local_addr;	     /**< pool IPv4 address (network order) */
	uint32_t remote_addr;	     /**< remote IPv4 address (network order) */
};

/**
 * @brief The session timeouts of a stateful NAT64 instance
 *
 * Two knobs in seconds: one for every session whatever its protocol,
 * and a longer one for a TCP session once it has carried an ACK — the
 * idle established connection RFC 6146 3.5.2.2 gives its two-hour
 * timer. Zero selects each knob's default.
 */
struct nat64state_timeouts {
	uint16_t timeout;
	uint16_t tcp_established;
};

#define NAT64STATE_TIMEOUT_DEFAULT 60
// RFC 6146 3.5.2.2: the two-hour TCP_est timer plus the four-minute
// transitory one.
#define NAT64STATE_TCP_ESTABLISHED_DEFAULT 7440
// The largest timeout the uint16_t knob can represent; values above it
// are refused at the config boundary instead of silently narrowing.
#define NAT64STATE_TIMEOUT_MAX 65535

_Static_assert(
	NAT64STATE_TIMEOUT_MAX <= UINT16_MAX,
	"the timeout cap must stay representable in the uint16_t knob"
);

/**
 * @brief Value of a session entry in either table
 *
 * A lan entry carries the pool allocation (ip4 + port) chosen at flow
 * creation; a wan entry carries the original IPv6 source (src6) a return
 * packet translates back to, under the NAT64 prefix (prefix) the
 * outbound packet actually used — with several prefixes configured, each
 * session returns under the one it left with. The fields of the other
 * side stay zeroed.
 */
struct nat64state_value {
	/** Timestamp when the session was created */
	uint64_t created_at;
	/** Timestamp of the last refresh retained in the map */
	uint64_t updated_at;
	/** TTL (ns) of the last refresh; expiry is updated_at + last_ttl */
	uint64_t last_ttl : 48;
	uint64_t _pad0 : 16;
	/** Whether the TCP session is established: once set, stays set,
	 * and the session ages on the established timeout. The module
	 * sets it on an ACK in either direction and on a SYN answering
	 * the initial one (RFC 6146 3.5.2.2). */
	bool tcp_established;
	/** Pool IPv4 address (network order) of the lan-side allocation */
	uint32_t ip4;
	/** Pool port (host order) of the lan-side allocation */
	uint16_t port;
	/** Original client source port (host order) the wan-side reverse
	 * mapping restores on return traffic */
	uint16_t cli_port;
	/** Original IPv6 client address of the wan-side reverse mapping */
	uint8_t src6[16];
	/** The NAT64 prefix (12 bytes) the outbound packet used */
	uint8_t prefix[12];
};

/**
 * @brief The session TTL of a stateful NAT64 instance
 *
 * A TCP session that has carried an ACK ages on the established knob,
 * everything else on the general one; a zero knob selects its default
 * rather than an already-expired session.
 *
 * @param proto IP protocol number of the session
 * @param tcp_established whether the TCP session has carried an ACK
 * @param timeouts the timeout knobs
 *
 * @return TTL in nanoseconds
 */
static inline uint16_t
nat64state_timeout(uint16_t value, uint16_t fallback) {
	return value ? value : fallback;
}

static inline uint64_t
nat64state_entry_ttl(
	uint16_t proto,
	bool tcp_established,
	const struct nat64state_timeouts *t
) {
	if (proto == IPPROTO_TCP && tcp_established) {
		return (uint64_t)nat64state_timeout(
			       t->tcp_established,
			       NAT64STATE_TCP_ESTABLISHED_DEFAULT
		       ) *
		       1000000000ull;
	}
	return (uint64_t
	       )nat64state_timeout(t->timeout, NAT64STATE_TIMEOUT_DEFAULT) *
	       1000000000ull;
}
