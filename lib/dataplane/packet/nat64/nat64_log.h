#pragma once

#include <rte_log.h>
#include <rte_memcpy.h>

/**
 * @brief Log type of the shared NAT64 translation core
 *
 * Registered in nat64_translate.c by a DPDK constructor; shared by the
 * library and the dataplane modules that include this header.
 */
extern int nat64_logtype;
#define RTE_LOGTYPE_NAT64 nat64_logtype

/**
 * @file nat64_log.h
 * @brief Debug logging macros of the shared NAT64 translation core.
 *
 * Only the dataplane translation units include this header; the public
 * nat64.h stays DPDK-free for control-plane consumers.
 */

#ifdef DEBUG_NAT64
#define LOG_DBGX(app, f, fmt, ...)                                             \
	do {                                                                   \
		f RTE_LOG(DEBUG, app, fmt, ##__VA_ARGS__);                     \
	} while (0)
#define LOG_DBG(app, fmt, ...)                                                 \
	do {                                                                   \
		RTE_LOG(DEBUG, app, fmt, ##__VA_ARGS__);                       \
	} while (0)
#else
#define LOG_DBGX(app, f, fmt, ...) (void)(0)
#define LOG_DBG(app, fmt, ...) (void)(0)
#endif

/**
 * @brief Macro to set an IPv4-mapped IPv6 address
 *
 * This macro sets an IPv6 address to be a mapping of an IPv4 address.
 * It copies a 12-byte prefix into the first 12 bytes of the IPv6 address
 * and the 4-byte IPv4 address into the last 4 bytes.
 *
 * @param ip6 Pointer to the array of 16 uint8_t values representing the IPv6
 * address.
 * @param prefix Pointer to the 12-byte array representing the IPv6 prefix.
 * @param ip4 Pointer to the 4-byte array representing the IPv4 address.
 *
 * @example
 * ```c
 * const uint8_t ipv6_prefix[12] = { 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0xFF };
 * // IPv4-mapped IPv6 prefix uint8_t ipv4_addr[4] = { 192, 168, 1, 1 }; //
 * 192.168.1.1 uint8_t ipv6_addr[16]; SET_IPV4_MAPPED_IPV6(ipv6_addr,
 * ipv6_prefix, ipv4_addr); printf(IPv6_BYTES_FMT "\n", IPv6_BYTES(ipv6_addr));
 * // Output: ::ffff:192.168.1.1
 * ```
 */
#define SET_IPV4_MAPPED_IPV6(ip6, prefix, ip4)                                 \
	do {                                                                   \
		rte_memcpy(ip6, prefix, 12);                                   \
		rte_memcpy(ip6[12], ip4, 4);                                   \
	} while (0)
