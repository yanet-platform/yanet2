#pragma once

#include <inttypes.h>
#include <stdint.h>

#include "lib/dataplane/packet/packet.h"

/**
 * @file nat64.h
 * @brief Shared RFC7915 IP/ICMP translation core for the NAT64 dataplane
 * modules.
 *
 * Both the stateless and the stateful NAT64 modules translate packets with
 * the same algorithm; they differ only in how the counterpart address of a
 * packet is resolved (static LPM mappings or a dynamic session table). This
 * header carries the address-resolution-independent half: header and ICMP
 * translation, checksum recalculation and MTU handling, parameterized by a
 * caller-supplied context.
 */

/**
 * @brief Format string for printing IPv4 addresses in dotted-decimal form
 */
#define IPv4_BYTES_FMT "%" PRIu8 ".%" PRIu8 ".%" PRIu8 ".%" PRIu8

/**
 * @brief Extract the four bytes of a network-order IPv4 address
 *
 * Use with printf and IPv4_BYTES_FMT; the first extracted byte is the most
 * significant and prints as the first octet.
 *
 * @param addr 32-bit integer representing an IPv4 address
 */
#define IPv4_BYTES(addr)                                                       \
	(uint8_t)(((addr) >> 24) & 0xFF), (uint8_t)(((addr) >> 16) & 0xFF),    \
		(uint8_t)(((addr) >> 8) & 0xFF), (uint8_t)((addr) & 0xFF)

/**
 * @brief Extract the four bytes of an IPv4 address in reverse byte order
 *
 * Use this when the address integer is in host (little-endian) order.
 *
 * @param addr 32-bit integer representing an IPv4 address
 */
#define IPv4_BYTES_LE(addr)                                                    \
	(uint8_t)((addr) & 0xFF), (uint8_t)(((addr) >> 8) & 0xFF),             \
		(uint8_t)(((addr) >> 16) & 0xFF),                              \
		(uint8_t)(((addr) >> 24) & 0xFF)

/**
 * @brief Format string for printing IPv6 addresses in colon-separated form
 */
#define IPv6_BYTES_FMT                                                         \
	"%02x%02x:%02x%02x:%02x%02x:%02x%02x:"                                 \
	"%02x%02x:%02x%02x:%02x%02x:%02x%02x"

/**
 * @brief Expand a 16-byte IPv6 address into printf arguments
 *
 * Use with printf and IPv6_BYTES_FMT.
 *
 * @param addr Array of 16 uint8_t values representing an IPv6 address
 */
#define IPv6_BYTES(addr)                                                       \
	addr[0], addr[1], addr[2], addr[3], addr[4], addr[5], addr[6],         \
		addr[7], addr[8], addr[9], addr[10], addr[11], addr[12],       \
		addr[13], addr[14], addr[15]

/**
 * @brief Format string for printing IPv6 addresses as 16-bit words
 */
#define IPv6_BYTES_FMT_U32                                                     \
	"%04x:%04x:%04x:%04x:"                                                 \
	"%04x:%04x:%04x:%04x"

/**
 * @brief Expand an IPv6 address held as four 32-bit words into printf
 * arguments
 *
 * Use with printf and IPv6_BYTES_FMT_U32.
 *
 * @param addr Array of 4 uint32_t values representing an IPv6 address
 */
#define IPv6_BYTES_U32(addr)                                                   \
	(addr[0] >> 16), (addr[0] & 0xFFFF), (addr[1] >> 16),                  \
		(addr[1] & 0xFFFF), (addr[2] >> 16), (addr[2] & 0xFFFF),       \
		(addr[3] >> 16), (addr[3] & 0xFFFF)

/**
 * @brief Translation context supplied by the owning module
 *
 * Holds the per-module knobs the generic translation code needs: the MTU
 * limits used for RFC1191 adjustments and a pointer to the module's
 * malformed-packet counter, bumped whenever a guard rejects a packet.
 */
struct nat64_ctx {
	uint16_t mtu_ipv4; /**< IPv4 MTU limit, 0 disables the check */
	uint16_t mtu_ipv6; /**< IPv6 MTU limit, 0 disables the check */

	/**
	 * @brief Malformed-packet counter of the owning module
	 *
	 * Updated with relaxed atomics: dataplane workers may run
	 * concurrently.
	 */
	uint64_t *malformed;
};

/**
 * @brief Count a malformed packet
 *
 * @param ctx Translation context carrying the counter
 */
static inline void
nat64_stats_malformed(const struct nat64_ctx *ctx) {
	__atomic_add_fetch(ctx->malformed, 1, __ATOMIC_RELAXED);
}

/**
 * @brief Translate an IPv6 packet to IPv4 in place
 *
 * Translates the outer IPv6 header (after its extension headers) to IPv4,
 * translates the transport header (ICMP/UDP/TCP, including embedded packets
 * inside ICMP errors) and recalculates checksums, per RFC7915.
 *
 * The source IPv4 address of the translation is @p src_ip4: how it maps to
 * the original IPv6 source is the caller's policy. The destination is the
 * last 4 bytes of the IPv6 destination address.
 *
 * @param ctx Translation context (MTU limits, malformed counter)
 * @param packet Packet with parsed network/transport header offsets
 * @param src_ip4 IPv4 source address in network byte order
 *
 * @return 0 on success, -1 on translation failure (packet must be dropped)
 */
int
nat64_translate_v6_to_v4(
	const struct nat64_ctx *ctx, struct packet *packet, uint32_t src_ip4
);

/**
 * @brief Translate an IPv4 packet to IPv6 in place
 *
 * Handles IPv4 options per RFC7915 (source-route options are dropped),
 * prepends or trims headroom for the IPv6 header and any fragment header,
 * translates the transport header (including embedded packets inside ICMP
 * errors) and recalculates checksums.
 *
 * The source IPv6 address of the translation is @p ip6 and the destination
 * is the IPv4-mapped @p prefix form of the IPv4 destination: how they are
 * chosen is the caller's policy.
 *
 * @param ctx Translation context (MTU limits, malformed counter)
 * @param packet Packet with parsed network/transport header offsets
 * @param prefix 12-byte IPv6 prefix for the IPv4-mapped destination
 * @param ip6 IPv6 source address (16 bytes)
 *
 * @return 0 on success, -1 on translation failure (packet must be dropped)
 */
int
nat64_translate_v4_to_v6(
	const struct nat64_ctx *ctx,
	struct packet *packet,
	uint8_t *prefix,
	uint8_t *ip6
);
